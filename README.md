# SofraPOS

**Geliştiren: Efe Hasret Hıdır**

Sekiz şubeli restoran için sipariş, mutfak, kasa, reçete bazlı stok ve raporlama uygulaması. Gereksinimler [proje.md](proje.md), düzeltmeler [REQUIREMENTS.md](REQUIREMENTS.md) içinde izlenebilir.

## Kurulum

Docker Engine/Desktop ve Compose v2 gerekir. PowerShell'de ilk satır için `Copy-Item .env.example .env` kullanılabilir.

```bash
cp .env.example .env
docker compose up -d --build --wait
docker compose --profile verify run --rm verify
```

Migrasyon ve seed API açılışında otomatik, tek transaction içinde uygulanır. Simülatör ayrı servistir. PostgreSQL 16 yalnızca Compose ağında erişilir. Servisler root olmayan kullanıcılarla çalışır; API/frontend imajları çok aşamalıdır. Boş Docker önbelleğiyle beş dakika sınırı ayrıca ölçülmedi.

| Adres | İşlev |
|---|---|
| [localhost:3000](http://localhost:3000) | Mobil operasyon ekranı |
| [localhost:8080/health](http://localhost:8080/health) | PostgreSQL dahil sağlık; kesintide 503 |
| [localhost:8080/_hz/cc64](http://localhost:8080/_hz/cc64) | Proje sağlık ucu, 200 |
| [localhost:8080/docs](http://localhost:8080/docs) | Swagger UI; varlıkları CDN'den yüklenir |
| [localhost:8080/openapi.json](http://localhost:8080/openapi.json) | 40 yol / 41 işlem için OpenAPI |
| [localhost:8080/metrics](http://localhost:8080/metrics) | Prometheus HTTP, Go ve veritabanı metrikleri |

`make up` ve `make verify` aynı işlemleri kolaylaştırır. Make/shell betikleri Linux, macOS veya Git Bash'te çalışır. Elle API denemeleri için [requests.http](requests.http) bulunur.

```bash
make reset-demo  # Simülatörü durdurur, ilk seed'i oluşturur; simülatör kapalı kalır.
make demo-live   # Sekiz şubeli canlı akışı başlatır.
make down        # Servisleri durdurur, veritabanı volume'unu korur.
```

Doğrulayıcı demo verisini sıfırlar. Simülatörle paylaşılan PostgreSQL oturum kilidi sayesinde doğrulama boyunca veri üretimi bekler, sonunda devam eder. Elle denemede sabit başlangıç için önce `make reset-demo` kullanın.

## Demo hesapları

Tüm hesapların parolası **`Sofra2026!`**; veritabanında bcrypt özeti tutulur. Rol düğmeleri gerçek giriş yapar. Personel şube 1'e bağlıdır; yalnız merkez sekiz şubeyi seçebilir. Şube sınırı sunucuda da uygulanır.

| Rol | E-posta | Erişim |
|---|---|---|
| İlk seed / merkez | `seed.ceylan@ddo-case.invalid` | Tüm şubeler ve merkez işlemleri |
| Garson | `garson@sofrapos.local` | Masa, adisyon, taşıma, birleştirme, KDS izleme |
| Kasiyer | `kasiyer@sofrapos.local` | Adisyon, kampanya, ödeme, vardiya, Z raporu |
| Aşçı | `asci@sofrapos.local` | KDS durum geçişleri, stok izleme |
| Şube müdürü | `mudur@sofrapos.local` | Şube operasyonları, sayım, menü ekleme, denetim |
| Merkez | `merkez@sofrapos.local` | Tüm operasyonlar, merkez raporu, tanılama, sıfırlama |
| Müşteri | `musteri@sofrapos.local` | QR menü; adisyona yazamaz |

## Mimari ve iş kuralları

Go 1.23 / Fiber, pgx/v5 ve PostgreSQL 16; frontend'de Svelte 5 / SvelteKit 3, TypeScript ve **Tailwind CSS 4** kullanılır. Svelte rol/canlı veri değişimini az istemci koduyla yönetir; Tailwind ortak boşluk, odak, hata ve mobil yerleşim kuralları sağlar. Ayrıntılar [DECISIONS.md](DECISIONS.md) içindedir.

- **Tutarlılık:** Mutasyon, olay, stok, denetim ve idempotans yanıtı aynı transaction'da commit edilir. Şube kilidi yazmaları sıralar; `FOR UPDATE` ve isteğe bağlı `expected_version` eski istemciyi 409 ile reddeder. Adisyon olaylardan yeniden kurulabilir. Ayrıntı/rebuild/KDS/stok okumaları aynı `REPEATABLE READ` anlık görüntüsünü kullanır.
- **İdempotans:** Kritik yazmalarda anahtar kullanıcı + metot + yol kapsamındadır. Aynı gövde tekrarında aynı yanıt, farklı gövdede 409 döner. Frontend ağ hatasında aynı anahtarı korur ve yinelenen tıklamayı engeller.
- **Kasa:** Para `int64`/`BIGINT` kuruştur. İndirim en büyük kalan yöntemiyle dağıtılır. Eşit, kalem, özel tutar, kısmi ve nakit/kart karışık ödeme desteklenir. Ödeme kalem bakiyelerine de işlenir. Tahsilat sonrası kampanya ve iptal kilitlenir; yeni kalem mevcut indirimi korur. Tam ödenen adisyona ekleme yapılamaz.
- **Stok:** Alt reçeteler döngü kontrolüyle hammaddelere açılır; varyant/modifier gramajları uygulanır. Tüketim kalem bazında kaydedilir. Üretim öncesi iptal aynı gramları iade eder; üretilmiş iptal tüketimi fire yapar, ikinci stok düşümü oluşturmaz. Sayım yalnızca girilen hammaddeleri değiştirir.
- **Mutfak:** Soğuk/sıcak/bar, kurs, hazırlık hedefi ve gecikme görünür. Durum sırası `PENDING → PREPARING → READY → SERVED` olur. SSE olayları kalıcıdır; 200'lük partiler sonuna kadar okunur. `Last-Event-ID` sorgu imlecine üstündür. İstemci 25 saniyelik yenilemelerde olayları geri alır.
- **Kampanya:** X al Y öde, kombo, saatlik ve kupon kuralları; gerçek varyant fiyatı, öncelik ve birlikte kullanım kurallarıyla değerlendirilir. En iyi izinli paket ve tekil seçenekler karşılaştırılır, nedenleri gösterilir. Sunucunun İstanbul saati kullanılır. Ödeme öncesi sepet değişirse indirim temizlenir, yeniden uygulanmalıdır.
- **Vardiya:** Şube başına tek açık vardiya; kapandıktan sonra yenisi açıkça açılır. Z raporu istenen/son vardiyayı tek SQL anlık görüntüsünde okur. Nakit bahşiş beklenen kasaya dahildir. Merkez raporu yaşam boyu şube toplamlarını gösterir.
- **Güvenlik:** Access/refresh JWT, rol/şube denetimi, iş hatalarında ortak hata biçimi, keyset sayfalama, kim/eski/yeni değerlerle append-only denetim kaydı bulunur. HTTP zaman aşımı, düzgün kapanış ve JSON log kullanılır; parola/token loglanmaz. Tablolar `dz-` öneklidir; kullanıcı tablosu `dz-sk-users`, yanıt başlığı `X-DDO-Case: cc6433` olur.

Simülatör seed=42 ile sekiz şubede eşzamanlı başlangıç akışı, ardından sipariş/iptal/taşıma/ödeme/KDS olayları üretir. Aynı temiz seed ve işlem sırası iş seçimlerini tekrarlar. Zaman damgaları, global ID sırası ve dış müdahaleli çalışmanın son verisi byte düzeyinde aynı değildir.

## Test kanıtları

[TESTING.md](TESTING.md) her F/T kimliği için komut ve beklenen sonucu içerir. CI; gofmt, go vet, yarış algılayıcısı, en az 40 test / domain kapsamı %70, frontend check/lint/build ve Compose üzerinde canlı doğrulama çalıştırır. Uzak GitHub Actions koşusu depo yayınına bağlıdır; aşağıdakiler yerel sonuçlardır.

| Kontrol | Sonuç |
|---|---|
| Go testleri | 56 üst düzey test PASS; domain %96,1 (468/487 statement) |
| Linux yarış algılayıcısı | 56 test PASS; yarış saptanmadı, [Linux çıktısı](evidence/backend/race-build.txt) |
| Canlı doğrulama | 24/24 PASS, [çıktı](evidence/verify.txt) |
| Frontend | Check 0 hata / 0 uyarı; lint ve üretim build PASS |
| PostgreSQL kaos | 200 → 503 → 200; sorgu toparlandı, [çıktı](evidence/chaos.txt) |

### Yük testi

`make load`: 26 Ekim 2026, Docker Desktop Linux ve yerel Compose ağı; 40 saniye, okuma senaryosu 0→25→50→0 sanal kullanıcı, ek olarak sekiz şubede birer yazıcı. Yazıcı adisyon açar, ürün ekler ve kartla kapatır: 936 mutasyon / 312 kapanan adisyon. Simülatör geçici durdurulur. Donanım/önbellek sabitlenmedi; üretim kapasitesi garantisi değildir.

| Trafik | p50 | p95 | p99 | Hata |
|---|---:|---:|---:|---:|
| Tüm HTTP | 1,164 ms | 3,738 ms | 11,407 ms | %0 |
| Okuma | 1,142 ms | 2,488 ms | 3,801 ms | %0 |
| Mutasyon | 8,678 ms | 15,640 ms | 29,902 ms | %0 |

**23.789 istek**, yaklaşık **591 istek/sn**, **24.100/24.100 kontrol PASS**. Eşikler: hata <%1, HTTP/mutasyon p95 <500 ms ve p99 <1000 ms. [Çıktı](evidence/load.txt), [token içermeyen metrikler](evidence/load-summary.json), [senaryo](tests/load/load-test.js).

### Dizin ölçümü

`make explain`, [betikteki](tests/sql/explain.sql) **100.000 sentetik satırlı geçici tabloda** iki dizini eklemeden önce/sonra `EXPLAIN (ANALYZE, BUFFERS)` çalıştırır ve rollback yapar. Gerçek olay tablosunun kolon biçimi kullanılır. Tek yerel koşudur; cache etkisi kontrol edilmedi, üretim verisi ölçümü değildir.

| Dizin / sorgu | Dizinsiz | Dizinli | Plan |
|---|---:|---:|---|
| `(order_id,id)` / adisyon olayları | 9,006 ms | 0,385 ms | Seq Scan → Bitmap Index Scan |
| `(branch_id,id)` / SSE imleci | 6,470 ms | 0,104 ms | Seq Scan + Sort → Index Scan |

[Tam çıktı](evidence/explain.txt) içinden:

```text
ORDER_REBUILD_WITHOUT_INDEX
Seq Scan on benchmark_events ... Rows Removed by Filter: 99950
Execution Time: 9.006 ms
ORDER_REBUILD_WITH_INDEX
Bitmap Index Scan on bench_order_id ... Index Cond: (order_id = 1)
Execution Time: 0.385 ms
SSE_REPLAY_WITHOUT_INDEX
Seq Scan on benchmark_events ... Rows Removed by Filter: 99875
Execution Time: 6.470 ms
SSE_REPLAY_WITH_INDEX
Index Scan using bench_branch_id ... Index Cond: ((branch_id = 8) AND (id > 99000))
Execution Time: 0.104 ms
```

## Doğrulama Soruları

1. **En zor görev ve alternatifler:** Adisyon, ödeme, stok ve olayları birlikte tutarlı tutmak. Frontend kilidi başka istemcileri korumaz; yalnız optimistic kontrol de stok/ödeme/idempotans kaydını atomik yapmaz. Tek transaction ve şube kilidi seçildi; eski versiyon kontrolü korundu. Karşılığı bağımsız masaların aynı şubede sıralanmasıdır.
2. **Bilinen sınırlama:** Çok yoğun tek şube kilitte bekler; kısa yük testi bu sınırı bulmaz. Daha yüksek ölçek masa/adisyon kilitleri ve merge için sabit kilit sırası gerektirir. SSE polling kullanır. Oturum bellektedir, yenilemede tekrar giriş gerekir. QR self-order ve çevrimdışı PWA bonusları uygulanmadı. Demo hesapları/sıfırlama ucu üretim dağıtımına uygun değildir.
3. **İki dizin:** `(order_id,id)` 9,006→0,385 ms; `(branch_id,id)` 6,470→0,104 ms. Yukarıdaki tablo, sentetik fixture sınırı ve EXPLAIN çıktısı kapsamı belirtir.
4. **Düzeltilen yapay zekâ çıktısı:** Önceki çözüm iptalde sabit `150 g` fire ve `satış tutarı / 3` maliyet yazıyor, üretim öncesi iptalde stok iade etmiyordu. Sonrasında kalemin gerçek tüketimi kaydedilip aynen iade/fireye dönüştürüldü. 1,5 porsiyon İskender + ekstra tereyağında test edilen tüketim `270 g et + 180 g domates + 75 g tereyağı + 120 g yoğurt`; fire maliyeti **24.750 kuruş**. `STOCK` ikinci stok düşümü olmadığını da doğrular.
5. **Tutarlılık kanıtı:** `docker compose --profile verify run --rm verify` gerçek PostgreSQL üzerinde eşzamanlı kalemler, eski versiyon, taşıma/birleştirme, ödeme ve rebuild çalıştırır. Son koşudan:

   ```text
   T-1         PASS  Eşzamanlı kalem, optimistic lock (409), taşıma, birleştirme, fire ve rebuild
   IDEMPOTENCY PASS  8 eşzamanlı aynı anahtar yalnızca 1 kalem oluşturdu; farklı gövde 409
   SNAPSHOT    PASS  20 eşzamanlı yazım sırasında 35 rebuild okuması tutarlı
   SONUÇ: Tüm 24 doğrulama kimliği PASS! / Exit code: 0
   ```

## Teslim

Yerel commitler aşamalıdır. Public GitHub deposu/push ve bağlantının [ankete](https://banukampus.bandirma.edu.tr/a/s/ZB76YDBA) gönderilmesi ayrıca yapılmalıdır. Son tarih **26 Ekim 2026 23.59**; teslimden sonra force push yapılmamalıdır.

DDO Case Kodu: CEYLAN-cc6433
