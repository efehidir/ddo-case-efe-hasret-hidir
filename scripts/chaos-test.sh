#!/usr/bin/env sh
# ==============================================================================
# SofraPOS — Hata Enjeksiyonu (Kaos) Senaryosu Betiği (Bölüm 9)
# PostgreSQL bağımlılığını durdurur, /health ucunun 503 döndüğünü doğrular,
# ardından bağımlılığı tekrar başlatarak sistemin otomatik toparlandığını kanıtlar.
# ==============================================================================

set -e

API_URL="${API_URL:-http://localhost:8080}"

echo "========================================================================"
echo " [KAOS TESTİ] 1. Başlangıç sağlık kontrolü..."
echo "========================================================================"
curl -fsS "${API_URL}/health" | grep '"status":"healthy"'
echo " -> Sistem sağlıklı (200 OK)."

echo "========================================================================"
echo " [KAOS TESTİ] 2. PostgreSQL konteyneri durduruluyor (Hata Enjeksiyonu)..."
echo "========================================================================"
docker compose stop postgres

sleep 2

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/health" || true)
echo " -> PostgreSQL kapalıyken GET /health yanıt kodu: ${HTTP_CODE}"
if [ "${HTTP_CODE}" != "503" ]; then
  echo "HATA: Veritabanı kapalıyken 503 bekleniyordu, ${HTTP_CODE} alındı."
  docker compose start postgres
  exit 1
fi
echo " -> Beklenen bozulma (503 Service Unavailable / degraded) doğrulandı."

echo "========================================================================"
echo " [KAOS TESTİ] 3. PostgreSQL yeniden başlatılıyor ve toparlanma bekleniyor..."
echo "========================================================================"
docker compose start postgres

RECOVERED=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  CODE=$(curl -s -o /dev/null -w "%{http_code}" "${API_URL}/health" || true)
  if [ "${CODE}" = "200" ]; then
    RECOVERED=1
    break
  fi
  sleep 1
done

if [ "${RECOVERED}" != "1" ]; then
  echo "HATA: Sistem 15 saniye içinde toparlanamadı!"
  exit 1
fi

curl -fsS "${API_URL}/api/v1/branches" | grep '"BR-01"' >/dev/null
echo "========================================================================"
echo " [KAOS TESTİ] PASS — Sistem veritabanı kesintisinden sonra otomatik toparlandı!"
echo "========================================================================"
