SERVICES := queue booking payment notification
MODULES  := pkg $(addprefix services/,$(SERVICES))
LINT_CFG := $(CURDIR)/.golangci.yml
COVERAGE_MIN ?= 80

.PHONY: help up down logs ps build test cover lint fmt tidy mock isolation run-% ci

help: ## Daftar target
	@grep -E '^[a-zA-Z_%-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

up: ## Jalankan semua (infra + service) dengan Docker Compose
	docker compose up -d --build

down: ## Hentikan dan hapus volume lokal
	docker compose down -v

logs: ## Ikuti log
	docker compose logs -f

ps: ## Status container
	docker compose ps

build: ## go build semua modul
	@for m in $(MODULES); do echo "==> build $$m"; (cd $$m && go build ./...) || exit 1; done

test: ## go test -race semua modul
	@for m in $(MODULES); do echo "==> test $$m"; (cd $$m && go test -race -count=1 ./...) || exit 1; done

cover: ## Unit test + ambang coverage logika (sama dengan stage case-coverage di CI)
	@for m in $(MODULES); do bash tools/check-coverage.sh $$m $(COVERAGE_MIN) || exit 1; done

lint: ## golangci-lint semua modul
	@for m in $(MODULES); do echo "==> lint $$m"; (cd $$m && golangci-lint run --config $(LINT_CFG) ./...) || exit 1; done

fmt: ## gofmt semua file
	gofmt -s -w pkg services

tidy: ## go mod tidy semua modul
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

mock: ## Generate mock (mockgen) ke _mock/ tiap service
	@for s in $(SERVICES); do echo "==> mock $$s"; (cd services/$$s && go generate ./internal/... && go mod tidy) || exit 1; done

isolation: ## Build tiap service tanpa go.work (bukti bisa dipindah ke repo sendiri)
	@for s in $(SERVICES); do echo "==> isolation $$s"; (cd services/$$s && GOWORK=off go build ./...) || exit 1; done

run-%: ## Jalankan satu service lokal, mis. make run-booking
	cd services/$* && go run ./cmd

ci: build lint cover isolation ## Pemeriksaan CI lokal (build, lint, test + coverage, isolation)
