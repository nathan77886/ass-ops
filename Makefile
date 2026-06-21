.PHONY: postgres gateway worker node-worker web build test tool context

postgres:
	docker compose -f deploy/docker-compose.yml up -d postgres

gateway:
	go run ./backend/cmd/gateway

worker:
	go run ./backend/cmd/worker

node-worker:
	go run ./backend/cmd/node-worker

web:
	cd web && pnpm install && pnpm dev

build:
	go build ./...
	cd web && pnpm install && pnpm build

test:
	go test ./...

tool:
	go run ./backend/cmd/assops-tool --help

context:
	go run ./backend/cmd/assops-tool project brief

