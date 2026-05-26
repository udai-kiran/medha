# medha — top-level Makefile
# Wraps Go + Python build/test/lint and Docker Compose bring-up.

.PHONY: help setup build test lint run-go run-py worker compose-up compose-down compose-light clean

GO_DIR := medha-api
PY_DIR := medha-extraction

help:
	@echo "medha make targets:"
	@echo "  setup        Install Go modules and sync Python deps (via uv)"
	@echo "  build        Build Go binaries"
	@echo "  test         Run Go + Python tests"
	@echo "  lint         Run golangci-lint + ruff + mypy"
	@echo "  run-go       Run the Go API service locally"
	@echo "  run-py       Run the Python FastAPI service locally"
	@echo "  worker       Run the Go async worker"
	@echo "  compose-up   docker compose up (full stack incl. Neo4j + RabbitMQ)"
	@echo "  compose-light  docker compose up with lightweight profile (no Neo4j)"
	@echo "  compose-down docker compose down"
	@echo "  clean        Remove build artifacts"

setup:
	cd $(GO_DIR) && go mod download
	cd $(PY_DIR) && uv sync --all-extras || (echo "uv not installed — see README"; exit 1)

build:
	cd $(GO_DIR) && go build -o bin/agent-mem-api ./cmd/api
	# worker binary appears in Task 12
	@if [ -d "$(GO_DIR)/cmd/worker" ]; then cd $(GO_DIR) && go build -o bin/agent-mem-worker ./cmd/worker; fi

test:
	cd $(GO_DIR) && go test ./... -race -cover
	cd $(PY_DIR) && uv run pytest --cov

lint:
	cd $(GO_DIR) && golangci-lint run ./...
	cd $(PY_DIR) && uv run ruff check .
	cd $(PY_DIR) && uv run mypy medha

run-go:
	cd $(GO_DIR) && go run ./cmd/api

run-py:
	cd $(PY_DIR) && uv run uvicorn medha.api:app --host 0.0.0.0 --port $${PY_PORT:-5000} --reload

worker:
	cd $(GO_DIR) && go run ./cmd/worker

compose-up:
	docker compose up --build

compose-light:
	docker compose -f docker-compose.yml -f deploy/docker-compose.lightweight.yml up --build

compose-down:
	docker compose down
	docker compose -f docker-compose.yml -f deploy/docker-compose.lightweight.yml down 2>/dev/null || true

clean:
	rm -rf $(GO_DIR)/bin $(GO_DIR)/coverage.out $(GO_DIR)/coverage.html
	find $(PY_DIR) -type d -name '__pycache__' -prune -exec rm -rf {} +
	find $(PY_DIR) -type d -name '.pytest_cache' -prune -exec rm -rf {} +
	find $(PY_DIR) -type d -name '.mypy_cache' -prune -exec rm -rf {} +
	find $(PY_DIR) -type d -name '.ruff_cache' -prune -exec rm -rf {} +
