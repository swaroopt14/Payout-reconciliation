# syntax=docker/dockerfile:1.7
# Sarama is pure Go — no librdkafka / CGO.
FROM golang:1.25.7-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags "-s -w" -o /out/relay ./cmd

FROM alpine:3.19

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 65532 nonroot \
    && mkdir -p /etc/relay

WORKDIR /app
COPY --from=builder /out/relay /app/relay
COPY --from=builder /src/config.yaml /app/config.yaml
COPY --from=builder /src/config.yaml /etc/relay/config.yaml

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
USER 65532:65532
EXPOSE 8082
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8082/health || exit 1
ENTRYPOINT ["/app/relay"]
