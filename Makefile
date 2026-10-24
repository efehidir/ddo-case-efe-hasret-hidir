# ==============================================================================
# SofraPOS — Otomatik Doğrulama, Sıfırlama, Yük ve Kaos Testi Komutları
# ==============================================================================

.PHONY: up down verify reset-demo test load chaos

up:
	cp -n .env.example .env 2>/dev/null || true
	docker compose up -d --build --wait

down:
	docker compose down -v

# Bölüm 10.1: Uçtan uca tüm özellik (F-1..F-7) ve zorlu görevleri (T-1..T-5) doğrular
verify:
	docker compose --profile verify run --rm verify

# Bölüm 10.4: Verileri ilk deterministik seed haline döndürür
reset-demo:
	curl -fsS -X POST http://localhost:8080/api/v1/admin/reset-demo || \
		docker compose exec -T backend wget -qO- --post-data="" http://127.0.0.1:8080/api/v1/admin/reset-demo

# Bölüm 9: Birim ve eşzamanlılık testleri (≥ 40 test, domain kapsamı ≥ %70)
test:
	cd backend && go test -v -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Bölüm 10.1: k6 Yük Testi
load:
	docker run --rm -i --network host grafana/k6:latest run - < tests/load/load-test.js

# Bölüm 9: Hata-enjeksiyonu (Kaos) senaryosu
chaos:
	sh scripts/chaos-test.sh
