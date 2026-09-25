# syntax=docker/dockerfile:1.7
# Test-only fake PSP for the Clearline B12 E2E harness. stdlib only.
FROM golang:1.25.7-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/fake-psp .

FROM alpine:3.19
RUN apk add --no-cache wget && adduser -D -u 65532 nonroot
COPY --from=builder /out/fake-psp /app/fake-psp
USER 65532:65532
EXPOSE 8099
HEALTHCHECK --interval=5s --timeout=3s --retries=10 \
    CMD wget -qO- http://127.0.0.1:8099/health || exit 1
ENTRYPOINT ["/app/fake-psp"]
