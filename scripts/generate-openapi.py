"""Generate both embedded OpenAPI formats from one contract. No dependencies."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
integer = {"type": "integer", "format": "int64", "minimum": 0}
positive = {**integer, "minimum": 1}
text = {"type": "string"}
boolean = {"type": "boolean"}
money = {**integer, "description": "Tam sayı kuruş; kayan nokta kabul edilmez."}

def enum(*values): return {"type": "string", "enum": list(values)}
def array(item): return {"type": "array", "items": item}
def obj(properties, required=()):
    result = {"type": "object", "properties": properties}
    if required: result["required"] = list(required)
    return result
def ref(name): return {"$ref": f"#/components/schemas/{name}"}
def parameter(name, schema=positive, location="query", required=False, description=""):
    return {"name": name, "in": location, "required": required, "schema": schema, "description": description}

schemas = {
    "Error": obj({"error": obj({"code": text, "message": text}, ("code", "message"))}, ("error",)),
    "User": obj({"id": positive, "branch_id": positive, "email": text, "full_name": text,
                 "role": enum("waiter", "cashier", "chef", "branch_manager", "hq_manager", "customer")}),
    "TokenPair": obj({"access_token": text, "refresh_token": text, "token_type": text, "expires_in": positive, "user": ref("User")}),
    "Login": obj({"email": {"type": "string", "format": "email"}, "password": {"type": "string", "format": "password"}}, ("email", "password")),
    "Refresh": obj({"refresh_token": text}, ("refresh_token",)),
    "OpenOrder": obj({"branch_id": positive, "table_id": positive}, ("table_id",)),
    "AddItem": obj({"menu_item_id": positive, "quantity": {"type": "integer", "minimum": 1, "maximum": 100},
                    "variant_id": integer, "modifier_ids": array(positive), "expected_version": integer}, ("menu_item_id", "quantity")),
    "CancelItem": obj({"reason": text, "produce_waste": boolean}, ("reason",)),
    "MoveOrder": obj({"target_table_id": positive}, ("target_table_id",)),
    "MergeOrders": obj({"source_order_id": positive, "target_order_id": positive}, ("source_order_id", "target_order_id")),
    "ApplyCampaigns": obj({"coupon_code": text}),
    "SplitPreview": obj({"split_mode": enum("EQUAL", "BY_ITEM", "CUSTOM"), "parts": {"type": "integer", "minimum": 2},
                         "selected_item_ids": array(positive), "custom_kurus": money}, ("split_mode",)),
    "Payment": obj({"method": enum("CASH", "CARD", "MIXED"), "split_mode": enum("FULL", "EQUAL", "BY_ITEM", "CUSTOM", "MIXED"),
                    "amount_kurus": money, "cash_amount_kurus": money, "card_amount_kurus": money,
                    "tip_kurus": money, "selected_item_ids": array(positive)}, ("method",)),
    "KDSStatus": obj({"status": enum("PREPARING", "READY", "SERVED")}, ("status",)),
    "StockCount": obj({"branch_id": positive, "counts": {"type": "array", "minItems": 1,
                        "items": obj({"ingredient_code": text, "counted_grams": integer}, ("ingredient_code", "counted_grams"))}}, ("counts",)),
    "OpenShift": obj({"branch_id": positive, "opening_cash_kurus": money}),
    "CloseShift": obj({"branch_id": positive, "closing_cash_kurus": money}),
    "CreateMenuItem": obj({"category_id": positive, "code": text, "name": text, "description": text,
                           "base_price_kurus": money, "station": enum("cold", "hot", "bar"),
                           "course": positive, "prep_time_sec": positive}, ("category_id", "code", "name", "base_price_kurus")),
    "OrderItem": obj({"id": positive, "menu_item_id": positive, "item_name": text, "variant_id": integer,
                      "variant_name": text, "modifier_names": text, "quantity": positive,
                      "unit_price_kurus": money, "line_subtotal_kurus": money, "allocated_discount_kurus": money,
                      "net_total_kurus": money, "paid_kurus": money, "remaining_kurus": money,
                      "station": enum("cold", "hot", "bar"), "course": positive, "kds_status": text,
                      "is_produced": boolean, "is_cancelled": boolean, "cancel_reason": text}),
    "Order": obj({"id": positive, "branch_id": positive, "table_id": positive, "table_code": text,
                  "status": enum("OPEN", "PARTIALLY_PAID", "PAID", "MERGED", "CANCELLED"), "version": positive,
                  **{name: money for name in ["subtotal_kurus", "discount_kurus", "total_kurus", "paid_kurus", "remaining_kurus", "tip_kurus"]},
                  "items": array(ref("OrderItem")), "payments": array({"type": "object"}),
                  "coupon_code": text, "campaign_explanation": array({"type": "object"})}),
    "KDSEvent": obj({"event_id": positive, "branch_id": positive, "order_id": positive, "event_type": text,
                     "station": text, "summary": text, "created_at": {"type": "string", "format": "date-time"}}),
    "ZReport": obj({"branch_id": positive, "shift_id": positive, "shift_status": enum("OPEN", "CLOSED"),
                    **{name: money for name in ["cash_payments_kurus", "card_payments_kurus", "total_payments_kurus", "total_tips_kurus", "opening_cash_kurus", "closing_cash_kurus", "expected_cash_kurus"]},
                    "cash_variance_kurus": {"type": "integer", "format": "int64"}, "is_balanced": boolean}),
}

doc = {"openapi": "3.0.3", "info": {"title": "SofraPOS API", "version": "1.1.0",
    "description": "Go/Fiber + PostgreSQL 16. Tutarlar tam sayı kuruştur. Tüm yanıtlar X-DDO-Case: cc6433 taşır. "
    "Merkez yönetim tüm şubelere, diğer personel yalnızca kendi şubesine erişir. Kritik mutasyonlar transaction, "
    "şube kilidi ve kullanıcı/metot/yol kapsamlı Idempotency-Key ile korunur. Aynı anahtar farklı gövdeyle 409 döner."},
    "servers": [{"url": "http://localhost:8080"}], "security": [{"bearerAuth": []}], "paths": {},
    "components": {"securitySchemes": {"bearerAuth": {"type": "http", "scheme": "bearer", "bearerFormat": "JWT"}}, "schemas": schemas}}

staff = ["waiter", "cashier", "chef", "branch_manager", "hq_manager"]
order_roles = ["waiter", "cashier", "branch_manager", "hq_manager"]
managers = ["branch_manager", "hq_manager"]
cashiers = ["cashier", "branch_manager", "hq_manager"]
cooks = ["chef", "branch_manager", "hq_manager"]
branch = parameter("branch_id", description="Varsayılan token şubesi; yalnızca hq_manager farklı şube seçebilir.")
cursor = parameter("since_event_id", integer)
page = [parameter("after_id", integer), parameter("limit", {"type": "integer", "minimum": 1, "maximum": 100})]
idempotency = parameter("Idempotency-Key", {"type": "string", "maxLength": 100}, "header", description="Ağ hatasında aynı anahtarı ve aynı gövdeyi tekrar gönderin; başarılı yanıt atomik olarak saklanır.")

def operation(path, method, summary, roles=staff, body=None, response=None, status=200, params=(), notes="", idem=False, content="application/json"):
    headers = {"X-DDO-Case": {"schema": {"type": "string", "example": "cc6433"}, "description": "Sabit proje başlığı"}}
    if idem: headers["X-Idempotent-Replay"] = {"schema": text, "description": "Tekrarlanan istekte true"}
    result = {"summary": summary, "description": notes or summary, "operationId": method + ''.join(p.title() for p in path.split('/') if p),
              "tags": [path.split('/')[3] if path.startswith('/api/v1/') else "system"],
              "responses": {str(status): {"description": "Başarılı", "headers": headers,
                "content": {content: {"schema": response or {"type": "object"}}}}}}
    result["security"] = [] if roles is None else [{"bearerAuth": []}]
    if roles: result["x-roles"] = roles
    parameters = list(params)
    for part in path.split('/'):
        if part.startswith('{'): parameters.append(parameter(part[1:-1], positive, "path", True))
    if idem: parameters.append(idempotency)
    if parameters: result["parameters"] = parameters
    if body: result["requestBody"] = {"required": True, "content": {"application/json": {"schema": ref(body)}}}
    for code, message in [(400,"Geçersiz girdi"),(401,"Kimlik doğrulama gerekli"),(403,"Rol/şube yetkisi yok"),(404,"Kaynak bulunamadı"),(409,"Versiyon, bakiye veya idempotans çakışması"),(500,"İşlem geri alındı"),(503,"Veritabanı erişilemiyor")]:
        result["responses"][str(code)] = {"description": message, "content": {"application/json": {"schema": ref("Error")}}}
    doc["paths"].setdefault(path, {})[method] = result

operation('/', 'get', 'Servis bilgisi ve uç noktalar', None)
for path in ["/health", "/_hz/cc64"]: operation(path, "get", "Uygulama ve veritabanı sağlığı", None)
doc['paths']['/health']['get']['responses']['503'] = {
    'description': 'Veritabanı erişilemiyor; status=degraded ve database tanılaması döner.',
    'content': {'application/json': {'schema': {'type': 'object'}}}}
for path, mime in [("/metrics","text/plain"),("/openapi.yaml","application/yaml"),("/openapi.json","application/json"),("/docs","text/html")]:
    operation(path, "get", "Metrikler veya API dokümanı", None, response=text, content=mime)
operation('/api/v1/db/check', 'get', 'Veritabanı tanılaması', ['hq_manager'])
operation('/api/v1/auth/demo-accounts', 'get', 'Yerel demo hesapları', None)
operation('/api/v1/auth/login', 'post', 'Bcrypt parola ile oturum aç', None, 'Login', ref('TokenPair'))
operation('/api/v1/auth/refresh', 'post', 'Aktif kullanıcı için token yenile', None, 'Refresh', ref('TokenPair'))
operation('/api/v1/auth/me', 'get', 'Aktif kullanıcı', staff + ['customer'], response=obj({'user': ref('User')}))
operation('/api/v1/branches', 'get', 'Sekiz şube', None)
operation('/api/v1/menu', 'get', 'Menü, varyantlar ve modifier seçenekleri', None)
operation('/api/v1/menu/items', 'post', 'Ortak menüye ürün ekle', managers, 'CreateMenuItem', status=201, idem=True)
operation('/api/v1/tables', 'get', 'Şube masa planı', params=[branch])
operation('/api/v1/orders', 'get', 'Keyset sayfalı adisyon listesi', params=[branch, *page])
operation('/api/v1/orders', 'post', 'Adisyon aç veya masanın açık adisyonunu getir', order_roles, 'OpenOrder', ref('Order'), status=201, idem=True,
          notes='Boş masa için 201, zaten açık masa için 200. Başka şubenin masası reddedilir.')
operation('/api/v1/orders/{id}', 'get', 'Adisyon ayrıntısı', response=ref('Order'))
operation('/api/v1/orders/{id}/rebuild', 'get', 'Olaylardan durum ve toplam tutarlılığı', order_roles)
operation('/api/v1/orders/{id}/items', 'post', 'Kalem ekle ve reçete stoğunu atomik düş', order_roles, 'AddItem', ref('Order'), status=201, idem=True,
          notes='expected_version eskiyse 409. Varyant ve modifier ürüne ait olmalı. Kapalı adisyona ekleme reddedilir. Ödeme başladıysa mevcut kalem indirimleri korunur.')
operation('/api/v1/orders/{id}/items/{itemId}/cancel', 'post', 'İptal, stok iadesi veya gerçek reçete firesi', order_roles, 'CancelItem', ref('Order'), idem=True,
          notes='Üretim öncesi iptal stok iadesidir. Üretim sonrası tüketim WASTE olarak sınıflanır; ikinci kez stok düşülmez. Ödeme başlamışsa 409.')
operation('/api/v1/orders/{id}/move', 'post', 'Boş masaya taşı', order_roles, 'MoveOrder', ref('Order'), idem=True)
operation('/api/v1/orders/merge', 'post', 'Aynı şubenin ödenmemiş adisyonlarını birleştir', order_roles, 'MergeOrders', ref('Order'), idem=True)
operation('/api/v1/campaigns', 'get', 'Aktif kurallar ve kuponlar', staff + ['customer'])
operation('/api/v1/orders/{id}/apply-campaigns', 'post', 'En avantajlı kampanyayı uygula', order_roles, 'ApplyCampaigns', idem=True,
          notes='Ödeme başladıysa 409. Sepet değişirse ödeme öncesi indirim temizlenir; yeniden uygulayın. Saat sunucunun Europe/Istanbul saatidir; current_hour yalnızca APP_ENV=test için kullanılabilir.')
operation('/api/v1/orders/{id}/split-preview', 'post', 'Eşit, kalem veya özel tutar önizlemesi', order_roles, 'SplitPreview')
operation('/api/v1/orders/{id}/payments', 'post', 'Ödeme ve kalem bakiyelerini atomik kapat', cashiers, 'Payment', ref('Order'), status=201, idem=True,
          notes='FULL tutarı 0 ise tüm kalan alınır. EQUAL/CUSTOM pozitif amount_kurus ister. BY_ITEM seçilen kalemlerin tam kalanını ister. MIXED iki pozitif alt tutar ister; bahşiş kart ayağına yazılır. Açık vardiya zorunludur.')
operation('/api/v1/kds/items', 'get', 'KDS istasyon, kurs ve gecikme anlık görüntüsü', ['waiter', *cooks], params=[branch, parameter('station', enum('all','cold','hot','bar'))])
operation('/api/v1/kds/items/{itemId}/status', 'patch', 'PENDING → PREPARING → READY → SERVED', cooks, 'KDSStatus', idem=True)
operation('/api/v1/kds/events', 'get', 'Kalıcı olay telafisi', ['waiter', *cooks], params=[branch, cursor, parameter('Last-Event-ID', integer, 'header')])
operation('/api/v1/kds/stream', 'get', 'SSE canlı olaylar ve eksiksiz replay', ['waiter', *cooks], response=text, content='text/event-stream',
          params=[branch, cursor, parameter('Last-Event-ID', integer, 'header'), parameter('once', enum('0','1')), parameter('token', text)],
          notes='Last-Event-ID sorgu imlecine üstün gelir. Kalıcı olaylar 200 kayıtlük partilerle sonuna kadar okunur. event: kds_event, id ve JSON data gönderilir. 25 saniyede bağlantı yenilenir; EventSource otomatik bağlanır. token yalnızca bu uçta EventSource için kabul edilir.')
operation('/api/v1/inventory', 'get', 'Hammaddeler, teorik stok ve son hareketler', cooks, params=[branch])
operation('/api/v1/inventory/count', 'post', 'Yalnızca sayılan hammaddeleri kaydet', managers, 'StockCount', status=201, idem=True)
operation('/api/v1/inventory/variance-report', 'get', 'En son fiziksel sayım farkları', managers, params=[branch])
operation('/api/v1/shifts/open', 'post', 'Şubede tek açık vardiya oluştur', cashiers, 'OpenShift', status=201, idem=True)
operation('/api/v1/shifts/close', 'post', 'Vardiya kapat; otomatik yenisini açma', cashiers, 'CloseShift', idem=True,
          notes='closing_cash_kurus gönderilmezse beklenen kasa kullanılır; 0 geçerli bir fiziksel sayımdır. Nakit bahşiş beklenen kasaya dahildir.')
operation('/api/v1/reports/z-report', 'get', 'Son veya belirtilen vardiya Z raporu', cashiers, response=ref('ZReport'), params=[branch, parameter('shift_id')])
operation('/api/v1/reports/hq', 'get', 'Sekiz şube için yaşam boyu toplamlar', ['hq_manager'])
operation('/api/v1/audit-logs', 'get', 'Keyset sayfalı append-only denetim kayıtları', managers, params=page)
operation('/api/v1/admin/reset-demo', 'post', 'Demo şemasını atomik sıfırla ve seed uygula', ['hq_manager'], notes='Yalnızca demo kullanımı. Mevcut işlem verilerini siler; seed kullanıcıları, menü, stok ve sekiz açık vardiya oluşturur.')

def yaml_lines(value, indent=0):
    pad = ' ' * indent
    if isinstance(value, dict) and value:
        for key, child in value.items():
            prefix = pad + json.dumps(key, ensure_ascii=False) + ':'
            if isinstance(child, (dict, list)) and child:
                yield prefix
                yield from yaml_lines(child, indent + 2)
            else: yield prefix + ' ' + json.dumps(child, ensure_ascii=False)
    elif isinstance(value, list) and value:
        for child in value:
            if isinstance(child, (dict, list)) and child:
                yield pad + '-'
                yield from yaml_lines(child, indent + 2)
            else: yield pad + '- ' + json.dumps(child, ensure_ascii=False)

def validate(value):
    if isinstance(value, dict):
        if '$ref' in value:
            target = doc
            for part in value['$ref'].removeprefix('#/').split('/'):
                target = target[part]
        for child in value.values(): validate(child)
    elif isinstance(value, list):
        for child in value: validate(child)

validate(doc)
operation_ids = [op['operationId'] for path in doc['paths'].values() for op in path.values()]
assert len(operation_ids) == len(set(operation_ids)), 'Duplicate operationId'
directory = ROOT / 'backend/internal/openapi'
(directory / 'spec.json').write_text(json.dumps(doc, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
(directory / 'spec.yaml').write_text('\n'.join(yaml_lines(doc)) + '\n', encoding='utf-8')
print(f"Generated {len(doc['paths'])} paths / {sum(len(v) for v in doc['paths'].values())} operations")
