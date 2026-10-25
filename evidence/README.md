# Yerel doğrulama kanıtları

Koşular 26 Ekim 2026 İstanbul saatinde, Docker Desktop Linux konteynerleri üzerinde yapıldı. Metin başlıkları UTC kullanır; bu nedenle UTC tarihi 25 Ekim görünebilir. Dosyalar tahmini sonuç değildir.

| Dosya | İçerik |
|---|---|
| `verify.txt` | Son imaj üzerinde 24 gerçek API/PostgreSQL kontrolü, çıkış kodu 0 |
| `backend/race-build.txt` | Linux Go 1.23 gofmt/vet/race/kapsam kapısı; 56 test, domain %96,1 |
| `backend/test-results.jsonl` | Aynı Linux koşusunun ham Go test olayları |
| `backend/coverage.out` | Aynı koşunun statement kapsam profili |
| `backend/coverage-summary.txt` | Fonksiyon bazlı kapsam |
| `load.txt` | k6 komutu, p50/p95/p99, hata ve kontrol toplamları |
| `load-summary.json` | Metrikler; setup JWT'si özellikle dışarıda bırakıldı |
| `explain.txt` | 100 bin sentetik geçici olay satırında iki dizinin önce/sonra planı |
| `chaos.txt` | PostgreSQL stop/start, sağlık 200→503→200 ve sorgu toparlanması |
| `ui-mobile.png` | Rol seçimi/giriş ve dar ekran yerleşimi |

Tarayıcıda ayrıca garsonun 1,5 porsiyon+modifier ile 560 TL kalem eklemesi, aşçı canlı KDS'si, müdür sayım alanları, kasiyerin sıfır bakiyeyle kapanan ödemesi ve dengeli Z raporu, merkez şube erişimi ve müşterinin yalnız menü görmesi kontrol edildi. Bunlar otomatik tarayıcı test paketi olarak sunulmaz.

Yük ve SQL ölçümlerinin koşulları/sınırları README'dedir. Yeniden üretim komutları TESTING.md içindedir. GitHub Actions uzaktan çalıştırılmadı; workflow yerelde geçen kontrolleri tekrarlayacak şekilde düzenlendi.
