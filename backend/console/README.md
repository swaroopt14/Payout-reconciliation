# Console — Multi-Tenant Ingestion Dashboard

A Next.js 14 application providing role-based dashboards for payout reconciliation and transaction ingestion.

## 🚀 Quick Start

### Docker Deployment (Recommended)

```bash
# Build and run with Docker
docker-compose up -d --build

# Check status
docker-compose ps

# View logs
docker compose logs -f

# Stop
docker-compose down
```

### Local Development

```bash
# Install dependencies
npm install

# Run development server
npm run dev

# Build for production
npm run build

# Start production server
npm start
```

Cash/settlement routes return 503 unless `.env.local` sets `ZORD_SETTLEMENT_URL=http://localhost:8081` (live recon) or the demo pair `SMOKE_SIMULATOR_URL=http://localhost:8099` + `CLEARLINE_DEMO_SETTLEMENT_SIMULATOR=1` (see `.env.example`, alongside `env.docker-compose.example`).
The demo pair serves fixture data labelled demo (`X-Clearline-Data: demo`) and must never be used for a live tenant.

## 🌐 Access Points

- **Application**: http://localhost:3000
- **Health Check**: http://localhost:3000/api/health

## 🏗️ Architecture

### Three Role-Based Consoles

1. **Customer Console** (`/console`)
   - View ingestion history
   - Upload transaction batches
   - Track receipt status with real-time updates
   - View evidence trails and audit logs

2. **Operations Console** (`/ops`)
   - Monitor ingestion pipeline health
   - View dead-letter queue (DLQ)
   - Track processing metrics and alerts

3. **Admin Console** (`/admin`)
   - Manage tenant configurations
   - System settings and user management
   - View comprehensive audit logs

### Key Features

- **Real-time Status Updates**: Auto-polling every 3 seconds
- **Evidence Trail Visualization**: Complete audit trail for compliance
- **Multi-tenant Architecture**: Isolated data and permissions
- **Responsive Design**: AWS-inspired UI components
- **Health Monitoring**: Built-in health checks and metrics
- **Observability Integration**: Connects to monitoring stack

## 🛠️ Technology Stack

- **Framework**: Next.js 14 with App Router
- **Language**: TypeScript
- **Styling**: Tailwind CSS
- **State Management**: React Hooks + Context
- **Build**: Docker multi-stage builds
- **Health Checks**: Built-in API endpoints

## 📁 Project Structure

```
console/
├── app/                    # Next.js App Router pages
│   ├── console/           # Customer console routes
│   ├── ops/               # Operations console routes
│   ├── admin/             # Admin console routes
│   └── api/               # API routes
├── components/            # Reusable UI components
│   ├── auth/             # Authentication components
│   ├── aws/              # AWS-style UI components
│   └── ingestion/        # Ingestion-specific components
├── services/             # API services and data fetching
├── types/                # TypeScript type definitions
├── utils/                # Utility functions
├── constants/            # Application constants
└── config/               # Configuration files
```

## 🔧 Configuration

### Environment Variables

```bash
# Application
NODE_ENV=production
PORT=3000
NEXT_TELEMETRY_DISABLED=1

# API Configuration
NEXT_PUBLIC_API_BASE_URL=/api/v1
```

### Docker Configuration

- **Port**: 3000
- **Health Check**: `/api/health`
- **Build**: Multi-stage with standalone output
- **Base Image**: node:18-alpine

## 🚀 Deployment

### Docker Compose

```yaml
services:
  console:
    build: .
    ports:
      - "3000:3000"
    environment:
      - NODE_ENV=production
    healthcheck:
      test: ["CMD-SHELL", "wget --no-verbose --tries=1 --spider http://localhost:3000/api/health || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3
```

### Scripts

- `npm run deploy` - Build and deploy with Docker
- `npm run docker:build` - Build Docker image
- `npm run docker:up` - Start containers
- `npm run docker:logs` - View container logs

## 🔍 Monitoring

### Health Checks

- **Endpoint**: `GET /api/health`
- **Response**: Service status, uptime, version
- **Docker**: Automated health monitoring

### Logging

- Structured JSON logging
- Request/response tracking
- Error monitoring and alerting

## 🔐 Security

- **Authentication**: API key-based with role validation
- **Authorization**: Role-based access control (RBAC)
- **Input Validation**: Comprehensive request validation
- **HTTPS**: TLS encryption for all communications

## 📊 Performance

- **Build Optimization**: Next.js standalone output
- **Caching**: Efficient asset caching strategies
- **Bundle Size**: Optimized with tree shaking
- **Loading**: Progressive loading with suspense

## 🧪 Development

### Code Quality

- **TypeScript**: Full type safety
- **ESLint**: Code linting and formatting
- **Prettier**: Code formatting
- **Path Aliases**: Clean import statements

### Hot Reload

```bash
npm run dev
# Application available at http://localhost:3000
# Auto-reloads on file changes
```

### Troubleshooting Dev 404 / Missing Chunk Errors

If you see errors like `Cannot find module './8948.js'`, `MODULE_NOT_FOUND` from `.next/server/*`, or repeated 404s for `_next/static/*`:

```bash
# 1) Stop all running Next.js dev servers for this repo
pkill -f "next dev" || true

# 2) Reset caches
npm run clean

# 3) Start with clean cache
npm run dev:fresh
```

Notes:
- Avoid running two `next dev` processes from the same repo at once.
- Use `npm run dev` (or `npm run dev:fresh`) from `backend/console`.
- Confirm the active URL with `npm run dev:url` and open exactly that port.
- If your browser tab still references old chunks, do a hard reload (`Cmd/Ctrl + Shift + R`).
## 📈 Scaling

- **Horizontal**: Stateless design for easy scaling
- **Load Balancing**: Ready for multiple instances
- **CDN**: Static asset optimization
- **Database**: Connection pooling and optimization

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## 📄 License

MIT License. See the repository LICENSE.

---

**Built with ❤️ for financial transaction processing**
