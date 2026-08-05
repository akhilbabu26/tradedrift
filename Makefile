.PHONY: proto lint test up down logs build

proto:
	$(MAKE) -C platform/api

lint:
	golangci-lint run ./...

test:
	go test ./...

# ─── Docker ───────────────────────────────────────────────────────────────────

up:
	docker compose up --build -d

down:
	docker compose down

rebuild:
	docker compose down && docker compose up --build -d

logs:
	docker compose logs -f

build:
	docker compose build
