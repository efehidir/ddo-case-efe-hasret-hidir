# Gereksinim incelemesi ve çalışma aşamaları

Kaynak: `proje.md`. İnceleme tarihi: 25 Ekim 2026.

Mevcut Go/PostgreSQL 16/SvelteKit/Tailwind CSS mimarisi korunur.

| Aşama | Bulgu / yapılacak düzeltme | Kanıt |
|---|---|---|
| 1 | Şubeler arası yetki açığı, müşterinin herhangi bir adisyona yazabilmesi, açık demo sıfırlama ucu; atomik olmayan ve kullanıcı/istek kapsamı bulunmayan idempotans | RBAC ve eşzamanlı tekrar istek testleri |
| 2 | Ödenmiş adisyona ekleme, birleştirmede kaynak olaylarının eksikliği, kalem ödemelerinin toplam ödemeyle ayrışması, ödeme sonrası indirim/iptal | Domain ve canlı API regresyonları |
| 3 | Fire için sabit 150 g / satış bedelinin üçte biri kullanılması, üretim öncesi iptalde stok iadesinin olmaması, yalnızca tek düzey alt reçete | Reçete, gerçek stok hareketi ve sayım testleri |
| 4 | KDS istemcisinin canlı bağlantı açmaması, SSE'de 100 olay sınırı ve replay/abonelik aralığında kayıp, vardiya yarışları | SSE replay, vardiya ve Z raporu testleri |
| 5 | Rol değişiminde önceki adisyonun kalması, hardcoded fiziksel sayım, eksik vardiya akışı, tekrar tıklama ve boş/hata durumları | Tailwind CSS arayüzü, typecheck, lint, build, tarayıcı kontrolü |
| 6 | CI'da kapsam eşiğinin uygulanmaması, frontend lint eksikliği, doğrulamada yetersiz negatif senaryolar, ölçülmemiş performans iddiaları | CI, `make verify`, yük/SQL ölçüm çıktıları ve güncel dokümanlar |

Her aşama ilgili kontrollerden sonra ayrı ve anlamlı bir commit olarak kaydedilir. Ölçülmeyen sonuçlar ölçülmüş gibi sunulmaz. GitHub'a yayınlama ve anket gönderimi yerel kod düzenleme kapsamından ayrı teslim işlemleridir.
