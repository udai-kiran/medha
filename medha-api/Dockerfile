# syntax=docker/dockerfile:1.7
#
# Two-stage build for the agent_mem Go service.
# Final image is `gcr.io/distroless/static-debian12:nonroot` so the binary
# runs as a non-root user with no shell / package manager attached.

ARG GO_VERSION=1.26.3

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src

# Cache modules separately so source edits don't bust the dep layer.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X github.com/oneconvergence/agent-mem/internal/api.Version=${VERSION}" \
    -o /out/agent-mem-api ./cmd/api

# Worker binary appears in Task 12; build conditionally so the M0 image still works.
RUN if [ -d ./cmd/worker ]; then \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
      go build -trimpath -ldflags "-s -w" -o /out/agent-mem-worker ./cmd/worker; \
    fi

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/agent-mem-api /app/agent-mem-api
# Worker is optional — Compose's `worker` service overrides the entrypoint.
COPY --from=build /out/ /app/

USER nonroot:nonroot
EXPOSE 3111 3113
ENTRYPOINT ["/app/agent-mem-api"]
