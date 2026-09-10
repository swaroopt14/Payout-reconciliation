# syntax=docker/dockerfile:1.7
FROM node:20-alpine

WORKDIR /app
RUN apk add --no-cache wget

COPY package.json package-lock.json* ./
RUN npm ci --omit=dev

COPY src ./src

ENV PORT=8099
EXPOSE 8099
HEALTHCHECK --interval=15s --timeout=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8099/healthz || exit 1
CMD ["node", "src/server.js"]
