# syntax=docker/dockerfile:1.7
#
# Consolidated multi-stage build — Go API + MCP server + Python sidecar.
# Three build stages; one slim runtime image.
#
# Modes (via CMD or docker run arg):
#   api  — Go REST API :3111/:3113  (default in docker-compose api service)
#   mcp  — MCP HTTP server :3114    (default in docker-compose mcp service)
#   py   — Python FastAPI :5000     (default in docker-compose py service)
#   all  — all three via supervisord (standalone single-container mode)

ARG GO_VERSION=1.26.3
ARG PYTHON_VERSION=3.14.5
ARG UV_VERSION=0.5.6

# Named stage so ARG substitution works in COPY --from below.
FROM ghcr.io/astral-sh/uv:${UV_VERSION} AS uv-bin

# ── Stage 1: Go binaries ──────────────────────────────────────────────────────
FROM golang:${GO_VERSION}-bookworm AS go-builder
WORKDIR /src

COPY medha-api/go.mod medha-api/go.sum* ./
RUN go mod download

COPY medha-api/ .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags "-s -w -X github.com/udai-kiran/medha/internal/api.Version=${VERSION}" \
      -o /out/agent-mem-api ./cmd/api && \
    go build -trimpath \
      -ldflags "-s -w -X main.Version=${VERSION}" \
      -o /out/agent-mem-mcp ./cmd/mcp

RUN if [ -d ./cmd/worker ]; then \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
      go build -trimpath -ldflags "-s -w" -o /out/agent-mem-worker ./cmd/worker; \
    fi

# ── Stage 2: Python venv ──────────────────────────────────────────────────────
FROM python:${PYTHON_VERSION}-slim-bookworm AS py-builder

COPY --from=uv-bin /uv /usr/local/bin/uv

# Use /srv/py to match the runtime destination so editable-install .pth paths are correct.
WORKDIR /srv/py

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never \
    VIRTUAL_ENV=/srv/py/.venv \
    PATH="/srv/py/.venv/bin:$PATH"

COPY medha-extraction/pyproject.toml medha-extraction/uv.lock* ./
RUN uv venv /srv/py/.venv && uv sync --no-dev --no-install-project

COPY medha-extraction/ .
RUN uv pip install --no-deps -e .

# ── Stage 3: Runtime ──────────────────────────────────────────────────────────
FROM python:${PYTHON_VERSION}-slim-bookworm AS runtime

RUN apt-get update && \
    apt-get install -y --no-install-recommends supervisor && \
    rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 10001 app && \
    useradd --uid 10001 --gid app --no-create-home app

# Go binaries (statically linked — no Go runtime needed)
COPY --from=go-builder /out/ /app/bin/

# Python venv + source
COPY --from=py-builder /srv/py /srv/py

# Process supervisor config and entrypoint
COPY deploy/supervisord.conf /etc/supervisor/conf.d/medha.conf
COPY deploy/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENV VIRTUAL_ENV=/srv/py/.venv \
    PATH="/srv/py/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PY_PORT=5000

EXPOSE 3111 3113 3114 5000

ENTRYPOINT ["/entrypoint.sh"]
CMD ["all"]
