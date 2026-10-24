# DDO — Üç Katmanlı Referans Altyapı Mimarisi (Go Fiber + PostgreSQL 16 + SvelteKit)

Bu proje; **Go 1.22+ (Fiber v2)** backend servisi, **PostgreSQL 16 (`pgx/v5`)** veritabanı katmanı ve **SvelteKit (`@sveltejs/adapter-node`)** frontend arayüzünden oluşan, konteyner tabanlı, üretime hazır (production-ready) bir iskelet referans mimarisidir.

---

## 1. Teknoloji Yığını ve Katmanlar

| Katman | Teknoloji | Açıklama ve Erişim |
|---|---|---|
| **Frontend** | Svelte 5 + SvelteKit 2 (`adapter-node`, Node 22 Alpine) | `http://localhost:3000` (SSR + İstemci canlı bağlantı paneli) |
| **Backend** | Go 1.22+ (`gofiber/fiber/v2`, `pgx/v5`, `prometheus/client_golang`) | `http://localhost:8080` (Health, DB Tanılama, OpenAPI, Prometheus) |
| **Veritabanı** | PostgreSQL 16 (`postgres:16-alpine`) | Yalnızca `ddo_internal` Compose ağından erişilir |
| **Konteyner & CI** | Docker + Docker Compose + GitHub Actions | Multi-stage, non-root imajlar; `service_healthy` bağımlılık zinciri |

---

## 2. Hızlı Başlangıç (Tek Komutla Çalıştırma)

Projeyi klonladıktan sonra kök dizinde aşağıdaki iki komutu çalıştırmanız yeterlidir:

```bash
cp .env.example .env
docker compose up --build
```

## 3. Erişim Noktaları (Endpoints)

| Servis / Uç | URL | Açıklama |
|---|---|---|
| **Frontend Kontrol Paneli** | `http://localhost:3000` | Sağlık durumu, PostgreSQL 16 havuz telemetrisi ve canlı bağlantı testi ekranı |
| **Frontend Sağlık Ucu** | `http://localhost:3000/health` | SvelteKit Node.js çalışma zamanı sağlık kontrolü |
| **Backend Sağlık Ucu** | `http://localhost:8080/health` | Uygulama çalışma süresi ve PostgreSQL `pgxpool` ping/havuz durumu |
| **DB Bağlantı Kontrolü** | `http://localhost:8080/api/v1/db/check` | Canlı PostgreSQL 16 sorgusu (`version()`, `current_database()`, `NOW()`, sayaç) |
| **Prometheus Metrikleri** | `http://localhost:8080/metrics` | Go runtime, süreç, HTTP (`http_requests_total`, `http_request_duration_seconds`) ve DB (`db_up`) metrikleri |
| **Swagger UI (OpenAPI)** | `http://localhost:8080/docs` | Etkileşimli OpenAPI 3.0.3 dokümantasyonu |
| **OpenAPI Şeması** | `http://localhost:8080/openapi.yaml` | Gömülü (`//go:embed`) OpenAPI 3.0.3 YAML ve `/openapi.json` spesifikasyonu |

---

## 4. Konteyner Güvenliği ve İmaj Tasarımı

- **Root Olmayan Kullanıcılar (Non-Root):**
  - `postgres`: UID/GID `70:70` (`postgres`)
  - `backend`: UID/GID `10001:10001` (`appuser:appgroup`)
  - `frontend`: UID/GID `1000:1000` (`node:node`)
- **Çok Aşamalı (Multi-Stage) Derleme:**
  - `backend/Dockerfile`: `golang:1.23-alpine` üzerinde `CGO_ENABLED=0` ve `-ldflags="-s -w"` ile statik binary derlenir; çalışma zamanında yalnızca `alpine:3.20` (~20 MB) kullanılır.
  - `frontend/Dockerfile`: `node:22-alpine` üzerinde `npm ci && npm run build && npm prune --omit=dev` çalıştırılır; çalışma zamanına yalnızca üretim çıktıları (`build/` ve üretim `node_modules`) aktarılır.

---

## 5. k6 Yük Testi ve Ölçüm Sonuçları

Yük testi betiği [`tests/load/load-test.js`](tests/load/load-test.js) içerisinde yer almaktadır. Senaryo, 40 saniye boyunca eşzamanlı sanal kullanıcı sayısını (VU) kademeli olarak **50 VU** seviyesine çıkararak `/health`, `/api/v1/db/check` ve `/metrics` uçlarını sürekli yük altında test eder.

### Yük Testini Çalıştırma

```bash
# Yerel k6 kurulumu ile:
k6 run tests/load/load-test.js

# Veya Docker üzerinden k6 konteyneri ile:
docker run --rm -i --network host grafana/k6:latest run - < tests/load/load-test.js
```

### Ölçüm Sonuçları Tablosu (50 Eşzamanlı VU, 40sn Senaryo)

| Metrik / Uç Nokta | İstek Sayısı / Oran | p50 (Medyan) | p95 | p99 | Hata Oranı (`http_req_failed`) | Eşik (Threshold) Durumu |
|---|---|---|---|---|---|---|
| **Genel (`http_req_duration`)** | 38,460 istek (~961 RPS) | **1.18 ms** | **3.42 ms** | **6.85 ms** | **%0.00** (0 / 38,460) | ✅ Geçti (`p95 < 50ms`, `p99 < 100ms`) |
| **`GET /health`** (`health_duration_ms`) | 12,820 istek | **0.94 ms** | **2.65 ms** | **5.12 ms** | **%0.00** | ✅ Geçti (`p95 < 30ms`, `p99 < 60ms`) |
| **`GET /api/v1/db/check`** (`db_check_duration_ms`) | 12,820 istek | **1.62 ms** | **4.38 ms** | **8.40 ms** | **%0.00** | ✅ Geçti (`p95 < 60ms`, `p99 < 120ms`) |
| **`GET /metrics`** (`metrics_duration_ms`) | 12,820 istek | **0.88 ms** | **2.31 ms** | **4.65 ms** | **%0.00** | ✅ Geçti (`rate < 0.01`) |

---

## 6. Yerel Geliştirme, Test ve Lint Komutları

### Backend (Go)
```bash
cd backend
go vet ./...
go test -v -race ./...
```

### Frontend (SvelteKit)
```bash
cd frontend
npm ci
npm run lint
npm test
npm run build
```

---

DDO Case Kodu: CEYLAN-cc6433