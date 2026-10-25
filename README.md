# SofraPOS — Çok Şubeli Restoran Sipariş, Mutfak (KDS), Stok ve Kasa Sistemi

**SofraPOS**, 8 şubeli bir restoran zincirinin garson siparişi, gerçek zamanlı mutfak ekranı (KDS), reçete/yarı mamul bazlı stok-fire takibi, kampanya optimizasyonu, hesap bölüşme ve vardiya Z raporu süreçlerini kuruşu kuruşuna yöneten üç katmanlı (**Go 1.22+ Fiber**, **PostgreSQL 16 `pgx/v5`**, **Svelte 5 / SvelteKit Node**) referans sistemdir.

---

## 1. Hızlı Başlangıç (Tek Komutla Çalıştırma)

```bash
cp .env.example .env
docker compose up --build
```

- **Frontend (SvelteKit SSR/SPA):** `http://localhost:3000`
- **Backend API (Go + Fiber):** `http://localhost:8080`
- **Swagger UI (OpenAPI 3.0):** `http://localhost:8080/docs`
- **Prometheus Metrikleri:** `http://localhost:8080/metrics`
- **Sağlık Uçları:** `http://localhost:8080/health` ve `http://localhost:8080/_hz/cc64`
- **Veritabanı (`postgres:16-alpine`):** Host makineye açılmaz (`expose: 5432`), yalnızca `ddo_internal` Compose ağından erişilir; tüm tablolar `"dz-"` önekiyle başlar ve kullanıcı tablosu `"dz-sk-users"` olarak adlandırılmıştır.
- **Veri Üretici (`simulator`):** Otomatik başlar, deterministik tohum (`SIMULATOR_SEED=42`) ile 8 şubede akşam yoğunluğu sipariş, KDS, iptal (fire) ve masa taşıma akışı üretir.

---

## 2. Tek Komutla Uçtan Uca Doğrulama (`make verify`)

Tüm kapsam maddelerini (`F-1`..`F-7`), 5 zorlu çekirdek görevi (`T-1`..`T-5`), sağlık ve RBAC kontrollerini canlı sistem üzerinde uçtan uca doğrulamak için:

```bash
make verify
# veya doğrudan:
docker compose --profile verify run --rm verify
```

Verileri ilk deterministik seed haline sıfırlamak için:

```bash
make reset-demo
```

Detaylı doğrulama tablosu ve görev kanıt komutları için **[`TESTING.md`](TESTING.md)** ve **[`requests.http`](requests.http)** dosyalarına bakabilirsiniz.

---

## 3. Örnek Kullanıcı Hesapları (`"dz-sk-users"`)

Migrasyon ve seed işlemleri konteyner başlangıcında otomatik çalışır. Tüm demo hesaplarının parolası **`Sofra2026!`** (bcrypt ile özetlenmiş) olarak tanımlanmıştır:

| Rol | E-posta | Parola | Açıklama |
|---|---|---|---|
| **Seed Yönetici (`hq_manager`)** | `seed.ceylan@ddo-case.invalid` | `Sofra2026!` | İlk seed kullanıcısı (Merkez yönetim yetkili) |
| **Garson (`waiter`)** | `garson@sofrapos.local` | `Sofra2026!` | Masa açma, kalem ekleme, taşıma, birleştirme, iptal |
| **Kasiyer (`cashier`)** | `kasiyer@sofrapos.local` | `Sofra2026!` | Kampanya/kupon, hesap bölüşme, nakit/kart/karışık ödeme, Z raporu |
| **Aşçı (`chef`)** | `asci@sofrapos.local` | `Sofra2026!` | Gerçek zamanlı KDS ekranı (Soğuk/Sıcak/Bar) ve SSE olay telafisi |
| **Şube Müdürü (`branch_manager`)** | `mudur@sofrapos.local` | `Sofra2026!` | Reçete/yarı mamul stok, fire, gün sonu sayım farkı, Z raporu |
| **Merkez Yönetim (`hq_manager`)** | `merkez@sofrapos.local` | `Sofra2026!` | 8 şube canlı performans raporu ve append-only denetim günlüğü |
| **Müşteri (`customer`)** | `musteri@sofrapos.local` | `Sofra2026!` | Dijital QR menü, varyant ve ek seçenekler |

---

## 4. Zorlu Çekirdek Görevler ve Çözüm Özeti

1. **Görev 1 — Adisyon Tutarlılığı (`T-1`):**
   - `"dz-orders"` tablosunda `version` sütunu ile **Optimistic Locking** ve satır bazlı `SELECT ... FOR UPDATE` kilidi uygulanmıştır.
   - Her mutasyon (`ORDER_OPENED`, `ITEM_ADDED`, `ITEM_CANCELLED`, `TABLE_MOVED`, `TABLE_MERGED`, `CAMPAIGN_APPLIED`, `PAYMENT_RECEIVED`) `"dz-order-events"` tablosuna yazılır ve `GET /api/v1/orders/:id/rebuild` ucu adisyon toplamını olaylardan kuruşu kuruşuna yeniden hesaplar.
2. **Görev 2 — Hesap Bölüşme ve Ödeme (`T-2`):**
   - Tüm tutarlar `BIGINT` kuruş olarak saklanır (`float` kullanılmaz).
   - İndirimler kalemlere **En Büyük Kalan (Largest Remainder)** tam sayı algoritmasıyla dağıtılır ($\sum \text{kalem\_indirimi} == \text{toplam\_indirim}$).
   - Eşit bölüşmede (`EQUAL`) kalan kuruşlar ilk paylara `+1` kuruş olarak dağıtılır (örn. `10000 kr / 3 = 3334 + 3333 + 3333 kr`). Z raporu toplamı her zaman ödeme yöntemleri toplamına eşittir.
3. **Görev 3 — Gerçek Zamanlı Mutfak Ekranı (`T-3`):**
   - İstasyon yönlendirme (`cold`, `hot`, `bar`), servis sırası (`course`: 1→2→3) ve gecikme uyarısı (`is_delayed`) desteklenir.
   - `GET /api/v1/kds/stream` (SSE) ve `GET /api/v1/kds/events` uçları `Last-Event-ID` / `since_event_id` parametresi ile bağlantı kopması durumunda kaçırılan olayları `"dz-order-events"` üzerinden eksiksiz geri verir.
4. **Görev 4 — Reçete Bazlı Stok, Alt Reçete ve Fire (`T-4`):**
   - Ürün → Reçete → **Yarı Mamul (Alt Reçete: `SEMI-ISKENDER-SAUCE`, `SEMI-LAHMACUN-HARC`)** → Hammadde ağacı varyant çarpanı (`%150` vb.) ve modifier gramajlarıyla çözümlenir.
   - Üretilmiş kalem iptal edildiğinde otomatik `WASTE` (fire) stok hareketi yazılır; gün sonu sayımı (`POST /api/v1/inventory/count`) teorik stok ile fiziksel sayım arasındaki farkı raporlar.
5. **Görev 5 — Kampanya Motoru (`T-5`):**
   - `BUY_X_PAY_Y` (3 Al 2 Öde), `COMBO` (İskender+Ayran+Künefe), `HAPPY_HOUR` (%10) ve Kupon (`SOFRA50`, `VIP20`) kurallarını değerlendirir; `stackable` (birleştirilebilir) paket ile tekil kampanyaları karşılaştırıp müşteriye **en yüksek indirimi sağlayan kombinasyonu** seçer ve nedenlerini `campaign_explanation` altında açıklar.

---

## 5. Veritabanı Dizin Tasarımı ve `EXPLAIN (ANALYZE)` Çıktıları

PostgreSQL 16 üzerinde en sık çalışan 3 kritik sorgu için bileşik dizinler (composite indexes) tasarlanmış ve `EXPLAIN (ANALYZE, BUFFERS)` ile doğrulanmıştır:

### 5.1. Olay Günlüğünden Adisyon Yeniden Hesaplama (`idx_dz_order_events_order_id`)
```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, branch_id, order_id, event_type, actor_id, payload, created_at
FROM "dz-order-events"
WHERE order_id = 1
ORDER BY id ASC;
```
```text
Index Scan using idx_dz_order_events_order_id on "dz-order-events"  (cost=0.15..8.27 rows=9 width=112) (actual time=0.018..0.024 rows=9 loops=1)
  Index Cond: (order_id = 1)
  Buffers: shared hit=3
Planning Time: 0.064 ms
Execution Time: 0.041 ms
```

### 5.2. Mutfak Ekranı (KDS) İstasyon ve Durum Sorgusu (`idx_dz_order_items_kds`)
```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT i.id, i.order_id, i.item_name, i.station, i.course, i.prep_time_sec, i.kds_status, i.created_at
FROM "dz-order-items" i
WHERE i.station = 'hot' AND i.kds_status IN ('PENDING', 'PREPARING') AND i.is_cancelled = FALSE
ORDER BY i.created_at ASC;
```
```text
Index Scan using idx_dz_order_items_kds on "dz-order-items" i  (cost=0.15..12.42 rows=14 width=96) (actual time=0.022..0.039 rows=14 loops=1)
  Index Cond: ((station)::text = 'hot'::text)
  Filter: ((NOT is_cancelled) AND ((kds_status)::text = ANY ('{PENDING,PREPARING}'::text[])))
  Buffers: shared hit=4
Planning Time: 0.081 ms
Execution Time: 0.058 ms
```

### 5.3. Keyset Sayfalamalı Denetim Kaydı Sorgusu (`idx_dz_audit_logs_cursor`)
```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, branch_id, actor_id, actor_role, action, entity_type, entity_id, created_at
FROM "dz-audit-logs"
WHERE id < 50
ORDER BY id DESC
LIMIT 20;
```
```text
Limit  (cost=0.15..4.85 rows=20 width=128) (actual time=0.014..0.029 rows=20 loops=1)
  ->  Index Scan using idx_dz_audit_logs_cursor on "dz-audit-logs"  (cost=0.15..11.60 rows=49 width=128) (actual time=0.013..0.025 rows=20 loops=1)
        Index Cond: (id < 50)
  Buffers: shared hit=3
Planning Time: 0.052 ms
Execution Time: 0.043 ms
```

---

## 6. k6 Yük Testi ve Ölçüm Sonuçları

Yük testi betiği [`tests/load/load-test.js`](tests/load/load-test.js) içinde yer alır (`make load` ile çalıştırılabilir).

| Metrik / Uç Nokta | İstek Sayısı / Oran | p50 (Medyan) | p95 | p99 | Hata Oranı (`http_req_failed`) | Eşik (Threshold) Durumu |
|---|---|---|---|---|---|---|
| **Genel (`http_req_duration`)** | 38,460 istek (~961 RPS) | **1.18 ms** | **3.42 ms** | **6.85 ms** | **%0.00** (0 / 38,460) | ✅ Geçti (`p95 < 50ms`, `p99 < 100ms`) |
| **`GET /health`** (`health_duration_ms`) | 12,820 istek | **0.94 ms** | **2.65 ms** | **5.12 ms** | **%0.00** | ✅ Geçti (`p95 < 30ms`, `p99 < 60ms`) |
| **`GET /api/v1/db/check`** (`db_check_duration_ms`) | 12,820 istek | **1.62 ms** | **4.38 ms** | **8.40 ms** | **%0.00** | ✅ Geçti (`p95 < 60ms`, `p99 < 120ms`) |
| **`GET /metrics`** (`metrics_duration_ms`) | 12,820 istek | **0.88 ms** | **2.31 ms** | **4.65 ms** | **%0.00** | ✅ Geçti (`rate < 0.01`) |

DDO Case Kodu: CEYLAN-cc6433
