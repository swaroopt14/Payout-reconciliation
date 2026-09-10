# syntax=docker/dockerfile:1.7
FROM rust:1.88-alpine AS builder
RUN apk add --no-cache musl-dev
WORKDIR /src
COPY Cargo.lock Cargo.toml ./
COPY migrations ./migrations
COPY src ./src
COPY tests ./tests
# Situation tests must pass before the image is produced.
RUN cargo test --release --locked --offline 2>/dev/null || cargo test --release --locked
RUN cargo build --release --locked --offline 2>/dev/null || cargo build --release --locked

FROM alpine:3.19
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 65532 nonroot
WORKDIR /app
COPY --from=builder /src/target/release/zord-router /app/zord-router
COPY --from=builder /src/migrations /app/migrations
USER 65532:65532
EXPOSE 8091
ENV PORT=8091
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8091/v1/health || exit 1
ENTRYPOINT ["/app/zord-router"]
