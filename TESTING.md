# SofraPOS — Test, Doğrulama ve Kanıt Rehberi (`TESTING.md`)

Bu doküman; **SofraPOS** sisteminin tek komutla uçtan uca nasıl doğrulanacağını (`make verify`), her rol için hazır demo hesaplarını, doğrulama kimliklerinin (`F-1`..`F-7` ve `T-1`..`T-5`) beklenen sonuçlarını ve arayüz üzerinden elle denenecek senaryoları içerir.

---

## 1. Tek Komutla Hızlı Doğrulama ve Sıfırlama

Sistem ayağa kaldırıldıktan (`docker compose up -d --build`) sonra kök dizinde aşağıdaki komutları çalıştırabilirsiniz:

```bash
# Tüm kapsam maddelerini (F-1..F-7) ve zorlu görevleri (T-1..T-5) uçtan uca doğrular, PASS/FAIL tablosu basar:
make verify
# (Veya doğrudan: docker compose --profile verify run --rm verify)

# Veritabanını ilk deterministik seed haline döndürür:
make reset-demo

# 40+ birim ve eşzamanlılık (race) testini çalıştırır:
make test

# Hata-enjeksiyonu (Kaos) senaryosunu çalıştırır (PostgreSQL durdurulup toparlanma kanıtlanır):
make chaos

# k6 yük testini çalıştırır:
make load
```

---

## 2. Demo Kullanıcı Hesapları (`"dz-sk-users"`)

Sistem ilk ayağa kalktığında migrasyon ve seed otomatik çalışır. Tüm demo hesaplarının parolası **`Sofra2026!`** (bcrypt ile özetlenmiş) olarak tanımlanmıştır:

| Sıra | Rol Kodu | Rol Açıklaması | E-posta | Parola | Yetkili Ekranlar |
|---|---|---|---|---|---|
| 1 | `hq_manager` | **İlk Seed Kullanıcı** | `seed.ceylan@ddo-case.invalid` | `Sofra2026!` | Tüm Şubeler, Merkez Rapor, Denetim, Stok, Kasa |
| 2 | `waiter` | **Garson** | `garson@sofrapos.local` | `Sofra2026!` | Masa Planı, Adisyon Açma/Ekleme/Taşıma/Birleştirme, KDS İzleme |
| 3 | `cashier` | **Kasiyer** | `kasiyer@sofrapos.local` | `Sofra2026!` | Kasa, Hesap Bölüşme, Kampanya/Kupon, Ödeme, Z Raporu |
| 4 | `chef` | **Aşçı (Mutfak KDS)** | `asci@sofrapos.local` | `Sofra2026!` | Gerçek Zamanlı Mutfak Ekranı (Soğuk/Sıcak/Bar), Stok İzleme |
| 5 | `branch_manager` | **Şube Müdürü** | `mudur@sofrapos.local` | `Sofra2026!` | Şube Stok & Sayım Fark Raporu, Fire, Vardiya, Z Raporu, Menü Ekleme |
| 6 | `hq_manager` | **Merkez Yönetim** | `merkez@sofrapos.local` | `Sofra2026!` | 8 Şube Karşılaştırma Raporu, Salt-Eklemeli Denetim Kaydı (`dz-audit-logs`) |
| 7 | `customer` | **Müşteri (QR Menü)** | `musteri@sofrapos.local` | `Sofra2026!` | Dijital QR Menü, Varyantlar ve Ek Seçenekler |

---

## 3. Doğrulama Kimlikleri Tablosu (`F-1`..`F-7` ve `T-1`..`T-5`)

| Kimlik | Özellik / Görev | Nasıl Denenir? | Beklenen Sonuç |
|---|---|---|---|
| **`HEALTH`** | Sağlık ve Başlık Kontrolü | `GET /health` ve `GET /_hz/cc64` | İki uç da `200 OK` döner; tüm yanıtlarda `X-DDO-Case: cc6433` başlığı yer alır. |
| **`AUTH-RBAC`** | Kimlik Doğrulama & RBAC | Yanlış parola ile `POST /api/v1/auth/login`; Garson token'ı ile `GET /api/v1/reports/hq`; Müşteri token'ı ile `POST /api/v1/orders/1/payments` | Yanlış parola ve tokensiz istek `401 Unauthorized`; yetkisiz rol erişimleri `403 Forbidden` ile reddedilir. |
| **`F-1`** | Menü, Varyantlar ve Modifier'lar | `GET /api/v1/menu` ve `Idempotency-Key` ile `POST /api/v1/menu/items` | Kategoriler, ürün varyantları (`1.5 Porsiyon`, `Duble`) ve modifier'lar (`Ekstra Tereyağı`, `Ekstra Kaşar`) listelenir; aynı `Idempotency-Key` ile tekrarlanan POST isteği `X-Idempotent-Replay: true` döner. |
| **`F-2`** | Masa ve Adisyon Yönetimi | `GET /api/v1/tables`, `POST /api/v1/orders`, `POST /api/v1/orders/:id/items` | Masa `OCCUPIED` durumuna geçer, varyant ve modifier fiyat farkları kuruş cinsinden (`BIGINT`) adisyona eklenir. |
| **`F-3`** | Mutfak Ekranı (KDS) ve İstasyonlar | `GET /api/v1/kds/items?station=hot`, `PATCH /api/v1/kds/items/:id/status` | Sipariş kalemleri `cold`/`hot`/`bar` istasyonlarına yönlendirilir, `PREPARING` → `READY` → `SERVED` geçişleri yapılır ve `is_produced=true` işaretlenir. |
| **`F-4`** | Kasa: Nakit/Kart, Bölüşme, Bahşiş | `POST /api/v1/orders/:id/split-preview`, `POST /api/v1/orders/:id/payments` | Nakit, Kart ve Karışık (`MIXED`) ödemeler ile bahşiş (`tip_kurus`) kaydedilir; borç sıfırlandığında masa otomatik `EMPTY` durumuna döner. |
| **`F-5`** | Reçete Bazlı Stok ve Fire | `GET /api/v1/inventory`, `POST /api/v1/orders/:id/items/:itemId/cancel` | Sipariş eklendiğinde reçete hammaddeleri gram bazında düşülür; üretilmiş kalem iptal edildiğinde `WASTE` (fire) hareketi oluşur. |
| **`F-6`** | Kampanya ve Kupon | `GET /api/v1/campaigns`, `POST /api/v1/orders/:id/apply-campaigns` | Aktif kampanyalar (`CMP-3AL2ODE`, `CMP-COMBO-KEBAP`, `CMP-HAPPY-HOUR`) ve kuponlar (`SOFRA50`, `VIP20`) adisyona uygulanır. |
| **`F-7`** | Vardiya, Z Raporu, Merkezi Raporlar | `GET /api/v1/reports/z-report`, `GET /api/v1/reports/hq`, `GET /api/v1/audit-logs` | Şube Z raporu, 8 şube merkezi ciro/fire özeti ve keyset sayfalamalı salt-eklemeli denetim kayıtları döner. |
| **`T-1`** | **Adisyon Tutarlılığı** | İki eşzamanlı garsonun aynı masaya kalem eklemesi, eski `expected_version` ile istek, masa taşıma (`move`), birleştirme (`merge`), ödeme sırasında kalem ekleme ve `GET /api/v1/orders/:id/rebuild` | Eski versiyon `409 Conflict` ile reddedilir; tüm işlemler `"dz-order-events"` tablosuna yazılır ve olaylardan yeniden hesaplanan toplam ile adisyon toplamı birebir tutar (`is_consistent: true`). |
| **`T-2`** | **Hesap Bölüşme ve Ödeme** | 3 eşit kaleme bölünemeyen indirim (`5000` kr → `1667+1667+1666` kr) ve 3 kişilik eşit bölüşme (`EQUAL`) sonrası `GET /api/v1/reports/z-report` | Kalem indirimleri toplamı adisyon indirimine, bölüşme payları toplamı kalan tutara ve Z raporu toplamı (`cash + card == total_payments`) ödeme yöntemleri toplamına kuruşu kuruşuna eşittir (`is_balanced: true`). |
| **`T-3`** | **Gerçek Zamanlı Mutfak Ekranı** | `GET /api/v1/kds/stream?since_event_id=<id>&once=1` ve `GET /api/v1/kds/events?since_event_id=<id>` | Kurs sırası (`1→2→3`) ve geciken sipariş (`is_delayed: true`) hesaplanır; SSE bağlantısı koptuğunda istemci `Last-Event-ID` / `since_event_id` ile kaçırdığı olayları eksiksiz geri alır. |
| **`T-4`** | **Reçete Bazlı Stok ve Fire** | Varyant (`1.5 Porsiyon`) + Modifier (`Ekstra Tereyağı`) + Alt Reçete (`SEMI-ISKENDER-SAUCE` → `ING-TOMATO` + `ING-BUTTER`) çözümü ve `POST /api/v1/inventory/count` | Yarı mamul alt reçetesi hammaddelerine ayrıştırılarak teorik stoktan düşülür; gün sonu fiziksel sayımı ile teorik stok arasındaki fark hammadde bazlı raporlanır. |
| **`T-5`** | **Kampanya Motoru** | 3 Lahmacun + 1 İskender + 1 Ayran + 1 Künefe (`925.00 TL`) sepetine `SOFRA50` kuponuyla `POST /api/v1/orders/:id/apply-campaigns` | Birleştirilebilir paket (`3Al2Öde 110 TL` + `Kombo 85 TL` + `SOFRA50 50 TL` = **`245.00 TL`**), tekil `%10 Saatlik İndirim` (`92.50 TL`) seçeneğini yener ve `campaign_explanation` içinde her kuralın neden seçildiği/elendiği açıklanır. |

---

## 4. Zorlu Çekirdek Görevler (`T-1`..`T-5`) İçin Kanıt Komutları

Her zorlu görev hem birim/eşzamanlılık testi düzeyinde (`go test`) hem de canlı konteyner API'si üzerinde (`make verify`) bağımsız olarak kanıtlanabilir:

```bash
# Tüm görevleri canlı sistem üzerinde uçtan uca kanıtlayan tek komut:
docker compose --profile verify run --rm verify

# Görev T-1 Kanıtı (Eşzamanlı 20 garson optimistic locking yarış testi + Olay günlüğünden rebuild):
cd backend && go test -v -race -run "TestT1_" ./internal/domain/...

# Görev T-2 Kanıtı (Kuruş kaymasız indirim dağıtımı, asal sayı bölüşme ve Z raporu mutabakatı):
cd backend && go test -v -run "TestT2_" ./internal/domain/...

# Görev T-3 Kanıtı (İstasyon/kurs sıralaması, gecikme uyarısı ve SSE kopma sonrası event replay):
cd backend && go test -v -run "TestT3_" ./internal/domain/...

# Görev T-4 Kanıtı (Ürün -> Yarı mamul alt reçetesi -> Hammadde zinciri ve gün sonu sayım farkı):
cd backend && go test -v -run "TestT4_" ./internal/domain/...

# Görev T-5 Kanıtı (X Al Y Öde + Kombo + Saatlik + Kupon en avantajlı kombinasyon çözücü):
cd backend && go test -v -run "TestT5_" ./internal/domain/...
```

---

## 5. Arayüzde (`http://localhost:3000`) Elle Denenecek Kısa Senaryolar

1. **Garson Senaryosu (`F-2`, `T-1`):**
   - Üst bardan **"Garson"** butonuna tıklayın.
   - Masa planından `T-01` veya boş bir masaya tıklayarak adisyon açın, ürün seçip **"Kalem Ekle"** butonuna basın.
   - Kalem satırındaki **"İptal (Fire)"** butonuyla üretilmiş kalemi iptal edin, ardından **"Olay Günlüğünden Doğrula (T-1)"** butonuna tıklayarak `TUTARLI (PASS)` kanıt kutusunu görün.
2. **Aşçı / Mutfak KDS Senaryosu (`F-3`, `T-3`):**
   - Üst bardan **"Aşçı (Mutfak KDS)"** butonuna tıklayın.
   - İstasyon filtrelerini (`COLD`, `HOT`, `BAR`) değiştirin, bir fişi **"Hazırlanıyor"** veya **"Hazır"** durumuna alın.
   - **"SSE Kopma Telafisi (Replay Test)"** butonuna tıklayarak son olay kimliğinden (`since_event_id`) itibaren kaçırılan olayların geri alındığını doğrulayın.
3. **Kasiyer Senaryosu (`F-4`, `F-6`, `T-2`, `T-5`):**
   - Üst bardan **"Kasiyer (Kasa)"** butonuna tıklayın.
   - **"En Avantajlı Kampanyayı Uygula"** butonuna basarak hangi kampanyaların neden uygulandığını ve hangilerinin neden elendiğini yeşil/kırmızı açıklama kartlarında inceleyin.
   - Bölüşme modunu **"Eşit Bölüşme (EQUAL)"** seçip **"Kuruş Paylarını Hesapla"** butonuna basın (örn. `33.34 ₺`, `33.33 ₺`, `33.33 ₺`) ve **"Ödemeyi Tamamla"** ile sağdaki Z Raporu kartında `TAM EŞİT (BALANCED)` rozetini görün.
4. **Şube Müdürü & Merkez Yönetim Senaryosu (`F-5`, `F-7`, `T-4`):**
   - **"Şube Müdürü"** rolüne geçip **"Gün Sonu Sayımı Gönder (T-4)"** butonuna tıklayarak teorik stok ile fiziksel sayım arasındaki gram ve kuruş fark raporunu inceleyin.
   - **"Merkez Yönetim"** rolüne geçip **"8 Şube Merkez & Denetim"** sekmesinde 8 şubenin canlı cirolarını ve `dz-audit-logs` denetim kayıtlarını görüntüleyin.
