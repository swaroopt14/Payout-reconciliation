# syntax=docker/dockerfile:1.7
FROM golang:1.25.7-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w" -o /out/main ./cmd/main.go

FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget postgresql-client \
    && adduser -D -u 65532 nonroot

WORKDIR /app
COPY --from=builder /out/main /app/main
COPY --from=builder /src/db/migrations /app/db/migrations

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8081/v1/health || exit 1
ENTRYPOINT ["/app/main"]
