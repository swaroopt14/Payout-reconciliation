# syntax=docker/dockerfile:1.7
FROM python:3.12-slim AS builder

WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends \
        gcc \
        librdkafka-dev \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

FROM python:3.12-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
        librdkafka1 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --no-create-home --shell /usr/sbin/nologin mluser \
    && mkdir -p /data \
    && chown 10001:10001 /data

WORKDIR /app
COPY --from=builder /install /usr/local
COPY main.py .
COPY app/ app/
COPY healthcheck.py .
COPY scripts/ scripts/
COPY model_artifacts/ model_artifacts/
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && chown 10001:10001 /entrypoint.sh

USER 10001:10001

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

EXPOSE 8091
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=5 \
    CMD python /app/healthcheck.py
ENTRYPOINT ["/entrypoint.sh"]
