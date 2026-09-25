# Edge Microservice

A high-performance API gateway and ingestion service, built with Go and Gin, with observability and tracing.

## Overview

Edge handles request processing, authentication, and routing for ingestion. It runs on port `8080` and integrates with PostgreSQL for data persistence.

## Features

- **RESTful API**: Built with Gin web framework for high performance
- **Database Integration**: PostgreSQL for persistent storage
- **Authentication & Security**: JWT-based auth with encryption support
- **Rate Limiting**: Request throttling and traffic management
- **Distributed Tracing**: OpenTelemetry integration with Jaeger
- **Middleware Support**: Tenant isolation, request logging, and more
- **Health Monitoring**: Built-in health checks and metrics
- **Docker Ready**: Production-ready Docker setup
- **Observability**: Comprehensive monitoring with Prometheus metrics

## Technology Stack

- **Language**: Go 1.25.0
- **Framework**: Gin Gonic
- **Database**: PostgreSQL 16
- **Tracing**: OpenTelemetry + Jaeger
- **Metrics**: Prometheus integration
- **Deployment**: Docker & Docker Compose

## Quick Start

### Local Development

#### Prerequisites
- Go 1.25.0 or higher
- PostgreSQL 16+

#### Setup
```bash
# Install dependencies
go mod download

# Set up environment variables
cp .env.example .env

# Run the application
go run ./cmd/main.go
```

The service will start on `http://localhost:8080`

### Production Deployment

#### Build and Run
```bash
# Build and start with Docker Compose
docker-compose up --build

# Run in background
docker-compose up -d --build

# View logs
docker compose logs -f

# Stop services
docker-compose down
```
The service will start on `http://localhost:8080`

#### Managing Services
```bash
# Stop all containers
docker-compose stop

# Restart services
docker-compose restart

# Remove containers and volumes
docker-compose down -v
```

## API Endpoints

### Public Endpoints
- **Health Check**: `GET /health` or `GET /v1/health` - Service health status
- **Metrics**: `GET /metrics` - Prometheus metrics

### Ingestion Endpoints (API Key or JWT Authenticated)
- **JSON Ingestion**: `POST /v1/ingest` - Submit single intent request (requires idempotency header)
- **Bulk Ingestion**: `POST /v1/bulk-ingest` - Submit CSV/Excel file for per-row ingestion (multipart form upload)

### Admin Endpoints (Admin Token Authenticated)
- **Tenant Registration**: `POST /v1/admin/tenantReg` - Register a new tenant prefix/keys
- **List Tenants**: `GET /v1/admin/tenants` - List all registered tenants
- **Get Tenant by ID**: `GET /v1/admin/tenants/:tenant_id` - Fetch tenant configuration by ID

### Webhook Endpoints
- **Connector Webhooks**: `POST /v1/raw/envelopes/webhooks/:provider/:connectorID` - Raw webhook ingestion (signature verified)

### Public Authentication Endpoints (JWT / Session)
- **Signup**: `POST /v1/auth/signup` - Register first tenant admin user
- **Login**: `POST /v1/auth/login` - Authenticate user credentials and issues tokens
- **Refresh**: `POST /v1/auth/refresh` - Refresh access credentials using refresh token
- **Logout**: `POST /v1/auth/logout` - Revoke current user session refresh token

### Protected Authentication Endpoints (JWT Authenticated)
- **Current User Profile**: `GET /v1/auth/me` - Fetch details of authenticated user
- **Current Principal Detail**: `GET /v1/auth/principal` - Fetch principal identity details (role, email, tenant, etc.)

### Session Management Endpoints (Console JWT Authenticated)
- **Session Status**: `GET /v1/session/status` - Check current console session expiry and idle limits
- **Session Refresh**: `POST /v1/session/refresh` - Validate refresh token and rotate tokens
- **Logout All Sessions**: `POST /v1/session/logout-all` - Terminate all user session tokens

### Internal Outbox Endpoints (Internal usage only)
- **Lease Outbox Records**: `GET /internal/outbox/lease` - Lease pending outbox records
- **Acknowledge Output**: `POST /internal/outbox/ack` - Mark record as successfully published
- **Negative Acknowledge**: `POST /internal/outbox/nack` - Release lease on failed publisher delivery attempts

## Configuration

Environment variables:
```env
DB_HOST=localhost
DB_PORT=5432
DB_USER=edge
DB_PASSWORD=edge
DB_NAME=edge
# Must match this service's docker-compose.yml
DB_SSLMODE=disable
ENVIRONMENT=development
```

## Project Structure

```
/cmd              # Entry point and main application
/config           # Configuration and environment setup
/db               # Database connection and queries
/handler          # HTTP request handlers
/middleware       # Middleware (auth, rate limit, tracing)
/routes           # API route definitions
/services         # Business logic
/security         # Authentication and encryption
/model            # Data models
/dto              # Data transfer objects
/client           # External service clients
```

## Database

### Initialization
The application automatically creates required tables on startup. Currently creates:
- `tenants` table for tenant management

### Accessing Database
```bash
# Connect to PostgreSQL inside Docker container
docker compose exec postgres psql -U "$DB_USER" -d "$DB_NAME"

# View tables
\dt

# View table structure
\d tenants
```

## Development

### Building
```bash
# Local build
go build -o edge ./cmd/main.go

# Docker build
docker-compose build --no-cache
```

### Testing
```bash
go test ./...
```

### Code Quality
```bash
# Format code
go fmt ./...

# Lint code
golangci-lint run
```

## Docker Configuration

### Dockerfile
- **Multi-stage build**: Optimizes final image size
- **Alpine base**: Minimal and secure base image
- **CGO enabled**: For PostgreSQL driver support

### docker-compose.yml
- **Service**: Edge application
- **Database**: PostgreSQL with persistent volume
- **Network**: Isolated compose network for service communication
- **Health checks**: Automatic service monitoring
- **Environment**: Production-ready configuration

## Troubleshooting

### Port Already in Use
```bash
# Change port in docker-compose.yml or use different port
docker-compose up -d -p 8081:8080
```

### Database Connection Errors
```bash
# Check database status
docker-compose logs postgres

# Verify credentials in environment variables
docker compose exec edge env | grep DB_

# For SSL connection issues, ensure DB_SSLMODE is set to 'disable' in docker-compose.yml
# Add to environment section:
# - DB_SSLMODE=disable
```

### Build Failures
```bash
# Clean up and rebuild
docker-compose down -v
docker-compose build --no-cache
docker-compose up
```

## Production Deployment

For production deployment:
1. Update environment variables with production values
2. Use strong database passwords
3. Enable TLS/SSL for API endpoints
4. Configure proper logging and monitoring
5. Set up backup strategy for PostgreSQL volume
6. Use environment-specific configuration files

## Integration

This service integrates with:
- **Vault / token enclave**: For secure journal storage
- **Frontend Console**: Provides APIs for the dashboard
- **PostgreSQL**: Primary data store
- **OpenTelemetry Collector**: For distributed tracing
- **Prometheus**: For metrics collection
- **Jaeger**: For trace visualization

See the main project README for full architecture details.

## Observability & Monitoring

### Distributed Tracing
- **OpenTelemetry Integration**: Automatic request tracing
- **Jaeger Export**: Traces visible at http://localhost:16686
- **Span Creation**: Detailed operation tracking
- **Context Propagation**: Trace context across services

### Metrics Collection
- **Prometheus Metrics**: Available at `/metrics` endpoint
- **Health Checks**: Service status monitoring
- **Performance Metrics**: Request duration, throughput, error rates
- **Custom Business Metrics**: Transaction processing metrics

### Health Monitoring
```bash
# Check service health
curl http://localhost:8080/health

# View Prometheus metrics
curl http://localhost:8080/metrics

# Check traces in Jaeger
# Open http://localhost:16686 and select the edge service
```

### Key Metrics
- `http_requests_total`: Total HTTP requests
- `http_request_duration_seconds`: Request duration histogram
- `database_connections_active`: Active database connections
- `auth_requests_total`: Authentication requests
- `rate_limit_exceeded_total`: Rate limiting events

## Support

For issues or questions, refer to the project documentation or contact the development team.

## Connector secrets at rest (D31, D32)

- `CLEARLINE_SECRETS_KEY` (required): standard base64 of 32 random bytes, e.g.
  `openssl rand -base64 32`. It is used by `internal/secretbox` (AES-256-GCM,
  random nonce, stored as `enc:v1:<base64(nonce||ciphertext)>`) for
  `connectors.secret` (webhook secret) and `connectors.api_secret_ref` (tenant
  Razorpay key secret). If it is missing or invalid, secret writes fail and
  Razorpay webhooks return 503 without being processed.
- Legacy plaintext `connectors.secret` values are rejected on read. Run
  `go run ./cmd/reencrypt-secrets` once per environment (idempotent; prints
  only a count).
- There is no fallback to `RAZORPAY_WEBHOOK_SECRET`. A connector with no own
  secret (or whose `webhook_secret_ref` points at a shared `RAZORPAY_*` var)
  gets 401 for its webhooks.
- Replay protection reuses `provider_webhook_receipts`
  UNIQUE (tenant_id, connector_id, event_id) keyed on `x-razorpay-event-id`. A
  repeated event id returns 200 `duplicate` and is not re-published.
- Set a connector's webhook secret: `PUT /v1/connectors/:connectorID/webhook-secret`
  with `{"webhook_secret": "..."}` (1-256 chars). Auth: Bearer user session
  with role CONNECTOR_ADMIN or PLATFORM_ADMIN (CUSTOMER_ADMIN, tenant API
  keys and PAYOUT_APPROVER get 403; CONNECTOR_ADMIN is granted only via
  `POST /v1/admin/roles/grant`). Every attempt writes an audit event
  (`auth_audit_events`, `CONNECTOR_WEBHOOK_SECRET_SET:<result>:connector=<id>`,
  user id; never the secret). The tenant comes from the token; another tenant's
  connector is 404. Response `{connector_id, webhook_secret_set, updated_at}`
  never echoes the secret; missing `CLEARLINE_SECRETS_KEY` returns 503 and
  writes nothing.

## Internal connector routes (D52)

Service-to-service only; both send `Cache-Control: no-store`, require
`X-Service-Tenant-ID` equal to `:tenant_id` (else 403), are tenant-scoped
(another tenant's connector is 404) and write one `connector_audit` log line +
`auth_audit_events` row per call with ids/mode/caller/result only.

- `GET /internal/v1/tenants/:tenant_id/connectors/:connector_id/razorpay-credentials?mode=test|live`
  (`:connector_id` = `connectors.id` UUID). Auth: `Authorization: Bearer
  $RECON_CREDENTIALS_TOKEN` only — relay's `RELAY_AUTH_TOKEN` gets 401. Returns
  `{key_id, key_secret, mode, version}` for an active razorpay connector whose
  `api_secret_ref` is `enc:v1:`. `env:` refs, legacy plaintext, inactive or
  missing → 404 `NO_TENANT_CREDENTIALS` (tenant must re-enter keys; no platform
  fallback). Missing/invalid `CLEARLINE_SECRETS_KEY` → 503. Token unset or equal
  to `RELAY_AUTH_TOKEN` → 503 (fails closed; logged at startup). `mode=live`
  requires TLS on the request, or `INTERNAL_TLS_TERMINATED_BY_PROXY=true` plus
  `X-Forwarded-Proto: https` from a trusted proxy; otherwise 403
  `LIVE_CREDENTIALS_REQUIRE_TLS`.
- `GET /internal/v1/tenants/:tenant_id/connectors/resolve?provider=razorpay&connector_id=<slug>`
  (slug like `con_razorpay_test_1a2b3c4d`). Auth: `X-Relay-Token` or Bearer
  `RELAY_AUTH_TOKEN`, or Bearer `RECON_CREDENTIALS_TOKEN`. Returns
  `{id, provider, connector_id, mode, active}`; never a secret or ref. Not found
  or inactive → 404 `CONNECTOR_NOT_FOUND`.
