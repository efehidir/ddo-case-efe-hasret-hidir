export interface PoolStats {
	total_conns: number;
	idle_conns: number;
	acquired_conns: number;
	max_conns: number;
}

export interface DatabaseHealth {
	status: 'up' | 'down';
	driver: string;
	latency_ms: number;
	pool: PoolStats;
	error?: string;
}

export interface HealthResponse {
	status: 'healthy' | 'degraded';
	service: string;
	version: string;
	environment: string;
	timestamp: string;
	uptime_seconds: number;
	database: DatabaseHealth;
}

export interface DatabaseDiagnosticResponse {
	connected: boolean;
	driver: string;
	server_version: string;
	current_database: string;
	current_user: string;
	server_time: string;
	latency_ms: number;
	total_checks: number;
	pool: PoolStats;
	error?: string;
}

export interface UserProfile {
	id: number;
	branch_id: number;
	email: string;
	full_name: string;
	role: 'waiter' | 'cashier' | 'chef' | 'branch_manager' | 'hq_manager' | 'customer';
}

export interface BranchInfo {
	id: number;
	code: string;
	name: string;
	city: string;
	is_active: boolean;
}

export interface MenuVariant {
	id: number;
	name: string;
	price_delta_kurus: number;
	recipe_multiplier_pct: number;
}

export interface MenuModifier {
	id: number;
	name: string;
	price_delta_kurus: number;
	ingredient_code: string;
	extra_qty_grams: number;
}

export interface MenuItem {
	id: number;
	category_id: number;
	code: string;
	name: string;
	description: string;
	base_price_kurus: number;
	station: 'cold' | 'hot' | 'bar';
	course: number;
	prep_time_sec: number;
	is_available: boolean;
	variants: MenuVariant[];
	modifiers: MenuModifier[];
}

export interface TableItem {
	id: number;
	branch_id: number;
	code: string;
	name: string;
	capacity: number;
	status: 'EMPTY' | 'OCCUPIED' | 'MERGED';
	active_order_id: number;
	version: number;
	order_total_kurus: number;
	order_paid_kurus: number;
	order_version: number;
}

export interface CampaignDecision {
	code: string;
	name: string;
	rule_type: string;
	applied: boolean;
	discount_kurus: number;
	reason: string;
}

export interface OrderLineItem {
	id: number;
	menu_item_id: number;
	item_name: string;
	variant_id: number;
	variant_name: string;
	modifier_names: string;
	quantity: number;
	unit_price_kurus: number;
	line_subtotal_kurus: number;
	allocated_discount_kurus: number;
	net_total_kurus: number;
	paid_kurus: number;
	remaining_kurus: number;
	station: string;
	course: number;
	kds_status: string;
	is_produced: boolean;
	is_cancelled: boolean;
	cancel_reason: string;
}

export interface OrderDetail {
	id: number;
	branch_id: number;
	table_id: number;
	table_code: string;
	status: string;
	version: number;
	subtotal_kurus: number;
	discount_kurus: number;
	total_kurus: number;
	paid_kurus: number;
	remaining_kurus: number;
	tip_kurus: number;
	coupon_code: string;
	campaign_explanation: CampaignDecision[];
	items: OrderLineItem[];
	payments: Array<{
		id: number;
		method: string;
		split_mode: string;
		amount_kurus: number;
		tip_kurus: number;
		created_at: string;
	}>;
}

export interface KDSTicket {
	order_item_id: number;
	order_id: number;
	branch_id: number;
	table_code: string;
	item_name: string;
	variant_name: string;
	modifier_names: string;
	quantity: number;
	station: string;
	course: number;
	prep_time_sec: number;
	estimated_total_sec: number;
	elapsed_sec: number;
	is_delayed: boolean;
	kds_status: string;
}
