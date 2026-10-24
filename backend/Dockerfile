# ==============================================================================
# Stage 1: Builder
# ==============================================================================
FROM golang:1.23-alpine AS builder

WORKDIR /src

# Güvenlik ve TLS sertifikaları için ca-certificates
RUN apk add --no-cache ca-certificates tzdata

# Önce bağımlılıkları önbelleğe al
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Kaynak kodunu kopyala ve statik binary olarak derle
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/ddo-api ./cmd/api

# ==============================================================================
# Stage 2: Minimal Runtime (Non-Root)
# ==============================================================================
FROM alpine:3.20 AS runtime

# Healthcheck için wget (busybox içinde mevcut) ve TLS sertifikaları
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 10001 -S appgroup && \
    adduser -u 10001 -S appuser -G appgroup -h /app -s /sbin/nologin

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /out/ddo-api /app/ddo-api

# Root olmayan kullanıcıya geç
USER appuser:appgroup

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
    CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/ddo-api"]

