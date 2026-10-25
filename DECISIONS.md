# Mimari kararlar

Durum: Kabul edildi. Gözden geçirme: 26 Ekim 2026.

## ADR-001 — Go/Fiber ve pgx

Go/PostgreSQL görev gereğidir. Fiber mevcut HTTP katmanını, pgx açık SQL/transaction kontrolünü korur. Kilit ve EXPLAIN incelemesi kolaylaşır; sorgu/DTO eşlemesi elle yönetilir. ORM eklenmedi.

## ADR-002 — SvelteKit ve Tailwind CSS

Svelte 5 / SvelteKit 3, TypeScript ve Tailwind CSS 4 kullanılır. SSR ilk menüyü oluşturur; yetkili operasyonlar giriş sonrası API'den alınır. Utility sınıfları ve `operations.css` içindeki `@apply`, ortak odak/hata/mobil kurallarını sağlar. İlave global state kütüphanesi eklenmedi. Büyük operasyon sayfasının rol bileşenlerine ayrılması gelecekte bakımı kolaylaştırabilir.

## ADR-003 — Atomik mutasyon ve şube kilidi

İşlem, olay, stok, denetim ve idempotans yanıtı tek PostgreSQL transaction'ına dahildir; iç transaction'lar savepoint olur. Reset ortak/exclusive kilitle yazmalardan ayrılır. Şube advisory kilidi mutasyonları sıralar, şube SSE ID sırasını commit sırasıyla uyumlu tutar. `FOR UPDATE` ve `expected_version` korunur. Karşılığı farklı masaların aynı şubede beklemesidir. Daha ince kilitlerde merge sırası ve SSE cursor tasarımı birlikte değişmelidir.

## ADR-004 — Kullanıcı kapsamında idempotans

Anahtar kullanıcı/metot/yol kapsamında URL+gövde hash'iyle saklanır. Aynı istek aynı başarılı durum/gövdeyi döndürür; farklı istek 409 olur. İşlemle yanıt kaydını ayrı transaction'lara koymak yanıt kaybında ikinci tahsilat riski taşıdığı için reddedildi. Frontend ağ hatasında anahtarı korur; başarısız işlem başarılı yanıt olarak cache'lenmez.

## ADR-005 — Olay günlüğü ve MVCC

Güncel satırlar hızlı okuma, append-only olaylar rebuild kanıtı içindir. Dağıtık event-sourcing altyapısı kurulmadı. Birden fazla SELECT içeren ayrıntı/rebuild/KDS/stok okumaları read-only `REPEATABLE READ` kullanır; header ve kalemlerin farklı commitleri görmesi önlenir. `SNAPSHOT` canlı testi yazım sırasında tutarlı okumayı sınar. Günlük arşivleme stratejisi henüz yoktur.

## ADR-006 — Tam sayı para ve kalem bakiyeleri

Para `int64`/`BIGINT` kuruştur. İndirim en büyük kalan yöntemiyle, eşit bölüşmede artan kuruşlar ilk paylara dağıtılır. Ödeme kalem bakiyelerine de işlenir. İlk tahsilat sonrası indirim/iptal kilitlenir. Nakit/kart alt tutarları doğrulanır; nakit bahşiş kasaya katılır. Kapanış yeni vardiya açmaz, geçmiş Z raporları korunur.

## ADR-007 — Gerçek tüketim ve fire

Alt reçeteler özyinelemeli ve döngü kontrolüyle açılır; gram dağıtımı toplamı korur. Varyant/modifier etkisi hareketlere yazılır. İptal güncel reçeteyi yeniden hesaplamak yerine orijinal tüketimi kullanır. Üretilmiş iptal tüketimi WASTE yapar, ikinci stok düşmez. Sabit gram/satış fiyatından maliyet yaklaşımı kaldırıldı; hareket anındaki hammadde maliyeti saklanır.

## ADR-008 — Kalıcı SSE ve kampanya kararları

KDS tek yönlü bildirim için SSE kullanır. PostgreSQL olayları 200'lük partilerle sonuna kadar okunur. Bellek broker'ı yeniden başlatma ve replay/abonelik aralığında kayıp yaratabileceği için bırakıldı. Polling yaklaşık bir saniye gecikme ve ek sorgu maliyeti getirir. `Last-Event-ID` üstündür; bağlantı 25 saniyede yenilenir. Kampanya motoru izinli paket ve tekil alternatifleri deterministik öncelikle karşılaştırır, açıklamaları saklar; gerçek varyant fiyatı ve İstanbul saati kullanır.

## ADR-009 — İzole, ölçülebilir doğrulama

Go CLI gerçek API/PostgreSQL üzerinde F/T ve negatif regresyonları çalıştırır; hatada sıfırdan farklı çıkar. Simülatörle paylaşılan pgx oturum kilidi test sırasında üretimi bekletir, bağlantı kapanınca serbest kalır. Yük testi simülatörü geçici durdurur. CI en az 40 test ve domain %70 kapsamı uygular. Sentetik EXPLAIN ve kısa yerel k6 testi üretim kapasitesi iddiası değildir.
