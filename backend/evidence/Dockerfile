# syntax=docker/dockerfile:1.7
FROM golang:1.25.7-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w" -o /out/zord-evidence ./cmd/main.go

FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 65532 nonroot

WORKDIR /app
COPY --from=builder /out/zord-evidence /app/zord-evidence
COPY --from=builder /src/db/migrations /app/db/migrations

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 8088
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8088/healthz || exit 1
ENTRYPOINT ["/app/zord-evidence"]
