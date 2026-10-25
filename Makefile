.PHONY: up down verify reset-demo demo-live test load chaos explain

up:
	cp -n .env.example .env 2>/dev/null || true
	docker compose up -d --build --wait

down:
	docker compose down

verify:
	docker compose --profile verify run --rm verify

# Leave the simulator stopped so the initial seed stays unchanged.
reset-demo:
	docker compose stop simulator
	docker compose --profile verify run --rm verify --reset-only

demo-live:
	docker compose up -d simulator

test:
	cd backend && test -z "$$(gofmt -l .)" && go vet ./... && go test -json -race -coverprofile=coverage.out ./... > test-results.jsonl && python3 scripts/check_quality.py && go tool cover -func=coverage.out

load:
	sh scripts/load-test.sh

chaos:
	sh scripts/chaos-test.sh

explain:
	docker compose exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"' < tests/sql/explain.sql
