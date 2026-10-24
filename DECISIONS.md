# Mimari Karar Kayıtları (Architecture Decision Records — ADR)

Bu doküman, projede alınan kritik mimari kararları, değerlendirilen alternatifleri ve bu kararların teknik sonuçlarını (trade-off) kayıt altına alır.

---

## ADR-001: Backend HTTP Katmanında Go (1.22+) ve Fiber v2 Seçimi

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Yüksek eşzamanlılık (concurrency) altında düşük gecikme süresi (`p99 < 50ms`), düşük bellek tüketimi ve hızlı konteyner başlatma süresi (cold start) sağlayan bir HTTP sunucu katmanına ihtiyaç duyulmaktadır.
- **Karar:**
  Backend servisi Go 1.22+ üzerinde `github.com/gofiber/fiber/v2` çatısı kullanılarak geliştirilmiştir.
- **Değerlendirilen Alternatifler:**
  - *Go standart `net/http` (`http.ServeMux`):* Ek bağımlılık gerektirmez ancak middleware zinciri (CORS, kurtarma/recover, metrik adaptörleri) için daha fazla şablon kod (boilerplate) gerektirir.
  - *Gin / Echo:* Yaygın topluluk desteğine sahiptir ancak `fasthttp` tabanlı Fiber, sıfır bellek tahsisi (zero-allocation) odaklı yönlendiricisiyle yüksek RPS yük testlerinde daha düşük kuyruk gecikmesi sunar.
- **Sonuçlar:**
  - **Olumlu:** Çok düşük CPU/RAM ayak izi, yerleşik `recover` ve `cors` middleware desteği, `fasthttp` adaptörü ile standart `promhttp` metriklerinin sorunsuz entegrasyonu.
  - **Olumsuz / Risk:** `fasthttp` tabanlı olduğu için standart `net/http` handler'ları `gofiber/fiber/v2/middleware/adaptor` üzerinden sarmalanmalıdır.

---

## ADR-002: PostgreSQL 16 Erişimi İçin ORM Yerine Doğrudan `pgx/v5` (`pgxpool`) Kullanımı

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  PostgreSQL 16 veritabanı ile iletişimde bağlantı havuzu yönetimi (connection pooling), sağlık kontrolleri (`Ping`), düşük sorgu gecikmesi ve havuz telemetrisinin (`total_conns`, `idle_conns`, `acquired_conns`) gözlemlenebilir olması gerekmektedir.
- **Karar:**
  Veritabanı sürücüsü ve bağlantı havuzu olarak doğrudan `github.com/jackc/pgx/v5/pgxpool` kullanılmış ve `database.Probe` arayüzü ile soyutlanmıştır.
- **Değerlendirilen Alternatifler:**
  - *GORM:* Hızlı prototipleme sağlar ancak reflection maliyeti nedeniyle gecikmeyi artırır ve bağlantı havuzu istatistiklerine doğrudan erişimi kısıtlar.
  - *`database/sql` + `lib/pq`:* `lib/pq` bakım modundadır ve PostgreSQL'in ikili protokol (binary protocol) optimizasyonlarını `pgx/v5` kadar verimli kullanamaz.
- **Sonuçlar:**
  - **Olumlu:** Yerel PostgreSQL protokol desteği, sıfıra yakın soyutlama maliyeti, `pgxpool.Stat()` ile gerçek zamanlı havuz telemetrisi ve `database.Probe` arayüzü sayesinde veritabanı olmadan hızlı birim testi (`go test`) yazabilme.
  - **Olumsuz / Risk:** SQL sorguları doğrudan yazıldığı için şema değişikliklerinde sorguların manuel veya `sqlc` ile senkronize tutulması gerekir.

---

## ADR-003: Frontend Katmanında SvelteKit (`@sveltejs/adapter-node`) ve Çift Yönlü API İletişimi

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Kullanıcı arayüzünün `http://localhost:3000` adresinde hem ilk sayfa yüklemesinde (SSR) hızlı yanıt vermesi hem de tarayıcı üzerinden etkileşimli canlı veritabanı testi yapabilmesi gerekmektedir.
- **Karar:**
  Frontend katmanı Svelte 5 + SvelteKit 2 ve `@sveltejs/adapter-node` ile yapılandırılmıştır. SSR aşamasında Docker Compose iç ağındaki `INTERNAL_API_URL` (`http://backend:8080`) kullanılırken, istemci tarafı (tarayıcı) tetiklemelerinde `PUBLIC_API_URL` (`http://localhost:8080`) kullanılmaktadır.
- **Değerlendirilen Alternatifler:**
  - *Statik SPA (Nginx + Vite):* Yalnızca istemci tarafında çalışır; ilk yüklemede (FCP) tarayıcının ayrıca API isteği atmasını bekler ve sunucu taraflı sağlık kontrolünü কেবল statik dosya üzerinden yapar.
- **Sonuçlar:**
  - **Olumlu:** İlk sayfa açılışında sağlık ve PostgreSQL 16 tanılama verileri sunucu tarafında (`+page.server.ts`) Docker iç ağından milisaniyeler içinde çekilerek hazır HTML olarak sunulur; ayrıca `/health` ucu Node.js çalışma zamanının gerçek sağlığını doğrular.
  - **Olumsuz / Risk:** Statik Nginx imajına kıyasla Node.js çalışma zamanı (~50 MB RAM) gerektirir.

---

## ADR-004: Ağ İzolasyonu, Non-Root Konteynerler ve Multi-Stage Docker İmajları

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Konteyner güvenliği (en az ayrıcalık prensibi), hızlı imaj indirme/derleme süresi (soğuk makinede < 5 dakika) ve veritabanının dış dünyaya kapatılması temel altyapı gereksinimleridir.
- **Karar:**
  1. **Ağ İzolasyonu:** `postgres:16-alpine` servisi host makineye `ports` ile açılmamış, yalnızca `ddo_internal` bridge ağı içinde `expose: ["5432"]` ile erişilebilir kılınmıştır.
  2. **Çok Aşamalı Derleme (Multi-Stage):** Backend `golang:1.23-alpine` -> `alpine:3.20` (`CGO_ENABLED=0`, `-ldflags="-s -w"`), Frontend ise `node:22-alpine` builder -> `node:22-alpine` runtime olarak ayrılmıştır.
  3. **Root Olmayan Kullanıcı (Non-Root):** Backend `appuser:appgroup` (`10001:10001`), Frontend `node:node` (`1000:1000`) ve PostgreSQL `70:70` (`postgres`) kullanıcısı ile çalıştırılmaktadır.
- **Değerlendirilen Alternatifler:**
  - *Tek aşamalı (Single-Stage) Dockerfile:* Derleyici araçlarını (Go SDK, npm build cache) çalışma zamanı imajında bırakarak imaj boyutunu >800 MB seviyesine çıkarır ve saldırı yüzeyini genişletir.
- **Sonuçlar:**
  - **Olumlu:** Backend imajı ~20 MB seviyesindedir; olası bir konteyner kaçışında (container escape) süreçler `root` yetkisine sahip değildir; PostgreSQL port çakışmalarından ve dış ağ taramalarından tamamen izoledir.
  - **Olumsuz / Risk:** Host makineden doğrudan `psql -h localhost` ile bağlanmak istendiğinde `docker compose exec postgres psql` komutu kullanılmalıdır.

---

## ADR-005: Deterministik Orkestrasyon İçin `healthcheck` ve `service_healthy` Bağımlılık Zinciri

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Standart `depends_on` tanımı yalnızca konteynerin başlatılmasını bekler, içindeki sürecin (örn. PostgreSQL'in WAL kurtarmasını tamamlayıp TCP bağlantı kabul etmesini) hazır olmasını beklemez. Bu durum soğuk başlatmalarda "connection refused" hatalarına ve konteyner yeniden başlatma döngülerine yol açar.
- **Karar:**
  Tüm servislerde (`postgres`, `backend`, `frontend`) `healthcheck` tanımlanmış ve `depends_on` bloklarında `condition: service_healthy` zorunlu kılınmıştır:
  `postgres (pg_isready)` -> `backend (GET /health)` -> `frontend (GET /health)`.
- **Sonuçlar:**
  - **Olumlu:** `docker compose up --build` komutu çalıştırıldığında yarış durumu (race condition) yaşanmadan tüm katmanlar deterministik sırayla ayağa kalkar.
  - **Olumsuz / Risk:** Toplam başlatma süresine sağlık kontrolü aralıkları (`interval: 5s`) kadar birkaç saniyelik ek bekleme eklenir.

---

## ADR-006: Gömülü OpenAPI 3.0 Spesifikasyonu ve Özel Kayıtlı (Custom Registry) Prometheus Metrikleri

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  API sözleşmesinin çalışma zamanında her zaman güncel olması ve servisin dış izleme sistemleri (Prometheus/Grafana) tarafından ek ajan gerektirmeden izlenebilmesi gerekmektedir.
- **Karar:**
  - OpenAPI 3.0.3 şemaları (`spec.yaml` ve `spec.json`) Go 1.22 `//go:embed` direktifi ile doğrudan ikili dosyaya (binary) gömülmüş; `/openapi.yaml`, `/openapi.json` ve `/docs` (Swagger UI) uçlarından sunulmuştur.
  - Prometheus metrikleri için global varsayılan kayıt defteri yerine izole `prometheus.NewRegistry()` kullanılmış; Go runtime, süreç metrikleri, HTTP istek/gecikme metrikleri (`http_requests_total`, `http_request_duration_seconds`, `http_requests_in_flight`) ve PostgreSQL sağlık metrikleri (`db_up`, `db_ping_duration_seconds`) `/metrics` ucundan açılmıştır.
- **Sonuçlar:**
  - **Olumlu:** Konteyner içine harici `.yaml`/`.json` dosyası kopyalama zorunluluğu ortadan kalkmıştır; izole Prometheus registry sayesinde birim testlerde paralel sunucu örnekleri oluşturulduğunda "duplicate metrics collector registration" paniği yaşanmaz.
  - **Olumsuz / Risk:** OpenAPI şeması güncellendiğinde yeni konteyner imajının derlenmesi gerekir.



---

## ADR-007: Adisyon Tutarlılığı İçin Olay Günlüğü (Event Sourcing) ve Optimistic Locking (`T-1`)

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Aynı masaya iki garsonun eşzamanlı kalem eklemesi, masa taşıma, masa birleştirme, üretilmiş kalemin iptali (fire) ve kısmi ödeme sırasında yeni kalem eklenmesi süreçlerinde veri kaybı veya tutarsız toplam oluşmamalıdır.
- **Karar:**
  `"dz-orders"` tablosunda `version` sütunu ile **Optimistic Locking** (`expected_version` kontrolü) ve satır kilidi (`SELECT ... FOR UPDATE`) birlikte uygulanmış; her durum değişikliği `"dz-order-events"` tablosuna eklemeli-yalnız (append-only) olay olarak kaydedilmiştir. `RebuildOrderStateFromEvents` fonksiyonu adisyon durumunu olaylardan kuruşu kuruşuna yeniden inşa eder.
- **Sonuçlar:**
  - **Olumlu:** Eşzamanlı güncellemelerde kayıp güncelleme (lost update) imkansızdır (`409 Conflict` döner); adisyonun mevcut toplamı her an olay günlüğünden doğrulanabilir (`GET /api/v1/orders/:id/rebuild`).
  - **Olumsuz / Risk:** Her adisyon mutasyonu ek bir olay satırı (`INSERT INTO "dz-order-events"`) yazar.

---

## ADR-008: Tam Sayı (Kuruş) Aritmetiği ve En Büyük Kalan (Largest Remainder) İndirim Dağıtımı (`T-2`)

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Kayan noktalı (`float`) aritmetik kullanıldığında 100.00 TL'nin 3'e bölünmesi veya 50.00 TL indirimin 3 kaleme dağıtılması sırasında 1 kuruşluk yuvarlama farkları oluşmakta ve gün sonu Z raporunun ödeme toplamlarıyla uyuşmamasına yol açmaktadır.
- **Karar:**
  Tüm parasal değerler veritabanında `BIGINT` ve Go katmanında `int64` kuruş olarak temsil edilmiştir. İndirimlerin kalemlere dağıtılmasında **En Büyük Kalan (Largest Remainder)** yöntemi, eşit bölüşmede ise kalan kuruşların (`remaining % parts`) ilk paylara `+1` kuruş eklenmesi uygulanmıştır.
- **Sonuçlar:**
  - **Olumlu:** $\sum \text{kalem\_indirimi} == \text{toplam\_indirim}$ ve $\sum \text{bölüşme\_payı} == \text{kalan\_tutar}$ değişmezi (invariant) matematiksel olarak garanti altındadır; Z raporu kuruşu kuruşuna tutar.

---

## ADR-009: Ağaç Yapılı Alt Reçete (Yarı Mamul) Çözümleme ve Deterministik Kampanya Optimizasyonu (`T-4` & `T-5`)

- **Durum:** Kabul Edildi
- **Tarih:** 2026-10-24
- **Bağlam:**
  Restoran mutfağında ürünler doğrudan hammaddelerin yanı sıra yarı mamuller (*Özel İskender Sosu*, *Lahmacun İç Harcı*) kullanır. Ayrıca sepette birden fazla kampanya ve kupon aynı anda tetiklendiğinde müşteriye en yüksek faydayı sağlayan kombinasyonun deterministik olarak seçilmesi gerekir.
- **Karar:**
  - `ResolveRecipeTree` fonksiyonu ürün reçetesindeki yarı mamulleri (`is_semi_finished = true`) alt reçete bileşenlerine özyinelemeli olarak ayrıştırır, varyant çarpanı (`recipe_multiplier_pct`) ve modifier gramajlarını ekler.
  - `EvaluateBestCampaigns` fonksiyonu birleştirilebilir (`stackable = true`) kampanya/kupon kümesinin toplam indirimini, tekil (`stackable = false`) kampanya/kuponların her biriyle karşılaştırarak maksimum indirimi sağlayan stratejiyi seçer ve tüm karar gerekçelerini `campaign_explanation` (JSONB) olarak kaydeder.
