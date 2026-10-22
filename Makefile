SERVICES := queue booking payment notification user
MODULES  := pkg $(addprefix services/,$(SERVICES))
LINT_CFG := $(CURDIR)/.golangci.yml
COVERAGE_MIN ?= 80
KEYDIR   := deployments/compose/keys
KEYS     := $(addprefix $(KEYDIR)/,$(addsuffix -token.pem,user queue payment booking))

# Integration test memakai infrastruktur Compose (make infra).
export TEST_POSTGRES_DSN    ?= postgres://rbt:rbt-local-only@localhost:5432/rbt?sslmode=disable
export TEST_POSTGRES_HOST   ?= localhost
export TEST_POSTGRES_PORT   ?= 5432
export TEST_POSTGRES_USER   ?= rbt
export TEST_POSTGRES_PASSWORD ?= rbt-local-only
export TEST_POSTGRES_DATABASE ?= rbt
export TEST_VALKEY_SERVER   ?= [{"host":"localhost","port":6379}]
export TEST_RABBITMQ_URL    ?= amqp://guest:guest@localhost:5672/

.PHONY: help keys infra obs up down logs ps build test test-integration cover lint fmt tidy mock isolation migrate-% run-% worker-% ci

help: ## Daftar target
	@grep -E '^[a-zA-Z_%-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

keys: $(KEYS) ## Buat kunci Ed25519 lokal (user, queue, payment, booking); tidak di-commit
	@chmod 0644 $(KEYS)

$(KEYDIR)/%-token.pem:
	@mkdir -p $(KEYDIR)
	openssl genpkey -algorithm ed25519 -out $@
	openssl pkey -in $@ -pubout -out $(KEYDIR)/$*-token.pub.pem

infra: ## Jalankan PostgreSQL, Valkey, RabbitMQ, Mailpit saja
	docker compose up -d postgres valkey rabbitmq mailpit

obs: ## Jalankan Prometheus, Loki, Alloy, Grafana (profile observability); Grafana di http://localhost:3000
	@mkdir -p logs
	docker compose --profile observability up -d prometheus loki alloy grafana

up: keys ## Jalankan semua (infra + migrasi + service) dengan Docker Compose
	docker compose up -d --build

down: ## Hentikan dan hapus volume lokal (termasuk observability)
	docker compose --profile observability down -v

logs: ## Ikuti log
	docker compose logs -f

ps: ## Status container
	docker compose ps

build: ## go build semua modul
	@for m in $(MODULES); do echo "==> build $$m"; (cd $$m && go build ./...) || exit 1; done

test: ## Unit test -race semua modul
	@for m in $(MODULES); do echo "==> test $$m"; (cd $$m && go test -race -count=1 ./...) || exit 1; done

test-integration: ## Unit + integration test (butuh make infra)
	@for m in $(MODULES); do echo "==> test-integration $$m"; (cd $$m && go test -tags integration -race -count=1 ./...) || exit 1; done

cover: ## Unit + integration test + ambang coverage logika (sama dengan stage case-coverage di CI)
	@for m in $(MODULES); do GO_TEST_TAGS=integration bash tools/check-coverage.sh $$m $(COVERAGE_MIN) || exit 1; done

lint: ## golangci-lint semua modul
	@for m in $(MODULES); do echo "==> lint $$m"; (cd $$m && golangci-lint run --config $(LINT_CFG) ./...) || exit 1; done

fmt: ## gofmt semua file
	gofmt -s -w pkg services

tidy: ## go mod tidy semua modul (jalankan setelah menambah dependency, lalu commit go.mod dan go.sum)
	@for m in $(MODULES); do echo "==> tidy $$m"; (cd $$m && go mod tidy) || exit 1; done

mock: ## Generate mock (go tool mockgen) ke _mock/ tiap service
	@for s in $(SERVICES); do echo "==> mock $$s"; (cd services/$$s && go generate ./internal/...) || exit 1; done

isolation: ## Build tiap service tanpa go.work (bukti bisa dipindah ke repo sendiri)
	@for s in $(SERVICES); do echo "==> isolation $$s"; (cd services/$$s && GOWORK=off go build ./...) || exit 1; done

migrate-%: ## Migrasi satu service ke DB Compose, mis. make migrate-booking (pakai .env service)
	cd services/$* && go run ./cmd migrate up

run-%: ## Jalankan API satu service lokal, mis. make run-booking (pakai .env service)
	cd services/$* && go run ./cmd api

worker-%: ## Jalankan worker satu service lokal di APP_PORT+100 agar tidak bentrok dengan api, mis. make worker-booking
	cd services/$* && APP_PORT=$$(( $$(sed -n 's/^APP_PORT=//p' .env | head -1) + 100 )) go run ./cmd worker

ci: build lint cover isolation ## Pemeriksaan CI lokal (build, lint, test + coverage, isolation)
