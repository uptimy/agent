VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/uptimy/agent/internal/version.Version=$(VERSION)
BIN := bin/uptimy-agent

.PHONY: run build web go dev-api dev-web test lint docker clean

## run: local development at http://localhost:8080 with hot reload; Ctrl+C stops it
run:
	@./scripts/dev.sh

## build: build the UI and a single self-contained binary
build: web go

web:
	cd web && npm ci && npm run build

go:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/uptimy-agent

## dev-api / dev-web: the two halves of `make run`, if you prefer separate terminals
dev-api:
	DATA_DIR=./data MONITORS_FILE=./examples/dev.yaml UI_DEV_SERVER=http://127.0.0.1:5173 go run ./cmd/uptimy-agent

dev-web:
	cd web && npm run dev

## test: Go tests (set AGENT_TEST_POSTGRES_URL etc. for the database ones) and UI tests
test:
	go vet ./...
	go test -race ./...
	cd web && npm run typecheck && npm test

## lint: golangci-lint (https://golangci-lint.run), ESLint and Prettier, as in CI
lint:
	golangci-lint run ./...
	cd web && npm run lint

docker:
	docker build --build-arg VERSION=$(VERSION) -t ghcr.io/uptimy/agent:$(VERSION) .

clean:
	rm -rf bin data web/node_modules
	find web/dist -mindepth 1 ! -name .gitkeep -delete
