# Test ve doğrulama rehberi

## Başlangıç ve hesaplar

```bash
cp .env.example .env
docker compose up -d --build --wait
make verify
# Make olmayan sistemde:
docker compose --profile verify run --rm verify
```

Doğrulama demo verisini sıfırlar; simülatör aynı oturum kilidini bekler. Her satır PASS/FAIL, toplam sonuç ve hata durumunda sıfırdan farklı çıkış kodu üretilir. Son koşu: **24/24 PASS**, [kanıt](evidence/verify.txt). Sadece ilk seed'e dönmek için `make reset-demo`; bu komut simülatörü kapalı bırakır. `make demo-live` yeniden başlatır.

Tüm hesapların parolası **`Sofra2026!`**. Personel şube 1'e bağlıdır; yalnız merkez başka şube seçebilir.

| Rol | E-posta | Beklenen ekran |
|---|---|---|
| İlk seed / merkez | `seed.ceylan@ddo-case.invalid` | Tüm operasyonlar |
| Garson | `garson@sofrapos.local` | Masa/adisyon, KDS izleme |
| Kasiyer | `kasiyer@sofrapos.local` | Kasa, bölüşme, kampanya, vardiya/Z |
| Aşçı | `asci@sofrapos.local` | KDS ve stok |
| Şube müdürü | `mudur@sofrapos.local` | Şube operasyonları, stok/sayım, denetim |
| Merkez | `merkez@sofrapos.local` | Sekiz şube, denetim, tanılama |
| Müşteri | `musteri@sofrapos.local` | Yalnız QR menü |

## Kimlikler ve beklenen sonuçlar

Aşağıdaki satırların tamamı `make verify` içinde gerçek API üzerinde çalışır. Elle aynı uçlar [requests.http](requests.http) ile denenebilir. Kimlik doğrulama, kalem ID'leri ve açılan adisyon referansları koleksiyonda yer alır.

| Kimlik | Nasıl denenir | Beklenen sonuç |
|---|---|---|
| HEALTH | `/health`, `/_hz/cc64` | 200 ve `X-DDO-Case: cc6433` |
| AUTH-RBAC | Yedi hesapla giriş; yanlış parola/tokensiz/yetkisiz rol | Başarılı giriş; 401/403 |
| RESET | Merkez ile demo sıfırlama | İlk seed tekrar kurulur |
| FRONTEND | Frontend `/health` ve ana sayfa | 200, SSR menü içeriği |
| F-1 | Menü oku, ürün ekle, anahtarla tekrarla | Varyant/modifier listesi; tek kayıt ve replay |
| F-2 | Masa aç, varyant/modifier kalem ekle | OCCUPIED; doğru kuruş toplamı |
| F-3 | KDS listele, durum güncelle | İstasyon/kurs; geçerli durum ilerlemesi |
| F-4 | Nakit+kart+bahşiş ödemesi | Tahsilat/kalem bakiyesi; tam ödemede PAID |
| F-5 | Siparişten önce/sonra envanter | Reçeteye göre hammadde tüketimi |
| F-6 | Kampanya/kupon listesi | X al Y öde, kombo, saatlik ve kupon |
| F-7 | Vardiya/Z, merkez, audit | Sekiz şube, dengeli Z, keyset denetim |
| T-1 | Paralel kalemler, eski version, merge/move/cancel/rebuild | Eski version 409; olaylardan `is_consistent=true` |
| T-2 | İndirim dağıtımı, üç eşit pay, tahsilat ve Z | İndirim/pay toplamları tam; `is_balanced=true` |
| T-3 | İstasyon/kurs/gecikme ve SSE replay | İmleçten sonraki olaylar eksiksiz |
| T-4 | 1,5 porsiyon/modifier/alt reçete, fiziksel sayım | Gerçek gram tüketimi ve maliyetli fark raporu |
| T-5 | 3 lahmacun+İskender+ayran+künefe, SOFRA50 | 245 TL paket indirimi, 92,50 TL tekil seçenekten avantajlı; açıklama |
| SECURITY | Şube 2'ye personel erişimi, müşteri yazımı, yetkisiz reset | 403; veri değişmez |
| IDEMPOTENCY | Aynı anahtarla sekiz eşzamanlı POST | Tek kalem; değişen gövde 409; farklı kullanıcı ayrı kapsam |
| PAYMENT | Kısmi + BY_ITEM, tekrar ödeme, kapalı adisyona ekleme | Kalem ve adisyon bakiyesi birlikte kapanır; fazla işlem reddedilir |
| STOCK | Üretim öncesi iptal, üretilmiş iptal | Tam gram iadesi veya 24.750 kr fire; ikinci stok düşümü yok |
| SSE-REPLAY | İmleçten sonra 205 olay ve tek yeniden bağlantı | Partileme sınırını aşan olaylar kayıpsız |
| SSE-LIVE | Açık bağlantı sırasında yeni olay, iki farklı cursor | Yeni olay gelir; Last-Event-ID üstün |
| SNAPSHOT | 20 paralel yazım sırasında 35 rebuild | Tüm okumalar tutarlı anlık görüntü |
| SHIFT | Paralel açma, kapama, yeni vardiya ve eski rapor | Tek açık vardiya; 201/409; geçmiş rapor korunur |

## Birim testleri ve kalite kapısı

```bash
make test
# Go/C derleyicisi olmayan Windows için Linux konteynerinde:
docker build --target quality --output type=local,dest=evidence/backend backend

cd frontend
npm ci
npm run check
npm run lint
npm run build
```

Go kalite kapısı `go vet`, `gofmt`, `go test -race` ve `check_quality.py` kullanır. Üst düzey test sayısı ≥40, domain statement kapsamı ≥%70 olmalıdır. Alt testler sayıyı şişirmek için ayrı sayılmaz. Son birim sonucu 56 test / %96,1 domain kapsamıdır. Linux yarış sonucu README'de ayrıca belirtilir. Canlı eşzamanlı PostgreSQL kanıtı `make verify` içindedir; yalnız bellek üzerinde yarış testiyle yetinilmez.

Her zorlu görevin birim kanıtı (`backend` dizininde):

| Görev | Komut | Canlı ek kanıt |
|---|---|---|
| T-1 | `go test -race -v -run 'TestT1_' ./internal/domain/...` | T-1, IDEMPOTENCY, SNAPSHOT |
| T-2 | `go test -v -run 'TestT2_|TestPayment' ./internal/domain/...` | T-2, PAYMENT, SHIFT |
| T-3 | `go test -v -run 'TestT3_' ./internal/domain/...` | T-3, SSE-REPLAY, SSE-LIVE |
| T-4 | `go test -v -run 'TestT4_|TestNestedRecipe|TestRecipeCycle|TestRemovalModifier' ./internal/domain/...` | T-4, STOCK |
| T-5 | `go test -v -run 'TestT5_|TestCampaign' ./internal/domain/...` | T-5 |

## Arayüzde kısa senaryolar

Önce `make reset-demo`, sonra [localhost:3000](http://localhost:3000) açın. Rol düğmeleri gerçek demo girişi yapar. Oturum değişince önceki role ait seçili veri temizlenir.

1. **Garson:** Boş T-01'i açın. İskender, 1,5 porsiyon ve ekstra tereyağı seçip ekleyin; toplam **560 TL** olmalı. Olaylardan doğrula ile tutarlılığı görün. Boş hedef seçerek taşıyın; iki ödenmemiş adisyonu birleştirin. Mutfakta işleme alınmamış kalemi iptal edince stok iadesi bekleyin.
2. **Aşçı:** Aynı şubede KDS'yi açın. Canlı bağlantı durumunu, HOT/COLD/BAR ve kurs sırasını görün. Fişi sırayla Hazırlanıyor → Hazır → Servis edildi yapın. Başka pencerede kalem eklenince fiş gelmeli. Tarayıcı bağlantısını kısa kesip açınca son event ID'den devam etmeli. Üretilmiş bir kalem garsondan iptal edilince gerçek fire oluşmalı.
3. **Kasiyer:** Ödenmemiş adisyon seçin; SOFRA50 ile kampanyayı değerlendirin. Eşit/3 kişi önizlemesini alın ve **her payı ayrı ödeyin**; son paydan sonra bakiye sıfır olmalı. Başka adisyonda kısmi ödeme ardından kaleme göre kalan borcu seçip kapatın. Karışık yöntemde nakit/kart toplamı ve bahşişi kontrol edin. Z raporu BALANCED olmalı.
4. **Vardiya:** Sayılmış kasa alanına gerçek tutarı (0 da geçerli) girip kapatın. Yeni vardiya açmadan tahsilat reddedilmeli. Açılış nakdiyle yeni vardiya açın; rapor yeni vardiyada sıfırdan başlamalı. Geçmiş vardiya API'den `shift_id` ile okunabilir.
5. **Müdür:** Stok ekranında seçtiğiniz hammaddenin sayım kutusuna fiziksel gram değerini girin; boş bırakılanlar değişmemeli. Sayımı gönderin, gram/maliyet farkını ve hareket geçmişini görün. Sabit örnek sayım gönderilmez.
6. **Merkez:** Sekiz şubeyi değiştirin; merkez ciro/fire ve audit sayfalamasını inceleyin. Sistem sağlığında DB tanılamasını görün. Demo sıfırlama onayını iptal ederek verinin korunmasını deneyin.
7. **Müşteri/mobil:** Müşteri rolünde yalnız QR menü görünmeli. Dar ekranda giriş alanları üst üste, butonlar kullanılabilir olmalı. Yanlış parola/hata ve tekrar dene durumlarını kontrol edin.

## Ölçüm ve hata enjeksiyonu

```bash
make load      # ~40 saniye; simulator durur/geri başlar, evidence/load-summary.json yazar
make explain   # 100 bin geçici satır, önce/sonra EXPLAIN, rollback
make chaos     # PostgreSQL stop/start; sağlık 200 → 503 → 200
```

Bu komutları birbirleriyle veya verify/reset ile aynı anda çalıştırmayın. Kaos betiği trap ile PostgreSQL'i geri başlatır; yük betiği önceki simülatör durumunu korur. Ölçüm sonuçları ve sınırları README'dedir. `scripts/capture.py` çıktıyı UTF-8, UTC/komut/çıkış koduyla saklar:

```bash
python scripts/capture.py evidence/verify.txt docker compose --profile verify run --rm verify
```

## CI

[GitHub Actions](.github/workflows/ci.yml) backend lint/race/kapsam, frontend check/lint/build, konteyner build/health ve 24 canlı kontrolü çalıştırır. Kapsam/test çıktıları artifact olarak yüklenir. Yerel sonuç uzakta CI çalışmış olduğu anlamına gelmez.
