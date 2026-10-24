# ==============================================================================
# Stage 1: Builder (Compiles API, Simulator, and Verify CLI)
# ==============================================================================
FROM golang:1.23-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/ddo-api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/verify ./cmd/verify

# ==============================================================================
# Stage 2: Minimal Runtime (Non-Root)
# ==============================================================================
FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 -S appgroup && \
    adduser -u 10001 -S appuser -G appgroup -h /app -s /sbin/nologin

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /out/ddo-api /app/ddo-api
COPY --from=builder --chown=appuser:appgroup /out/simulator /app/simulator
COPY --from=builder --chown=appuser:appgroup /out/verify /app/verify

USER appuser:appgroup

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/ddo-api"]
