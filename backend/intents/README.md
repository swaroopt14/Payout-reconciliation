# Intent Engine Microservice

A high-performance intent processing and validation service for the platform, built with Go and designed for real-time financial transaction processing with comprehensive validation and canonicalization.

## 🚀 **Current Deployment Status**

✅ **Service Successfully Deployed & Running**
- **Container**: `intent-engine` - Running and healthy
- **Database**: `intent-postgres` - Running on port 5436
- **Network**: `compose network` - Active
- **Health Check**: Responding at `http://localhost:8083/health`
- **Redis Integration**: Connected to Redis message queues
- **OpenTelemetry**: Distributed tracing enabled

### Current Service Status
```bash
# Check running services
docker ps | grep intent
# intent-engine - Up and healthy
# intent-postgres - Up and running

# Test health endpoint
curl http://localhost:8083/health
# Should return: 200 OK
```

## Overview

The Intent Engine service handles intent validation, canonicalization, and processing for financial transactions. It runs on port `8083` and integrates with PostgreSQL for data persistence, Redis for message processing, and provides comprehensive business rule validation with idempotency guarantees.

## Features

- **🔍 Intent Validation**: Comprehensive schema and semantic validation
- **📋 Canonicalization**: Standardization of intent data formats
- **🔄 Idempotency**: Duplicate detection and prevention
- **📨 Redis Message Processing**: Consumes intents from Redis queues
- **🗄️ PostgreSQL Storage**: Reliable data persistence
- **🚨 Dead Letter Queue (DLQ)**: Failed message handling
- **📊 Business Rules Engine**: Configurable validation rules
- **🔍 OpenTelemetry Tracing**: Distributed request tracking
- **🐳 Docker Ready**: Production-ready containerized deployment
- **🏥 Health Monitoring**: Built-in health checks and monitoring

## Technology Stack

- **Language**: Go 1.22+ (Docker: Alpine-based)
- **Database**: PostgreSQL 16-alpine
- **Message Queue**: Redis 6379 (for inter-service communication)
- **Validation**: JSON Schema + Custom business rules
- **Tracing**: OpenTelemetry + Jaeger integration
- **Deployment**: Docker & Docker Compose (✅ Currently Deployed)
- **Container**: Multi-stage build with Alpine Linux
- **Architecture**: Event-driven microservice with message queues

## Quick Start

### Local Development

#### Prerequisites
- Go 1.24.1 or higher
- PostgreSQL 16+
- Redis (for message queues)

#### Setup
```bash
# Install dependencies
go mod download

# Set up environment variables
cp .env.example .env

# Initialize database
psql -U intent_user -d intent_engine_db < db/migration.sql

# Run the application
go run ./cmd/main.go
```

The service will start on `http://localhost:8083`

### Docker Deployment ✅ **Currently Running**

#### Build and Run
```bash
# Build and start with Docker Compose (ALREADY DEPLOYED)
docker-compose up --build

# Run in background (CURRENTLY ACTIVE)
docker-compose up -d --build

# View logs
docker-compose logs -f intent-engine

# Check status
docker ps | grep intent

# Stop services
docker-compose down
```

#### Current Service Status
```bash
# Check running containers
docker ps | grep intent-engine
# Should show: Up X minutes (healthy)

# Test health endpoint
curl http://localhost:8083/health
# Should return: 200 OK

# Check database connection
docker-compose exec intent-postgres pg_isready
# Should return: accepting connections
```

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

### Health & Status
- **Health Check**: `GET /health` - Service health status ✅ **Active**
- **Service Status**: `GET /v1/status` - Detailed service information

### Intent Processing (Redis-based)
- **Intent Processing**: Automatic consumption from Redis queues
- **Validation**: Schema and business rule validation
- **Canonicalization**: Intent data standardization
- **DLQ Handling**: Failed message processing and retry logic

### Testing Endpoints
```bash
# Test health endpoint (currently working)
curl http://localhost:8083/health

# Test with Docker network
docker-compose exec intent-engine wget -qO- http://localhost:8083/health

# Check Redis message processing
docker exec intent-redis redis-cli LLEN Intent_Processing
docker exec intent-redis redis-cli LLEN Intent_Validated
```

## Configuration ✅ **Currently Applied**

Environment variables (currently active in Docker):
```env
# Database Configuration (Active)
DB_HOST=postgres
DB_PORT=5432
DB_USER=intent_user
DB_PASSWORD=intent_password
DB_NAME=intent_engine_db

# Service Configuration (Active)
ENVIRONMENT=production
SERVICE_PORT=8083
EXTERNAL_PORT=8083 # Mapped to localhost:8083

# Redis Configuration (Active)
REDIS_HOST=redis
REDIS_PORT=6379
REDIS_DB=0

# Processing Configuration (Active)
BATCH_SIZE=10
POLL_INTERVAL=5s
MAX_RETRIES=3
DLQ_ENABLED=true

# OpenTelemetry Configuration (Active)
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
JAEGER_ENDPOINT=http://jaeger:14268/api/traces
```

### Accessing Current Configuration
```bash
# View current environment variables
docker-compose exec intent-engine env | grep -E "(DB_|REDIS_|OTEL_)"

# Check mounted volumes
docker inspect intent-engine | grep -A 10 "Mounts"

# Check Redis connectivity
docker-compose exec intent-engine redis-cli -h redis ping
```

## Project Structure

```
/cmd # Entry point and main application
/config # Configuration and environment setup
/db # Database connection and migrations
/internal/
 ├── canonicalizer/ # Intent canonicalization logic
 ├── fetcher/ # Data fetching and retrieval
 ├── guards/ # Pre-processing guards and checks
 ├── handlers/ # HTTP request handlers (DLQ management)
 ├── idempotency/ # Idempotency checking and management
 ├── models/ # Data models and structures
 ├── persistence/ # Database repositories
 ├── pii/ # PII tokenization integration
 ├── services/ # Business logic and processing services
 ├── validator/ # Schema and business rule validation
 └── worker/ # Background processing workers
/pkg/
 └── hash/ # Hashing utilities for idempotency
```

## Message Processing Architecture

### Redis Queue Integration
```
vault-journal → Redis (Intent_Processing) → intent-engine → Validation & Canonicalization
 ↓
 Redis (Intent_Validated) → Downstream Services
 ↓
 Redis (Intent_DLQ) ← Failed Messages
```

### Processing Flow
1. **Consume**: Listen to `Intent_Processing` Redis queue (BRPOP)
2. **Validate**: Schema validation and business rule checking
3. **Canonicalize**: Standardize intent data format
4. **Idempotency**: Check for duplicate processing
5. **Persist**: Save canonical intent and processing metadata
6. **Publish**: Send validated intent to `Intent_Validated` queue (LPUSH)
7. **DLQ**: Handle failed messages in Dead Letter Queue

### Data Structures
```go
// Incoming intent from Redis
type IncomingIntent struct {
 TenantID string `json:"tenant_id"`
 TraceID string `json:"trace_id"`
 IntentType string `json:"intent_type"`
 RawPayload map[string]interface{} `json:"raw_payload"`
 IdempotencyKey string `json:"idempotency_key"`
 ReceivedAt time.Time `json:"received_at"`
}

// Canonical intent output
type CanonicalIntent struct {
 ID string `json:"id"`
 TenantID string `json:"tenant_id"`
 IntentType string `json:"intent_type"`
 CanonicalData map[string]interface{} `json:"canonical_data"`
 ValidationHash string `json:"validation_hash"`
 ProcessedAt time.Time `json:"processed_at"`
}

// DLQ entry for failed processing
type DLQEntry struct {
 ID string `json:"id"`
 OriginalData string `json:"original_data"`
 ErrorReason string `json:"error_reason"`
 RetryCount int `json:"retry_count"`
 CreatedAt time.Time `json:"created_at"`
}
```

## Database Schema

### Tables
- **canonical_intents**: Processed and validated intents
- **ingress_envelopes**: Raw intent metadata
- **payment_intents**: Payment-specific intent data
- **dlq_entries**: Dead letter queue for failed processing
- **idempotency_records**: Duplicate detection records

### Initialization
The application automatically creates required tables on startup:

```bash
# View migrations
cat db/migration.sql

# Run migrations manually
psql -U intent_user -d intent_engine_db < db/migration.sql
```

### Accessing Database ✅ **Currently Available**
```bash
# Connect to running PostgreSQL container
docker-compose exec postgres psql -U intent_user -d intent_engine_db

# Alternative connection (external port)
psql -h localhost -p 5436 -U intent_user -d intent_engine_db

# Check database status
docker-compose exec postgres pg_isready -U intent_user

# View database logs
docker-compose logs postgres
```

## Validation Engine

### Schema Validation
- **JSON Schema**: Structural validation of intent data
- **Type Checking**: Data type validation and coercion
- **Required Fields**: Mandatory field validation
- **Format Validation**: Email, phone, currency format checks

### Business Rules Validation
- **Amount Limits**: Transaction amount validation
- **Currency Support**: Supported currency validation
- **Tenant Rules**: Tenant-specific business rules
- **Compliance Checks**: Regulatory compliance validation

### Validation Configuration
```go
// Example validation rules
type ValidationRules struct {
 MaxAmount decimal.Decimal `json:"max_amount"`
 MinAmount decimal.Decimal `json:"min_amount"`
 AllowedCurrencies []string `json:"allowed_currencies"`
 RequiredFields []string `json:"required_fields"`
 TenantRules map[string]interface{} `json:"tenant_rules"`
}
```

## Canonicalization Process

### Data Standardization
- **Field Mapping**: Map raw fields to canonical schema
- **Data Normalization**: Standardize formats and values
- **Currency Conversion**: Normalize currency representations
- **Date/Time Formatting**: ISO 8601 standardization

### Canonical Schema
```go
type CanonicalPayoutIntent struct {
 IntentID string `json:"intent_id"`
 TenantID string `json:"tenant_id"`
 Amount decimal.Decimal `json:"amount"`
 Currency string `json:"currency"`
 Recipient Recipient `json:"recipient"`
 Metadata map[string]interface{} `json:"metadata"`
 CreatedAt time.Time `json:"created_at"`
}
```

## Idempotency Management

### Duplicate Detection
- **Hash-based Keys**: SHA-256 hashing of intent content
- **Tenant Isolation**: Per-tenant idempotency scoping
- **Time Windows**: Configurable idempotency windows
- **Status Tracking**: Processing status management

### Implementation
```go
type IdempotencyRecord struct {
 ID string `json:"id"`
 TenantID string `json:"tenant_id"`
 Hash string `json:"hash"`
 Status string `json:"status"` // pending, processed, failed
 ResultData string `json:"result_data"`
 CreatedAt time.Time `json:"created_at"`
 ExpiresAt time.Time `json:"expires_at"`
}
```

## Development

### Building
```bash
# Local build
go build -o intent-engine ./cmd/main.go

# Docker build
docker-compose build --no-cache
```

### Testing
```bash
# Run unit tests
go test ./...

# Run integration tests
go test -tags=integration ./...

# Test with coverage
go test -cover ./...
```

### Code Quality
```bash
# Format code
go fmt ./...

# Lint code
golangci-lint run

# Security scanning
gosec ./...
```

## Docker Configuration

### Dockerfile
- **Multi-stage build**: Optimizes final image size
- **Alpine base**: Minimal and secure base image
- **CGO enabled**: For PostgreSQL driver support
- **Health checks**: Built-in health monitoring

### docker-compose.yml
- **Service**: Intent Engine application (port 8083)
- **Database**: PostgreSQL with persistent volume
- **Redis**: Message queue integration
- **Network**: Isolated `compose-network` for service communication
- **Health checks**: Automatic service monitoring
- **OpenTelemetry**: Tracing configuration

## Troubleshooting

### Common Issues

#### Redis Connection Issues
```bash
# Check Redis connectivity
docker exec intent-redis redis-cli ping
# Should return: PONG

# Check if service can reach Redis
docker-compose exec intent-engine ping redis

# Monitor Redis queues
docker exec intent-redis redis-cli MONITOR
```

#### Message Processing Issues
```bash
# Check queue lengths
docker exec intent-redis redis-cli LLEN Intent_Processing
docker exec intent-redis redis-cli LLEN Intent_Validated
docker exec intent-redis redis-cli LLEN Intent_DLQ

# View messages in queue (without removing)
docker exec intent-redis redis-cli LRANGE Intent_Processing 0 -1

# Check service logs for processing errors
docker logs intent-engine --tail 50
```

#### Database Connection Errors
```bash
# Check database status
docker-compose logs postgres

# Verify credentials and connection
docker-compose exec intent-engine env | grep DB_

# Test database connection
docker-compose exec postgres psql -U intent_user -d intent_engine_db -c "SELECT version();"
```

#### Validation Failures
```bash
# Check validation logs
docker logs intent-engine | grep -i validation

# View DLQ entries
docker-compose exec postgres psql -U intent_user -d intent_engine_db -c "SELECT * FROM dlq_entries ORDER BY created_at DESC LIMIT 10;"

# Check validation rules configuration
docker-compose exec intent-engine env | grep -i validation
```

#### Port Already in Use
```bash
# Change port in docker-compose.yml or use different port
docker-compose up -d -p 8084:8083
```

#### Build Failures
```bash
# Clean up and rebuild
docker-compose down -v
docker-compose build --no-cache
docker-compose up
```

## Performance Optimization

### Processing Optimization
- **Batch Processing**: Configurable batch sizes for queue processing
- **Connection Pooling**: PostgreSQL connection management
- **Redis Pipelining**: Efficient queue operations
- **Async Processing**: Non-blocking message consumption
- **Caching**: Validation rule and schema caching

### Database Optimization
- **Indexing**: Optimized database queries with proper indexing
- **Partitioning**: Table partitioning for large datasets
- **Query Optimization**: Efficient SQL queries and prepared statements

## Monitoring & Observability

### OpenTelemetry Integration ✅ **Active**
- **Distributed Tracing**: End-to-end request tracking
- **Span Creation**: Detailed operation tracking
- **Context Propagation**: Trace context across services
- **Jaeger Export**: Trace visualization in Jaeger UI

### Key Metrics to Track
- **Processing Rate**: Intents processed per second
- **Validation Success Rate**: Percentage of successful validations
- **Queue Lengths**: Redis queue depth monitoring
- **Database Performance**: Query execution times
- **Error Rates**: Validation and processing error rates
- **DLQ Size**: Dead letter queue growth monitoring

### Health Monitoring
```bash
# Service health
curl http://localhost:8083/health

# Container health
docker inspect intent-engine | grep -A 5 "Health"

# Resource usage
docker stats intent-engine
```

## Integration ✅ **Active Connections**

This service integrates with:
- **vault-journal**: ✅ Receives intents via Redis `Intent_Processing` queue
- **Redis Message Queues**: ✅ Connected to Redis on port 6379
 - Consumes from: `Intent_Processing` queue
 - Publishes to: `Intent_Validated` queue
 - DLQ: `Intent_DLQ` queue for failed messages
- **PostgreSQL**: ✅ Connected and running (internal port 5432, external 5436)
 - Tables: `canonical_intents`, `dlq_entries`, `idempotency_records`
- **OpenTelemetry Collector**: ✅ Sending traces to OTEL collector
- **Jaeger**: ✅ Distributed tracing visualization
- **Docker Network**: ✅ Connected to `compose network`

### Current Service Mesh
```
vault-journal → Redis (Intent_Processing) → intent-engine (8083)
 ↓
 Validation & Canonicalization
 ↓
 Redis (Intent_Validated) → Downstream Services
 ↓
 PostgreSQL (Persistence)
```

### Message Flow Monitoring
```bash
# Monitor Redis queues
docker exec intent-redis redis-cli LLEN Intent_Processing
docker exec intent-redis redis-cli LLEN Intent_Validated
docker exec intent-redis redis-cli LLEN Intent_DLQ

# Check service connectivity
curl http://localhost:8083/health

# View service logs
docker logs intent-engine

# Check database records
docker-compose exec postgres psql -U intent_user -d intent_engine_db -c "SELECT COUNT(*) FROM canonical_intents;"
```

## Production Deployment

For production deployment:

1. **Security**:
 - Use strong database passwords (minimum 16 characters)
 - Enable database encryption at rest
 - Implement proper network segmentation
 - Use environment-specific configurations

2. **Performance**:
 - Configure appropriate batch sizes and poll intervals
 - Set up database connection pooling
 - Implement Redis clustering for high availability
 - Monitor and tune validation rule performance

3. **Monitoring**:
 - Set up centralized logging with structured logs
 - Configure alerts for processing failures and DLQ growth
 - Monitor validation success rates and processing latency
 - Track business metrics and SLA compliance

4. **Reliability**:
 - Implement circuit breakers for external dependencies
 - Configure proper retry policies and backoff strategies
 - Set up automated failover and recovery procedures
 - Ensure proper backup and disaster recovery plans

## Support

For issues or questions:
1. Check service logs: `docker logs intent-engine`
2. Verify Redis connectivity and queue status
3. Check database connection and table structure
4. Review validation rules and schema configuration
5. Monitor OpenTelemetry traces in Jaeger UI

The service is designed for high reliability with comprehensive error handling, automatic recovery mechanisms, and detailed observability for production monitoring.

## License

MIT License. See the repository LICENSE.

---

**🎯 Built for high-performance intent processing with comprehensive validation, canonicalization, and observability capabilities.**