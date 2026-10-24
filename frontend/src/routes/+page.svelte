<script lang="ts">
	import type {
		BranchInfo,
		DatabaseDiagnosticResponse,
		HealthResponse,
		KDSTicket,
		MenuItem,
		OrderDetail,
		TableItem,
		UserProfile
	} from '#lib/types';

	let { data } = $props();

	let health = $state<HealthResponse | null>(null);
	let dbDiag = $state<DatabaseDiagnosticResponse | null>(null);
	let globalError = $state<string | null>(null);
	let actionNotice = $state<string | null>(null);
	let isLoading = $state(false);

	// Auth state
	let accessToken = $state<string>('');
	let currentUser = $state<UserProfile | null>(null);
	let loginEmail = $state('garson@sofrapos.local');
	let loginPassword = $state('Sofra2026!');

	// Branch & Active Tab
	let selectedBranchId = $state<number>(1);
	let branches = $state<BranchInfo[]>([]);
	let activeTab = $state<'tables' | 'kds' | 'cashier' | 'inventory' | 'hq' | 'menu' | 'infra'>('tables');

	// Domain state
	let menuItems = $state<MenuItem[]>([]);
	let tables = $state<TableItem[]>([]);
	let selectedOrder = $state<OrderDetail | null>(null);
	let rebuildProof = $state<Record<string, unknown> | null>(null);

	// Add Item Form
	let addMenuItemId = $state<number>(1);
	let addVariantId = $state<number>(0);
	let addModifierIds = $state<number[]>([]);
	let addQuantity = $state<number>(1);
	let moveTargetTableId = $state<number>(5);
	let mergeSourceOrderId = $state<number>(0);

	// KDS state
	let kdsStation = $state<'all' | 'cold' | 'hot' | 'bar'>('all');
	let kdsItems = $state<KDSTicket[]>([]);
	let kdsLastEventId = $state<number>(0);
	let kdsReplayedEvents = $state<Array<Record<string, unknown>>>([]);

	// Cashier / Split / Campaign state
	let couponInput = $state('SOFRA50');
	let splitMode = $state<'FULL' | 'EQUAL' | 'BY_ITEM' | 'CUSTOM' | 'MIXED'>('FULL');
	let equalSplitParts = $state<number>(3);
	let equalSharesPreview = $state<number[]>([]);
	let selectedItemIdsForSplit = $state<number[]>([]);
	let paymentMethod = $state<'CASH' | 'CARD' | 'MIXED'>('CARD');
	let customAmountKurus = $state<number>(10000);
	let mixedCashKurus = $state<number>(10000);
	let mixedCardKurus = $state<number>(12000);
	let tipKurus = $state<number>(2000);
	let zReport = $state<Record<string, unknown> | null>(null);

	// Inventory & HQ state
	let ingredients = $state<Array<Record<string, unknown>>>([]);
	let stockMovements = $state<Array<Record<string, unknown>>>([]);
	let varianceReport = $state<Record<string, unknown> | null>(null);
	let hqReport = $state<Record<string, unknown> | null>(null);
	let auditLogs = $state<Array<Record<string, unknown>>>([]);

	const demoRoles = [
		{ role: 'waiter', label: 'Garson', email: 'garson@sofrapos.local', defaultTab: 'tables' as const },
		{ role: 'cashier', label: 'Kasiyer (Kasa)', email: 'kasiyer@sofrapos.local', defaultTab: 'cashier' as const },
		{ role: 'chef', label: 'Aşçı (Mutfak KDS)', email: 'asci@sofrapos.local', defaultTab: 'kds' as const },
		{ role: 'branch_manager', label: 'Şube Müdürü', email: 'mudur@sofrapos.local', defaultTab: 'inventory' as const },
		{ role: 'hq_manager', label: 'Merkez Yönetim', email: 'merkez@sofrapos.local', defaultTab: 'hq' as const },
		{ role: 'customer', label: 'Müşteri (QR Menü)', email: 'musteri@sofrapos.local', defaultTab: 'menu' as const }
	];

	$effect(() => {
		health = data.health;
		dbDiag = data.dbDiagnostics;
		if (data.errorMessage) {
			globalError = data.errorMessage;
		}
	});

	function formatTL(kurus: number | undefined | null): string {
		const val = Number(kurus ?? 0);
		return (val / 100).toFixed(2) + ' ₺';
	}

	function allowedTabsForRole(role: string | undefined): string[] {
		switch (role) {
			case 'waiter':
				return ['tables', 'kds', 'menu', 'infra'];
			case 'cashier':
				return ['cashier', 'tables', 'menu', 'infra'];
			case 'chef':
				return ['kds', 'inventory', 'menu', 'infra'];
			case 'branch_manager':
				return ['tables', 'cashier', 'kds', 'inventory', 'hq', 'menu', 'infra'];
			case 'hq_manager':
				return ['hq', 'inventory', 'cashier', 'tables', 'kds', 'menu', 'infra'];
			case 'customer':
				return ['menu', 'infra'];
			default:
				return ['menu', 'infra'];
		}
	}

	async function apiFetch(path: string, options: RequestInit = {}) {
		const headers = new Headers(options.headers || {});
		if (accessToken) {
			headers.set('Authorization', `Bearer ${accessToken}`);
		}
		if (options.body && !headers.has('Content-Type')) {
			headers.set('Content-Type', 'application/json');
		}
		const res = await fetch(`${data.publicApiUrl}${path}`, {
			...options,
			headers
		});
		const json = await res.json().catch(() => ({}));
		if (!res.ok) {
			const msg = json?.error?.message || `HTTP ${res.status} hatası`;
			throw new Error(msg);
		}
		return json;
	}

	async function loginWithCredentials(email: string, password: string, preferredTab?: typeof activeTab) {
		isLoading = true;
		globalError = null;
		actionNotice = null;
		try {
			const res = await apiFetch('/api/v1/auth/login', {
				method: 'POST',
				body: JSON.stringify({ email, password })
			});
			accessToken = res.access_token;
			currentUser = res.user;
			loginEmail = email;
			if (preferredTab) {
				activeTab = preferredTab;
			} else {
				const found = demoRoles.find((d) => d.role === res.user.role);
				activeTab = found ? found.defaultTab : 'menu';
			}
			await refreshRoleData();
			actionNotice = `${res.user.full_name} (${res.user.role}) olarak oturum açıldı.`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Giriş başarısız.';
		} finally {
			isLoading = false;
		}
	}

	async function refreshRoleData() {
		isLoading = true;
		globalError = null;
		try {
			const [brRes, mnRes] = await Promise.all([
				apiFetch('/api/v1/branches'),
				apiFetch('/api/v1/menu')
			]);
			branches = brRes.branches || [];
			menuItems = mnRes.items || [];

			if (!currentUser) return;
			const role = currentUser.role;

			if (['waiter', 'cashier', 'chef', 'branch_manager', 'hq_manager'].includes(role)) {
				const tbRes = await apiFetch(`/api/v1/tables?branch_id=${selectedBranchId}`);
				tables = tbRes.tables || [];
				const occupied = tables.find((t) => t.status === 'OCCUPIED' && t.active_order_id > 0);
				if (occupied && (!selectedOrder || selectedOrder.branch_id !== selectedBranchId)) {
					await loadOrder(occupied.active_order_id);
				}
			}

			if (['chef', 'waiter', 'branch_manager', 'hq_manager'].includes(role)) {
				const kdsRes = await apiFetch(`/api/v1/kds/items?branch_id=${selectedBranchId}&station=${kdsStation}`);
				kdsItems = kdsRes.items || [];
				kdsLastEventId = kdsRes.last_event_id || 0;
			}

			if (['cashier', 'branch_manager', 'hq_manager'].includes(role)) {
				zReport = await apiFetch(`/api/v1/reports/z-report?branch_id=${selectedBranchId}`);
			}

			if (['chef', 'branch_manager', 'hq_manager'].includes(role)) {
				const invRes = await apiFetch(`/api/v1/inventory?branch_id=${selectedBranchId}`);
				ingredients = invRes.ingredients || [];
				stockMovements = invRes.movements || [];
				if (role !== 'chef') {
					varianceReport = await apiFetch(`/api/v1/inventory/variance-report?branch_id=${selectedBranchId}`);
				}
			}

			if (['branch_manager', 'hq_manager'].includes(role)) {
				const [hqRes, audRes] = await Promise.all([
					apiFetch('/api/v1/reports/hq'),
					apiFetch('/api/v1/audit-logs?limit=15')
				]);
				hqReport = hqRes;
				auditLogs = audRes.audit_logs || [];
			}
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Veri yüklenirken hata oluştu.';
		} finally {
			isLoading = false;
		}
	}

	async function loadOrder(orderId: number) {
		if (!orderId) return;
		try {
			selectedOrder = await apiFetch(`/api/v1/orders/${orderId}`);
			rebuildProof = null;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Adisyon yüklenemedi.';
		}
	}

	async function openOrSelectTable(t: TableItem) {
		globalError = null;
		try {
			if (t.status === 'OCCUPIED' && t.active_order_id > 0) {
				await loadOrder(t.active_order_id);
				return;
			}
			const created = await apiFetch('/api/v1/orders', {
				method: 'POST',
				headers: { 'Idempotency-Key': `ui-open-${t.id}-${Date.now()}` },
				body: JSON.stringify({ branch_id: selectedBranchId, table_id: t.id })
			});
			selectedOrder = created;
			await refreshRoleData();
			actionNotice = `${t.code} masası açıldı (Adisyon #${created.id}).`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Masa açılamadı.';
		}
	}

	async function addItemToOrder() {
		if (!selectedOrder) return;
		globalError = null;
		try {
			const updated = await apiFetch(`/api/v1/orders/${selectedOrder.id}/items`, {
				method: 'POST',
				body: JSON.stringify({
					menu_item_id: Number(addMenuItemId),
					quantity: Number(addQuantity),
					variant_id: Number(addVariantId),
					modifier_ids: addModifierIds
				})
			});
			selectedOrder = updated;
			await refreshRoleData();
			actionNotice = `Adisyon #${selectedOrder.id} içerisine kalem eklendi (Versiyon v${updated.version}).`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Kalem eklenemedi.';
		}
	}

	async function cancelOrderItem(itemId: number, produceWaste: boolean) {
		if (!selectedOrder) return;
		try {
			const updated = await apiFetch(`/api/v1/orders/${selectedOrder.id}/items/${itemId}/cancel`, {
				method: 'POST',
				body: JSON.stringify({
					reason: produceWaste ? 'Üretilmiş kalem iptali (Fire)' : 'Müşteri siparişi değiştirdi',
					produce_waste: produceWaste
				})
			});
			selectedOrder = updated;
			await refreshRoleData();
			actionNotice = `Kalem #${itemId} iptal edildi (${produceWaste ? 'FİRE KAYDEDİLDİ' : 'Stok korundu'}).`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'İptal işlemi başarısız.';
		}
	}

	async function verifyEventSourcingRebuild() {
		if (!selectedOrder) return;
		try {
			rebuildProof = await apiFetch(`/api/v1/orders/${selectedOrder.id}/rebuild`);
			actionNotice = `Adisyon #${selectedOrder.id} olay günlüğünden yeniden hesaplandı: is_consistent = ${rebuildProof?.is_consistent}`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Olay günlüğü doğrulaması başarısız.';
		}
	}

	async function moveSelectedOrderTable() {
		if (!selectedOrder) return;
		try {
			const updated = await apiFetch(`/api/v1/orders/${selectedOrder.id}/move`, {
				method: 'POST',
				body: JSON.stringify({ target_table_id: Number(moveTargetTableId) })
			});
			selectedOrder = updated;
			await refreshRoleData();
			actionNotice = `Adisyon #${updated.id} yeni masaya taşındı.`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Masa taşıma başarısız.';
		}
	}

	async function mergeOrdersAction() {
		if (!selectedOrder || !mergeSourceOrderId) return;
		try {
			const updated = await apiFetch('/api/v1/orders/merge', {
				method: 'POST',
				body: JSON.stringify({
					source_order_id: Number(mergeSourceOrderId),
					target_order_id: selectedOrder.id
				})
			});
			selectedOrder = updated;
			await refreshRoleData();
			actionNotice = `Kaynak adisyon #${mergeSourceOrderId}, hedef adisyon #${selectedOrder.id} ile birleştirildi.`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Masa birleştirme başarısız.';
		}
	}

	async function updateKDSStatus(itemId: number, status: string) {
		try {
			await apiFetch(`/api/v1/kds/items/${itemId}/status`, {
				method: 'PATCH',
				body: JSON.stringify({ status })
			});
			await refreshRoleData();
			actionNotice = `Mutfak kalemi #${itemId} durumu ${status} olarak güncellendi.`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'KDS güncellemesi başarısız.';
		}
	}

	async function simulateKDSReconnectReplay() {
		try {
			const since = Math.max(0, kdsLastEventId - 5);
			const res = await apiFetch(`/api/v1/kds/events?branch_id=${selectedBranchId}&since_event_id=${since}`);
			kdsReplayedEvents = res.events || [];
			actionNotice = `SSE kopma telafisi: since_event_id=${since} sonrasında kaçırılan ${res.replayed_count} olay geri alındı.`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Olay telafisi başarısız.';
		}
	}

	async function applyBestCampaignsToOrder() {
		if (!selectedOrder) return;
		try {
			const res = await apiFetch(`/api/v1/orders/${selectedOrder.id}/apply-campaigns`, {
				method: 'POST',
				body: JSON.stringify({ coupon_code: couponInput })
			});
			selectedOrder = res.order;
			await refreshRoleData();
			actionNotice = `Kampanya motoru çalıştırıldı: ${formatTL(res.evaluation.total_discount_kurus)} indirim (${res.evaluation.winning_strategy}).`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Kampanya hesaplanamadı.';
		}
	}

	async function previewEqualSplit() {
		if (!selectedOrder) return;
		try {
			const res = await apiFetch(`/api/v1/orders/${selectedOrder.id}/split-preview`, {
				method: 'POST',
				body: JSON.stringify({ split_mode: 'EQUAL', parts: Number(equalSplitParts) })
			});
			equalSharesPreview = res.shares_kurus || [];
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Bölüşme hesaplanamadı.';
		}
	}

	async function submitPayment() {
		if (!selectedOrder) return;
		try {
			const payload: Record<string, unknown> = {
				method: paymentMethod,
				split_mode: splitMode,
				tip_kurus: Number(tipKurus)
			};
			if (paymentMethod === 'MIXED' || splitMode === 'MIXED') {
				payload.method = 'MIXED';
				payload.split_mode = 'MIXED';
				payload.cash_amount_kurus = Number(mixedCashKurus);
				payload.card_amount_kurus = Number(mixedCardKurus);
			} else if (splitMode === 'EQUAL' && equalSharesPreview.length > 0) {
				payload.amount_kurus = equalSharesPreview[0];
			} else if (splitMode === 'BY_ITEM') {
				payload.selected_item_ids = selectedItemIdsForSplit;
			} else if (splitMode === 'CUSTOM') {
				payload.amount_kurus = Number(customAmountKurus);
			} else {
				payload.amount_kurus = selectedOrder.remaining_kurus;
			}

			const updated = await apiFetch(`/api/v1/orders/${selectedOrder.id}/payments`, {
				method: 'POST',
				headers: { 'Idempotency-Key': `ui-pay-${selectedOrder.id}-${Date.now()}` },
				body: JSON.stringify(payload)
			});
			selectedOrder = updated;
			await refreshRoleData();
			actionNotice = `Ödeme başarıyla işlendi. Kalan tutar: ${formatTL(updated.remaining_kurus)}`;
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Ödeme başarısız.';
		}
	}

	async function submitEndOfDayCount() {
		try {
			const res = await apiFetch('/api/v1/inventory/count', {
				method: 'POST',
				body: JSON.stringify({
					branch_id: selectedBranchId,
					counts: [
						{ ingredient_code: 'ING-BEEF', counted_grams: 48200 },
						{ ingredient_code: 'ING-TOMATO', counted_grams: 48900 }
					]
				})
			});
			varianceReport = res;
			actionNotice = 'Gün sonu fiziksel stok sayımı kaydedildi ve teorik/sayım fark raporu güncellendi.';
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Stok sayımı gönderilemedi.';
		}
	}

	async function resetDemoDataAction() {
		isLoading = true;
		try {
			await apiFetch('/api/v1/admin/reset-demo', { method: 'POST' });
			selectedOrder = null;
			await refreshRoleData();
			actionNotice = 'Veritabanı ilk deterministik seed durumuna döndürüldü.';
		} catch (err) {
			globalError = err instanceof Error ? err.message : 'Sıfırlama başarısız.';
		} finally {
			isLoading = false;
		}
	}
</script>

<main class="container">
	<!-- Top Header & Role Switcher -->
	<header class="topbar">
		<div>
			<span class="badge">SofraPOS • 8 Şubeli Restoran Sipariş, Mutfak (KDS), Stok ve Kasa Sistemi</span>
			<h1>SofraPOS Yönetim ve Operasyon Ekranı</h1>
		</div>
		<div class="branch-picker">
			<label for="branch-select">Aktif Şube:</label>
			<select
				id="branch-select"
				bind:value={selectedBranchId}
				onchange={() => refreshRoleData()}
			>
				{#if branches.length === 0}
					<option value={1}>BR-01 • Kadıköy Merkez Şube</option>
				{:else}
					{#each branches as b}
						<option value={b.id}>{b.code} • {b.name} ({b.city})</option>
					{/each}
				{/if}
			</select>
			<button class="btn-outline" onclick={() => refreshRoleData()} disabled={isLoading}>
				{isLoading ? 'Yükleniyor...' : 'Yenile'}
			</button>
		</div>
	</header>

	<!-- Quick Role Login Bar -->
	<section class="card auth-bar">
		<div class="auth-header">
			<div>
				<strong>Rol Bazlı Hızlı Demo Girişi (RBAC):</strong>
				{#if currentUser}
					<span class="active-user">
						Aktif Oturum: <strong>{currentUser.full_name}</strong> (<code>{currentUser.role}</code> — {currentUser.email})
					</span>
				{:else}
					<span class="muted">İşlem yapmak için bir rol seçin veya giriş yapın (Parola: <code>Sofra2026!</code>)</span>
				{/if}
			</div>
		</div>
		<div class="role-buttons">
			{#each demoRoles as d}
				<button
					class="btn-role {currentUser?.role === d.role ? 'active' : ''}"
					onclick={() => loginWithCredentials(d.email, 'Sofra2026!', d.defaultTab)}
				>
					{d.label}
				</button>
			{/each}
		</div>
	</section>

	{#if globalError}
		<div class="alert-error" role="alert">
			<strong>Hata:</strong> {globalError}
		</div>
	{/if}

	{#if actionNotice}
		<div class="alert-ok" role="status">
			{actionNotice}
		</div>
	{/if}

	<!-- Navigation Tabs (Filtered by RBAC Role) -->
	<nav class="tabs">
		{#if allowedTabsForRole(currentUser?.role).includes('tables')}
			<button class:selected={activeTab === 'tables'} onclick={() => (activeTab = 'tables')}>
				Masa & Adisyon (Garson)
			</button>
		{/if}
		{#if allowedTabsForRole(currentUser?.role).includes('kds')}
			<button class:selected={activeTab === 'kds'} onclick={() => (activeTab = 'kds')}>
				Mutfak Ekranı (KDS)
			</button>
		{/if}
		{#if allowedTabsForRole(currentUser?.role).includes('cashier')}
			<button class:selected={activeTab === 'cashier'} onclick={() => (activeTab = 'cashier')}>
				Kasa, Bölüşme & Kampanya
			</button>
		{/if}
		{#if allowedTabsForRole(currentUser?.role).includes('inventory')}
			<button class:selected={activeTab === 'inventory'} onclick={() => (activeTab = 'inventory')}>
				Reçete Stok & Fire (Müdür)
			</button>
		{/if}
		{#if allowedTabsForRole(currentUser?.role).includes('hq')}
			<button class:selected={activeTab === 'hq'} onclick={() => (activeTab = 'hq')}>
				8 Şube Merkez & Denetim
			</button>
		{/if}
		<button class:selected={activeTab === 'menu'} onclick={() => (activeTab = 'menu')}>
			QR Menü & Varyantlar
		</button>
		<button class:selected={activeTab === 'infra'} onclick={() => (activeTab = 'infra')}>
			Sistem Sağlığı & DB
		</button>
	</nav>

	<!-- TAB 1: TABLES & ORDERS (F-2, T-1) -->
	{#if activeTab === 'tables'}
		<section class="split-layout">
			<div class="card">
				<h2>Şube #{selectedBranchId} Masa Planı</h2>
				{#if isLoading && tables.length === 0}
					<p class="muted">Masalar yükleniyor...</p>
				{:else if tables.length === 0}
					<p class="muted">Masa görüntülemek için lütfen yukarıdan Garson veya Müdür rolüyle giriş yapın.</p>
				{:else}
					<div class="table-grid">
						{#each tables as t}
							<button
								class="table-tile {t.status === 'OCCUPIED' ? 'occupied' : 'empty'} {selectedOrder?.table_id === t.id ? 'focused' : ''}"
								onclick={() => openOrSelectTable(t)}
							>
								<div class="tile-top">
									<strong>{t.code}</strong>
									<span class="status-tag">{t.status === 'OCCUPIED' ? 'DOLU' : 'BOŞ'}</span>
								</div>
								<div class="tile-meta">{t.capacity} Kişilik</div>
								{#if t.status === 'OCCUPIED'}
									<div class="tile-price">{formatTL(t.order_total_kurus)}</div>
									<small>Adisyon #{t.active_order_id} (v{t.order_version})</small>
								{:else}
									<small>Adisyon Aç</small>
								{/if}
							</button>
						{/each}
					</div>
				{/if}
			</div>

			<div class="card">
				{#if selectedOrder}
					<div class="card-header">
						<div>
							<h2>Adisyon #{selectedOrder.id} ({selectedOrder.table_code})</h2>
							<small class="muted">Optimistic Lock Versiyon: <code>v{selectedOrder.version}</code> • Durum: <code>{selectedOrder.status}</code></small>
						</div>
						<button class="btn-outline" onclick={verifyEventSourcingRebuild}>
							Olay Günlüğünden Doğrula (T-1)
						</button>
					</div>

					{#if rebuildProof}
						<div class="proof-box">
							<strong>Olay Günlüğü (Event Sourcing) Tutarlılık Kanıtı:</strong>
							<span>Durum: <code>{rebuildProof.is_consistent ? 'TUTARLI (PASS)' : 'UYUŞMAZLIK'}</code></span>
						</div>
					{/if}

					<!-- Add Item Form -->
					<div class="form-row">
						<select bind:value={addMenuItemId}>
							{#each menuItems as m}
								<option value={m.id}>{m.name} ({formatTL(m.base_price_kurus)})</option>
							{/each}
						</select>
						<input type="number" min="1" max="20" bind:value={addQuantity} style="width: 75px;" />
						<button class="btn-primary" onclick={addItemToOrder}>Kalem Ekle</button>
					</div>

					<!-- Order Items List -->
					<div class="item-list">
						{#if selectedOrder.items.length === 0}
							<p class="muted">Bu adisyonda henüz sipariş kalemi bulunmuyor.</p>
						{:else}
							{#each selectedOrder.items as item}
								<div class="order-item-row {item.is_cancelled ? 'cancelled' : ''}">
									<div>
										<strong>{item.item_name}</strong> ({item.variant_name}) x{item.quantity}
										{#if item.modifier_names}
											<div class="mod-text">+ {item.modifier_names}</div>
										{/if}
										<small class="muted">
											İstasyon: <code>{item.station}</code> • Kurs: {item.course} • KDS: <code>{item.kds_status}</code>
											{#if item.allocated_discount_kurus > 0}
												• İndirim Payı: -{formatTL(item.allocated_discount_kurus)}
											{/if}
										</small>
									</div>
									<div class="item-actions">
										<span>{formatTL(item.net_total_kurus)}</span>
										{#if !item.is_cancelled}
											<button class="btn-danger-sm" onclick={() => cancelOrderItem(item.id, true)}>
												İptal (Fire)
											</button>
										{:else}
											<span class="badge-err">İPTAL</span>
										{/if}
									</div>
								</div>
							{/each}
						{/if}
					</div>

					<!-- Totals & Table Move/Merge -->
					<div class="totals-bar">
						<div>Ara Toplam: <strong>{formatTL(selectedOrder.subtotal_kurus)}</strong></div>
						<div>İndirim: <strong>-{formatTL(selectedOrder.discount_kurus)}</strong></div>
						<div>Net Toplam: <strong>{formatTL(selectedOrder.total_kurus)}</strong></div>
						<div>Kalan: <strong class="highlight">{formatTL(selectedOrder.remaining_kurus)}</strong></div>
					</div>

					<div class="sub-actions">
						<div class="inline-group">
							<label for="move-table-input">Masaya Taşı (ID):</label>
							<input id="move-table-input" type="number" min="1" bind:value={moveTargetTableId} style="width:70px" />
							<button class="btn-outline" onclick={moveSelectedOrderTable}>Masayı Taşı</button>
						</div>
						<div class="inline-group">
							<label for="merge-order-input">Birleştirilecek Adisyon ID:</label>
							<input id="merge-order-input" type="number" min="1" bind:value={mergeSourceOrderId} style="width:80px" />
							<button class="btn-outline" onclick={mergeOrdersAction}>Adisyonu Birleştir</button>
						</div>
					</div>
				{:else}
					<p class="muted">Detay görmek veya sipariş eklemek için soldan bir masaya tıklayın.</p>
				{/if}
			</div>
		</section>
	{/if}

	<!-- TAB 2: KDS KITCHEN DISPLAY (F-3, T-3) -->
	{#if activeTab === 'kds'}
		<section class="card">
			<div class="card-header">
				<div>
					<h2>Gerçek Zamanlı Mutfak Ekranı (KDS) — Son Olay ID: #{kdsLastEventId}</h2>
					<small class="muted">İstasyon yönlendirme (Soğuk/Sıcak/Bar), kurs sırası (1→2→3), gecikme uyarısı ve SSE olay telafisi.</small>
				</div>
				<div class="inline-group">
					{#each ['all', 'cold', 'hot', 'bar'] as st}
						<button
							class="btn-outline {kdsStation === st ? 'active-filter' : ''}"
							onclick={() => {
								kdsStation = st as 'all' | 'cold' | 'hot' | 'bar';
								refreshRoleData();
							}}
						>
							{st.toUpperCase()}
						</button>
					{/each}
					<button class="btn-primary" onclick={simulateKDSReconnectReplay}>
						SSE Kopma Telafisi (Replay Test)
					</button>
				</div>
			</div>

			{#if kdsReplayedEvents.length > 0}
				<div class="proof-box">
					<strong>SSE Yeniden Bağlanma Telafisi (Olay Kimliği Üzerinden Geri Alınan Olaylar):</strong>
					<span>Toplam {kdsReplayedEvents.length} olay başarıyla telafi edildi.</span>
				</div>
			{/if}

			{#if kdsItems.length === 0}
				<p class="muted">Mutfak kuyruğunda bekleyen aktif sipariş kalemi yok.</p>
			{:else}
				<div class="kds-grid">
					{#each kdsItems as ticket}
						<article class="kds-card {ticket.is_delayed ? 'delayed' : ''}">
							<div class="kds-top">
								<strong>{ticket.table_code} • #{ticket.order_id}</strong>
								<span class="station-pill">{ticket.station.toUpperCase()} • Kurs {ticket.course}</span>
							</div>
							<h3>{ticket.item_name} ({ticket.variant_name}) x{ticket.quantity}</h3>
							{#if ticket.modifier_names}
								<p class="mod-text">+ {ticket.modifier_names}</p>
							{/if}
							<div class="kds-meta">
								<span>Süre: {ticket.elapsed_sec}sn / Tahmini: {ticket.estimated_total_sec}sn</span>
								{#if ticket.is_delayed}
									<span class="badge-err">GECİKTİ!</span>
								{/if}
							</div>
							<div class="kds-buttons">
								<button class="btn-outline" onclick={() => updateKDSStatus(ticket.order_item_id, 'PREPARING')}>
									Hazırlanıyor
								</button>
								<button class="btn-primary" onclick={() => updateKDSStatus(ticket.order_item_id, 'READY')}>
									Hazır
								</button>
								<button class="btn-outline" onclick={() => updateKDSStatus(ticket.order_item_id, 'SERVED')}>
									Servis Edildi
								</button>
							</div>
						</article>
					{/each}
				</div>
			{/if}
		</section>
	{/if}

	<!-- TAB 3: CASHIER, CAMPAIGNS & SPLIT BILL (F-4, F-6, T-2, T-5) -->
	{#if activeTab === 'cashier'}
		<section class="split-layout">
			<div class="card">
				<h2>Kampanya Motoru (T-5) & Hesap Bölüşme (T-2)</h2>
				{#if !selectedOrder}
					<p class="muted">Lütfen önce "Masa & Adisyon" sekmesinden açık bir adisyon seçin.</p>
				{:else}
					<p>
						Seçili Adisyon: <strong>#{selectedOrder.id} ({selectedOrder.table_code})</strong> •
						Kalan Tutar: <strong class="highlight">{formatTL(selectedOrder.remaining_kurus)}</strong>
					</p>

					<div class="section-block">
						<h3>1. Kampanya ve Kupon Değerlendirme (En Avantajlı Kombinasyon)</h3>
						<div class="form-row">
							<input type="text" placeholder="Kupon Kodu (SOFRA50 / VIP20)" bind:value={couponInput} />
							<button class="btn-primary" onclick={applyBestCampaignsToOrder}>
								En Avantajlı Kampanyayı Uygula
							</button>
						</div>

						{#if selectedOrder.campaign_explanation && selectedOrder.campaign_explanation.length > 0}
							<div class="campaign-list">
								{#each selectedOrder.campaign_explanation as dec}
									<div class="campaign-item {dec.applied ? 'applied' : 'skipped'}">
										<div>
											<strong>{dec.code} — {dec.name}</strong>
											<span class="status-pill {dec.applied ? 'ok' : 'err'}">
												{dec.applied ? 'UYGULANDI (-' + formatTL(dec.discount_kurus) + ')' : 'ELENDİ'}
											</span>
										</div>
										<small>{dec.reason}</small>
									</div>
								{/each}
							</div>
						{/if}
					</div>

					<div class="section-block">
						<h3>2. Hesap Bölüşme ve Ödeme (Nakit / Kart / Karışık + Bahşiş)</h3>
						<div class="form-row">
							<select bind:value={splitMode}>
								<option value="FULL">Tam Ödeme (FULL)</option>
								<option value="EQUAL">Eşit Bölüşme (EQUAL — Kuruş Kaymasız)</option>
								<option value="CUSTOM">Özel Tutar / Kısmi Ödeme (CUSTOM)</option>
								<option value="MIXED">Karışık Ödeme (Nakit + Kart)</option>
							</select>
							<select bind:value={paymentMethod}>
								<option value="CARD">Kredi/Banka Kartı (CARD)</option>
								<option value="CASH">Nakit (CASH)</option>
								<option value="MIXED">Nakit + Kart (MIXED)</option>
							</select>
						</div>

						{#if splitMode === 'EQUAL'}
							<div class="form-row">
								<label for="equal-parts-input">Kişi Sayısı:</label>
								<input id="equal-parts-input" type="number" min="2" max="12" bind:value={equalSplitParts} style="width:70px" />
								<button class="btn-outline" onclick={previewEqualSplit}>Kuruş Paylarını Hesapla</button>
							</div>
							{#if equalSharesPreview.length > 0}
								<p class="muted">
									Hesaplanan Kişi Başı Paylar:
									{#each equalSharesPreview as sh, i}
										<code>Pay #{i + 1}: {formatTL(sh)}</code>{' '}
									{/each}
								</p>
							{/if}
						{:else if splitMode === 'CUSTOM'}
							<div class="form-row">
								<label for="custom-amt-input">Özel Tutar (Kuruş):</label>
								<input id="custom-amt-input" type="number" step="100" bind:value={customAmountKurus} />
							</div>
						{:else if splitMode === 'MIXED' || paymentMethod === 'MIXED'}
							<div class="form-row">
								<label for="mix-cash-input">Nakit (Kuruş):</label>
								<input id="mix-cash-input" type="number" bind:value={mixedCashKurus} />
								<label for="mix-card-input">Kart (Kuruş):</label>
								<input id="mix-card-input" type="number" bind:value={mixedCardKurus} />
							</div>
						{/if}

						<div class="form-row">
							<label for="tip-input">Bahşiş (Kuruş):</label>
							<input id="tip-input" type="number" min="0" step="500" bind:value={tipKurus} />
							<button class="btn-primary" onclick={submitPayment}>Ödemeyi Tamamla</button>
						</div>
					</div>
				{/if}
			</div>

			<!-- Z-Report Summary Card -->
			<div class="card">
				<h2>Şube #{selectedBranchId} Vardiya ve Z Raporu (F-7, T-2)</h2>
				{#if zReport}
					<dl class="kv-list">
						<div>
							<dt>Vardiya Durumu</dt>
							<dd><code>{zReport.shift_status}</code> (Vardiya #{zReport.shift_id})</dd>
						</div>
						<div>
							<dt>Nakit Ödemeler Toplamı</dt>
							<dd><strong>{formatTL(Number(zReport.cash_payments_kurus))}</strong></dd>
						</div>
						<div>
							<dt>Kart Ödemeler Toplamı</dt>
							<dd><strong>{formatTL(Number(zReport.card_payments_kurus))}</strong></dd>
						</div>
						<div>
							<dt>Tahsilat Toplamı (Nakit + Kart)</dt>
							<dd><strong>{formatTL(Number(zReport.total_payments_kurus))}</strong></dd>
						</div>
						<div>
							<dt>Toplam Bahşiş</dt>
							<dd>{formatTL(Number(zReport.total_tips_kurus))}</dd>
						</div>
						<div>
							<dt>Toplam İndirim</dt>
							<dd>{formatTL(Number(zReport.total_discount_kurus))}</dd>
						</div>
						<div>
							<dt>Toplam Fire Maliyeti</dt>
							<dd>{formatTL(Number(zReport.total_waste_kurus))}</dd>
						</div>
						<div>
							<dt>Kuruş Mutabakat Durumu</dt>
							<dd>
								<span class="status-pill {zReport.is_balanced ? 'ok' : 'err'}">
									{zReport.is_balanced ? 'TAM EŞİT (BALANCED)' : 'FARK VAR'}
								</span>
							</dd>
						</div>
					</dl>
				{:else}
					<p class="muted">Z raporu için Kasiyer veya Müdür rolüyle giriş yapın.</p>
				{/if}
			</div>
		</section>
	{/if}

	<!-- TAB 4: INVENTORY, SUB-RECIPES & VARIANCE REPORT (F-5, T-4) -->
	{#if activeTab === 'inventory'}
		<section class="split-layout">
			<div class="card">
				<div class="card-header">
					<h2>Hammadde ve Yarı Mamul (Alt Reçete) Stoku</h2>
					<button class="btn-primary" onclick={submitEndOfDayCount}>
						Gün Sonu Sayımı Gönder (T-4)
					</button>
				</div>
				{#if ingredients.length === 0}
					<p class="muted">Stok verisi için Aşçı veya Müdür rolüyle giriş yapın.</p>
				{:else}
					<div class="item-list">
						{#each ingredients as ing}
							<div class="order-item-row">
								<div>
									<strong>{ing.name}</strong> (<code>{ing.code}</code>)
									{#if ing.is_semi_finished}
										<span class="station-pill">YARI MAMUL (ALT REÇETE)</span>
									{/if}
								</div>
								<div>
									<strong>{ing.theoretical_qty_grams} {ing.unit}</strong>
								</div>
							</div>
						{/each}
					</div>
				{/if}
			</div>

			<div class="card">
				<h2>Gün Sonu Sayım Fark (Variance) Raporu & Fire</h2>
				{#if varianceReport && Array.isArray(varianceReport.rows)}
					<p class="muted">
						Toplam Eksik/Fire: <strong>{varianceReport.total_deficit_grams} g</strong> •
						Maliyet Farkı: <strong>{formatTL(Number(varianceReport.total_deficit_cost_kurus))}</strong>
					</p>
					<div class="item-list">
						{#each (varianceReport.rows as Array<Record<string, unknown>>) as r}
							<div class="order-item-row">
								<div>
									<strong>{r.ingredient_name}</strong>
									<small class="muted">Teorik: {r.theoretical_grams}g | Sayım: {r.counted_grams}g</small>
								</div>
								<div>
									<span class="status-pill {r.status === 'MATCH' ? 'ok' : 'err'}">
										Fark: {r.variance_grams}g ({r.status})
									</span>
								</div>
							</div>
						{/each}
					</div>
				{:else}
					<p class="muted">Sayım fark raporu için Şube Müdürü veya Merkez Yönetim rolüyle giriş yapın.</p>
				{/if}
			</div>
		</section>
	{/if}

	<!-- TAB 5: HQ 8-BRANCH REPORT & APPEND-ONLY AUDIT LOGS (F-7) -->
	{#if activeTab === 'hq'}
		<section class="split-layout">
			<div class="card">
				<div class="card-header">
					<h2>8 Şube Canlı Merkez Yönetim Raporu</h2>
					<button class="btn-danger-sm" onclick={resetDemoDataAction}>
						Demo Verisini Sıfırla (reset-demo)
					</button>
				</div>
				{#if hqReport && Array.isArray(hqReport.branches)}
					<div class="totals-bar">
						<div>Toplam Ciro: <strong>{formatTL(Number(hqReport.grand_revenue_kurus))}</strong></div>
						<div>Toplam Bahşiş: <strong>{formatTL(Number(hqReport.grand_tips_kurus))}</strong></div>
						<div>Toplam Fire: <strong>{formatTL(Number(hqReport.grand_waste_kurus))}</strong></div>
						<div>Dolu Masa: <strong>{hqReport.total_occupied_tables}</strong></div>
					</div>
					<div class="item-list">
						{#each (hqReport.branches as Array<Record<string, unknown>>) as b}
							<div class="order-item-row">
								<div>
									<strong>{b.code} — {b.name}</strong> ({b.city})
									<small class="muted">Dolu Masa: {b.occupied_tables} • Sipariş: {b.total_orders}</small>
								</div>
								<div>
									<strong>{formatTL(Number(b.revenue_kurus))}</strong>
								</div>
							</div>
						{/each}
					</div>
				{:else}
					<p class="muted">Merkez raporu için Merkez Yönetim veya Şube Müdürü rolüyle giriş yapın.</p>
				{/if}
			</div>

			<div class="card">
				<h2>Salt-Eklemeli Denetim Kaydı (<code>dz-audit-logs</code>)</h2>
				{#if auditLogs.length === 0}
					<p class="muted">Denetim kaydı görüntülemek için yönetici rolüyle giriş yapın.</p>
				{:else}
					<div class="item-list">
						{#each auditLogs as log}
							<div class="order-item-row">
								<div>
									<strong>#{log.id} • {log.action}</strong>
									<small class="muted">Rol: <code>{log.actor_role}</code> • Varlık: {log.entity_type} #{log.entity_id}</small>
								</div>
								<small class="muted">{String(log.created_at).slice(11, 19)}</small>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		</section>
	{/if}

	<!-- TAB 6: QR MENU (F-1) -->
	{#if activeTab === 'menu'}
		<section class="card">
			<h2>Dijital QR Menü — Varyantlar ve Ek Seçenekler (F-1)</h2>
			{#if menuItems.length === 0}
				<p class="muted">Menü görmek için yukarıdan "Yenile" veya "Müşteri (QR Menü)" butonuna tıklayın.</p>
			{:else}
				<div class="kds-grid">
					{#each menuItems as item}
						<article class="kds-card">
							<div class="kds-top">
								<strong>{item.name}</strong>
								<span class="station-pill">{formatTL(item.base_price_kurus)}</span>
							</div>
							<p class="muted">{item.description}</p>
							<div>
								<small><strong>Varyantlar:</strong></small>
								<div class="tag-row">
									{#each item.variants as v}
										<span class="tag">{v.name} ({v.price_delta_kurus >= 0 ? '+' : ''}{formatTL(v.price_delta_kurus)})</span>
									{/each}
								</div>
							</div>
							{#if item.modifiers.length > 0}
								<div>
									<small><strong>Ek Seçenekler (Modifier):</strong></small>
									<div class="tag-row">
										{#each item.modifiers as m}
											<span class="tag">{m.name} (+{formatTL(m.price_delta_kurus)})</span>
										{/each}
									</div>
								</div>
							{/if}
						</article>
					{/each}
				</div>
			{/if}
		</section>
	{/if}

	<!-- TAB 7: INFRASTRUCTURE HEALTH & POSTGRESQL 16 DIAGNOSTICS -->
	{#if activeTab === 'infra'}
		<section class="split-layout">
			<article class="card">
				<div class="card-header">
					<h2>Backend Servisi (Go + Fiber)</h2>
					{#if health?.status === 'healthy'}
						<span class="status-pill ok">SAĞLIKLI (200 OK)</span>
					{:else}
						<span class="status-pill err">KESİNTİ</span>
					{/if}
				</div>
				{#if health}
					<dl class="kv-list">
						<div><dt>Servis</dt><dd><code>{health.service}</code></dd></div>
						<div><dt>Uptime</dt><dd>{health.uptime_seconds} sn</dd></div>
						<div><dt>DB Ping</dt><dd>{health.database.latency_ms.toFixed(2)} ms</dd></div>
						<div><dt>Havuz (Aktif / Toplam / Maks)</dt><dd>{health.database.pool.acquired_conns} / {health.database.pool.total_conns} / {health.database.pool.max_conns}</dd></div>
					</dl>
				{/if}
			</article>

			<article class="card">
				<div class="card-header">
					<h2>PostgreSQL 16 (<code>dz-</code> Şeması)</h2>
					{#if dbDiag?.connected}
						<span class="status-pill ok">BAĞLI (pgx/v5)</span>
					{:else}
						<span class="status-pill err">BAĞLANTI YOK</span>
					{/if}
				</div>
				{#if dbDiag}
					<dl class="kv-list">
						<div><dt>Veritabanı / Kullanıcı</dt><dd><code>{dbDiag.current_database}</code> / <code>{dbDiag.current_user}</code></dd></div>
						<div><dt>Sorgu Gecikmesi</dt><dd>{dbDiag.latency_ms.toFixed(2)} ms</dd></div>
						<div><dt>Toplam Kontrol</dt><dd>{dbDiag.total_checks}</dd></div>
					</dl>
				{/if}
			</article>
		</section>
	{/if}
</main>

<style>
	.container {
		max-width: 1240px;
		margin: 0 auto;
		padding: 1.5rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 1.1rem;
	}
	.topbar {
		display: flex;
		justify-content: space-between;
		align-items: center;
		flex-wrap: wrap;
		gap: 1rem;
		background: #111827;
		border: 1px solid #1f2937;
		border-radius: 12px;
		padding: 1.25rem 1.5rem;
	}
	.badge {
		display: inline-block;
		font-size: 0.75rem;
		font-weight: 700;
		color: #38bdf8;
		background: rgba(56, 189, 248, 0.12);
		padding: 0.25rem 0.65rem;
		border-radius: 999px;
		margin-bottom: 0.35rem;
	}
	h1 {
		margin: 0;
		font-size: 1.45rem;
		color: #f8fafc;
	}
	h2 {
		margin: 0;
		font-size: 1.1rem;
		color: #f1f5f9;
	}
	h3 {
		margin: 0 0 0.5rem 0;
		font-size: 0.96rem;
		color: #e2e8f0;
	}
	.branch-picker {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		flex-wrap: wrap;
	}
	select,
	input {
		background: #0b0f19;
		color: #f8fafc;
		border: 1px solid #334155;
		border-radius: 8px;
		padding: 0.5rem 0.75rem;
		font-size: 0.88rem;
	}
	.card {
		background: #111827;
		border: 1px solid #1f2937;
		border-radius: 12px;
		padding: 1.25rem;
	}
	.auth-bar {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.active-user {
		margin-left: 0.5rem;
		color: #34d399;
		font-size: 0.9rem;
	}
	.role-buttons {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.btn-role {
		background: #1e293b;
		color: #e2e8f0;
		border: 1px solid #334155;
		border-radius: 8px;
		padding: 0.45rem 0.85rem;
		cursor: pointer;
		font-weight: 600;
		font-size: 0.84rem;
	}
	.btn-role.active {
		background: #10b981;
		color: #052e16;
		border-color: #10b981;
	}
	.tabs {
		display: flex;
		flex-wrap: wrap;
		gap: 0.45rem;
		border-bottom: 1px solid #1f2937;
		padding-bottom: 0.5rem;
	}
	.tabs button {
		background: #111827;
		color: #94a3b8;
		border: 1px solid #1f2937;
		border-radius: 8px;
		padding: 0.55rem 0.95rem;
		cursor: pointer;
		font-weight: 600;
		font-size: 0.86rem;
	}
	.tabs button.selected {
		background: #0284c7;
		color: #f8fafc;
		border-color: #38bdf8;
	}
	.split-layout {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
		gap: 1.1rem;
	}
	.table-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(135px, 1fr));
		gap: 0.75rem;
		margin-top: 1rem;
	}
	.table-tile {
		background: #0b0f19;
		border: 1px solid #1e293b;
		border-radius: 10px;
		padding: 0.85rem;
		text-align: left;
		color: #e2e8f0;
		cursor: pointer;
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
	}
	.table-tile.occupied {
		border-color: #f59e0b;
		background: rgba(245, 158, 11, 0.08);
	}
	.table-tile.focused {
		outline: 2px solid #38bdf8;
	}
	.tile-top {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.status-tag {
		font-size: 0.7rem;
		font-weight: 700;
		padding: 0.1rem 0.4rem;
		border-radius: 4px;
		background: rgba(255, 255, 255, 0.08);
	}
	.tile-price {
		font-weight: 700;
		color: #fbbf24;
		font-size: 0.95rem;
	}
	.card-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.75rem;
		flex-wrap: wrap;
		margin-bottom: 1rem;
	}
	.form-row {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		flex-wrap: wrap;
		margin: 0.75rem 0;
	}
	.item-list {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		margin-top: 0.75rem;
		max-height: 360px;
		overflow-y: auto;
	}
	.order-item-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.75rem;
		padding: 0.65rem 0.85rem;
		background: #0b0f19;
		border: 1px solid #1e293b;
		border-radius: 8px;
	}
	.order-item-row.cancelled {
		opacity: 0.55;
		text-decoration: line-through;
	}
	.item-actions {
		display: flex;
		align-items: center;
		gap: 0.6rem;
	}
	.totals-bar {
		display: flex;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.75rem;
		background: #0b0f19;
		border: 1px solid #334155;
		border-radius: 8px;
		padding: 0.75rem 1rem;
		margin-top: 1rem;
	}
	.highlight {
		color: #34d399;
		font-size: 1.05rem;
	}
	.sub-actions {
		display: flex;
		flex-wrap: wrap;
		gap: 0.75rem;
		margin-top: 0.85rem;
	}
	.inline-group {
		display: flex;
		align-items: center;
		gap: 0.45rem;
		flex-wrap: wrap;
	}
	.kds-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(270px, 1fr));
		gap: 0.9rem;
		margin-top: 0.75rem;
	}
	.kds-card {
		background: #0b0f19;
		border: 1px solid #1e293b;
		border-radius: 10px;
		padding: 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
	}
	.kds-card.delayed {
		border-color: #ef4444;
		background: rgba(239, 68, 68, 0.08);
	}
	.kds-top,
	.kds-meta,
	.kds-buttons {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 0.4rem;
		flex-wrap: wrap;
	}
	.station-pill {
		font-size: 0.72rem;
		font-weight: 700;
		background: rgba(56, 189, 248, 0.15);
		color: #38bdf8;
		padding: 0.15rem 0.5rem;
		border-radius: 999px;
	}
	.btn-primary {
		background: #10b981;
		color: #052e16;
		font-weight: 700;
		border: none;
		padding: 0.55rem 1rem;
		border-radius: 8px;
		cursor: pointer;
	}
	.btn-outline {
		background: transparent;
		color: #e2e8f0;
		border: 1px solid #334155;
		padding: 0.5rem 0.85rem;
		border-radius: 8px;
		cursor: pointer;
	}
	.btn-outline.active-filter {
		border-color: #38bdf8;
		color: #38bdf8;
	}
	.btn-danger-sm {
		background: rgba(239, 68, 68, 0.2);
		color: #fca5a5;
		border: 1px solid #ef4444;
		padding: 0.3rem 0.6rem;
		border-radius: 6px;
		cursor: pointer;
		font-size: 0.78rem;
	}
	.alert-error {
		background: rgba(239, 68, 68, 0.15);
		border: 1px solid #ef4444;
		color: #fecaca;
		padding: 0.8rem 1rem;
		border-radius: 8px;
	}
	.alert-ok {
		background: rgba(16, 185, 129, 0.15);
		border: 1px solid #10b981;
		color: #a7f3d0;
		padding: 0.8rem 1rem;
		border-radius: 8px;
	}
	.proof-box {
		background: rgba(56, 189, 248, 0.12);
		border: 1px solid #38bdf8;
		padding: 0.65rem 0.9rem;
		border-radius: 8px;
		margin-bottom: 0.75rem;
		display: flex;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.5rem;
	}
	.campaign-list {
		display: flex;
		flex-direction: column;
		gap: 0.45rem;
		margin-top: 0.5rem;
	}
	.campaign-item {
		padding: 0.6rem 0.8rem;
		border-radius: 8px;
		background: #0b0f19;
		border: 1px solid #1e293b;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	.campaign-item.applied {
		border-color: #10b981;
	}
	.status-pill {
		font-size: 0.72rem;
		font-weight: 700;
		padding: 0.15rem 0.5rem;
		border-radius: 999px;
	}
	.status-pill.ok {
		background: rgba(16, 185, 129, 0.18);
		color: #34d399;
	}
	.status-pill.err,
	.badge-err {
		background: rgba(239, 68, 68, 0.2);
		color: #f87171;
		padding: 0.15rem 0.45rem;
		border-radius: 4px;
		font-size: 0.72rem;
		font-weight: 700;
	}
	.kv-list {
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: 0.65rem;
	}
	.kv-list div {
		display: flex;
		justify-content: space-between;
		border-bottom: 1px solid #1f2937;
		padding-bottom: 0.45rem;
	}
	.tag-row {
		display: flex;
		flex-wrap: wrap;
		gap: 0.35rem;
		margin-top: 0.25rem;
	}
	.tag {
		font-size: 0.75rem;
		background: #1e293b;
		padding: 0.15rem 0.45rem;
		border-radius: 4px;
	}
	.mod-text {
		font-size: 0.8rem;
		color: #38bdf8;
		margin: 0.15rem 0;
	}
	.muted {
		color: #94a3b8;
	}
	.section-block {
		border-top: 1px solid #1f2937;
		padding-top: 0.85rem;
		margin-top: 0.85rem;
	}
	@media (max-width: 768px) {
		.split-layout {
			grid-template-columns: 1fr;
		}
		.topbar {
			flex-direction: column;
			align-items: flex-start;
		}
	}
</style>
