# WBB developer entry points. Run `make help` for an overview.
FLUTTER ?= $(HOME)/development/flutter/bin/flutter
GOFLAGS ?=
export WBB_TEST_PG_ADMIN_URL ?= postgres://wbb:wbb-dev-password@127.0.0.1:55432/postgres
export WBB_TEST_REDIS_ADDR ?= 127.0.0.1:56379

.PHONY: help deps-up deps-down dev-db-reset test-backend build-backend run-backend \
        frontend-get frontend-analyze frontend-test frontend-build frontend-run ci

help:
	@echo "make deps-up          start dev PostgreSQL + Redis (docker)"
	@echo "make deps-down        stop the dev stack"
	@echo "make dev-db-reset     drop the dev database volume (destructive, dev only)"
	@echo "make test-backend     run Go tests against real PostgreSQL + Redis"
	@echo "make build-backend    build the API server binary"
	@echo "make run-backend      run the API locally (needs deps-up)"
	@echo "make frontend-get     flutter pub get"
	@echo "make frontend-analyze flutter analyze"
	@echo "make frontend-test    flutter test"
	@echo "make frontend-build   flutter build web (output served by api or Caddy)"
	@echo "make frontend-run     flutter run on a local web-server port 3000"
	@echo "make ci               everything CI runs"

deps-up:
	docker compose up -d --wait postgres redis

deps-down:
	docker compose down

dev-db-reset:
	docker compose down -v

test-backend: deps-up
	cd wbb-backend && go test -race -count=1 ./...

vet-backend:
	cd wbb-backend && go vet ./...

build-backend:
	cd wbb-backend && go build -o bin/wbb-api ./cmd/wbb-api

run-backend: deps-up
	cd wbb-backend && WBB_ENV=dev \
		WBB_DATABASE_URL=postgres://wbb:wbb-dev-password@127.0.0.1:55432/wbb \
		WBB_REDIS_ADDR=127.0.0.1:56379 \
		WBB_CORS_ORIGINS=http://localhost:8080,http://localhost:3000 \
		go run ./cmd/wbb-api

frontend-get:
	cd wbb-frontend && $(FLUTTER) pub get

frontend-analyze: frontend-get
	cd wbb-frontend && $(FLUTTER) analyze

frontend-test: frontend-get
	cd wbb-frontend && $(FLUTTER) test

frontend-build: frontend-get
	cd wbb-frontend && $(FLUTTER) build web --release --no-web-resources-cdn

frontend-run: frontend-get
	cd wbb-frontend && $(FLUTTER) run -d web-server --web-port 3000 \
		--dart-define=WBB_API_BASE_URL=http://localhost:8080/api/v1

ci: test-backend vet-backend frontend-analyze frontend-test frontend-build
