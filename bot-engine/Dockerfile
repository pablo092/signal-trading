# ── Builder ──────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata wget

WORKDIR /build

# Cache dependencies first
COPY go.mod go.sum* ./
RUN go mod download

# Build binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s -extldflags=-static" \
    -o bot-engine ./main.go

# ── Runtime ───────────────────────────────────────────────────────
FROM scratch

# Bring in certs and timezone data from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /build/bot-engine /bot-engine

# wget for healthcheck (use alpine-based image if you prefer)
# The scratch image has no shell — healthcheck is done by Kubernetes probes
# or the docker-compose healthcheck via an external curl/wget

EXPOSE 8080

ENTRYPOINT ["/bot-engine"]
