# Gereksinim incelemesi ve tamamlanan aşamalar

Kaynak: [proje.md](proje.md). İnceleme: 25–26 Ekim 2026. Go/PostgreSQL 16/SvelteKit mimarisi korundu; frontend değişiklikleri Tailwind CSS ile yapıldı.

| Aşama | Düzeltilen eksik/hata | Doğrulama |
|---|---|---|
| Yetki ve idempotans | Şubeler arası erişim, müşterinin adisyona yazması, açık reset; atomik olmayan tekrar yanıtı | SECURITY, IDEMPOTENCY, auth testleri |
| Adisyon ve kasa | Ödenmiş adisyona ekleme, merge olayları, kalem/ödeme bakiyesi, tahsilat sonrası indirim | T-1/T-2, PAYMENT, domain testleri |
| Reçete/stok | Sabit gram ve satış bedelinden fire maliyeti, eksik iade, tek düzey alt reçete | T-4, STOCK, recipe testleri |
| KDS/vardiya/kampanya | Canlı bağlantı eksikliği, sınırlı replay, bağlantı aralığı kaybı, vardiya yarışı, fiyat/saat hataları | T-3/T-5, SSE-REPLAY/LIVE, SHIFT |
| Frontend | Rol değişiminde eski veri, sahte sayım, eksik vardiya, yinelenen işlem, mobil/hata durumları | Check/lint/build ve rol bazlı tarayıcı kontrolü |
| Canlı kanıt | Simülatörün test verisine karışması, sınırlı negatif senaryolar, karma okuma snapshot'ı | Paylaşılan demo kilidi, 24/24 API kontrolü |
| CI ve belgeler | Uygulanmayan kapsam eşiği, eksik lint/OpenAPI, tahmini ölçümler, yinelenen ADR | ≥40/%70 kapısı; 40 yol/41 işlem; gerçek k6/EXPLAIN/kaos çıktıları |

## Kapsam eşlemesi

- F-1..F-7 ve T-1..T-5 için [TESTING.md](TESTING.md) komut/beklenti tablosu ve [evidence/verify.txt](evidence/verify.txt) PASS sonuçları bulunur.
- Go 1.23, PostgreSQL 16, otomatik migrasyon/seed, non-root Docker, healthcheck/depends_on ve ayrı sekiz şubeli simülatör korunur.
- En az 40 test, domain ≥%70, CI build/test/lint/konteyner, OpenAPI, metrics, yük/kaos, beşten fazla mimari karar ve reset komutu sağlanır.
- `dz-` tablo öneki, `dz-sk-users`, ilk seed e-postası, `ddo-marul-api`, zorunlu HTTP başlığı/sağlık ucu ve README son satırı korunur.
- README başına geliştirici adı, beş doğrulama sorusu ve gerçek ölçüm tabloları eklendi.

## Sınırlar ve dış teslim

Boş Docker cache ile beş dakika sınırı ölçülmedi. Simülatör aynı seed'de iş seçimlerini tekrarlar; ID/zaman damgası veya dış etkileşimli veritabanı byte düzeyinde aynı olmaz. Yerel testler uzaktaki GitHub Actions koşusu olarak sunulmaz. Şube kilidinin ölçek sınırı ve diğer teknik kısıtlar README'de açıklanır. QR self-order/PWA bonusları uygulanmadı.

Public GitHub yayını, anket gönderimi ve değerlendirme görüşmesi yerel kod düzeltmesinin dışındaki teslim adımlarıdır; yapılmış gibi işaretlenmedi. Proje son tarihi 26 Ekim 2026 23.59'dur.
