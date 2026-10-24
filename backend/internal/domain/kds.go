package domain

import (
	"sort"
	"sync"
	"time"
)

// KDSTicketItem represents a single kitchen order line item with station, course, and delay telemetry.
type KDSTicketItem struct {
	OrderItemID        int64     `json:"order_item_id"`
	OrderID            int64     `json:"order_id"`
	BranchID           int64     `json:"branch_id"`
	TableCode          string    `json:"table_code"`
	ItemName           string    `json:"item_name"`
	VariantName        string    `json:"variant_name"`
	ModifierNames      string    `json:"modifier_names"`
	Quantity           int       `json:"quantity"`
	Station            string    `json:"station"` // "cold", "hot", "bar"
	Course             int       `json:"course"`  // 1=Starter, 2=Main, 3=Dessert/Bar
	PrepTimeSec        int       `json:"prep_time_sec"`
	EstimatedTotalSec  int       `json:"estimated_total_sec"`
	ElapsedSec         int       `json:"elapsed_sec"`
	IsDelayed          bool      `json:"is_delayed"`
	KDSStatus          string    `json:"kds_status"` // "PENDING", "PREPARING", "READY", "SERVED", "CANCELLED"
	CreatedAt          time.Time `json:"created_at"`
}

// EnrichAndSortKDSItems calculates elapsed time, queue-adjusted prep time estimate, delay warning,
// and sorts items by course (1 -> 2 -> 3) then oldest-first.
func EnrichAndSortKDSItems(items []KDSTicketItem, now time.Time) []KDSTicketItem {
	stationActiveLoad := make(map[string]int)
	for _, it := range items {
		if it.KDSStatus == "PENDING" || it.KDSStatus == "PREPARING" {
			stationActiveLoad[it.Station] += it.Quantity
		}
	}

	out := make([]KDSTicketItem, len(items))
	for i, it := range items {
		elapsed := int(now.Sub(it.CreatedAt).Seconds())
		if elapsed < 0 {
			elapsed = 0
		}
		queueFactor := stationActiveLoad[it.Station]
		extraQueueSec := 0
		if queueFactor > 3 {
			extraQueueSec = (queueFactor - 3) * 30
		}
		estTotal := it.PrepTimeSec + extraQueueSec
		isDelayed := (it.KDSStatus == "PENDING" || it.KDSStatus == "PREPARING") && elapsed > it.PrepTimeSec

		it.ElapsedSec = elapsed
		it.EstimatedTotalSec = estTotal
		it.IsDelayed = isDelayed
		out[i] = it
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDelayed != out[j].IsDelayed {
			return out[i].IsDelayed
		}
		if out[i].Course != out[j].Course {
			return out[i].Course < out[j].Course
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})

	return out
}

// KDSStreamEvent represents a real-time event pushed over SSE/WebSocket with a monotonic ID.
type KDSStreamEvent struct {
	EventID   int64     `json:"event_id"`
	BranchID  int64     `json:"branch_id"`
	OrderID   int64     `json:"order_id"`
	Station   string    `json:"station"`
	EventType string    `json:"event_type"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// KDSBroker manages real-time subscribers and an in-memory replay buffer for reconnect recovery.
type KDSBroker struct {
	mu          sync.RWMutex
	history     []KDSStreamEvent
	maxHistory  int
	subscribers map[int]chan KDSStreamEvent
	nextSubID   int
}

// NewKDSBroker initializes a thread-safe KDS event broker with replay buffer capacity.
func NewKDSBroker(maxHistory int) *KDSBroker {
	if maxHistory <= 0 {
		maxHistory = 500
	}
	return &KDSBroker{
		history:     make([]KDSStreamEvent, 0, maxHistory),
		maxHistory:  maxHistory,
		subscribers: make(map[int]chan KDSStreamEvent),
		nextSubID:   1,
	}
}

// Publish records an event in the replay buffer and broadcasts it to all active subscribers.
func (b *KDSBroker) Publish(ev KDSStreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.history) >= b.maxHistory {
		b.history = b.history[1:]
	}
	b.history = append(b.history, ev)

	for _, ch := range b.subscribers {
		select {
		case ch <- ev:
		default:
			// Drop if slow consumer buffer is full; client can replay via Last-Event-ID
		}
	}
}

// ReplaySince returns all buffered events for branchID with EventID > sinceEventID in chronological order.
func (b *KDSBroker) ReplaySince(branchID, sinceEventID int64, station string) []KDSStreamEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var missed []KDSStreamEvent
	for _, ev := range b.history {
		if (branchID == 0 || ev.BranchID == branchID) && ev.EventID > sinceEventID {
			if station == "" || station == "all" || ev.Station == "" || ev.Station == station {
				missed = append(missed, ev)
			}
		}
	}
	return missed
}

// Subscribe registers a live listener channel and returns an unsubscribe function.
func (b *KDSBroker) Subscribe() (<-chan KDSStreamEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subID := b.nextSubID
	b.nextSubID++
	ch := make(chan KDSStreamEvent, 64)
	b.subscribers[subID] = ch

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subscribers[subID]; ok {
			delete(b.subscribers, subID)
			close(existing)
		}
	}

	return ch, unsubscribe
}
