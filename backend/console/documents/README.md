# Ingestion Console

A fully functional frontend for the ingestion console, built with Next.js 14, TypeScript, and Tailwind CSS.

## Features

- **Three Role-Based Consoles**: Customer, Ops, and Admin with distinct login pages
- **Evidence Trail**: Every ingestion creates a visible, timestamped evidence trail
- **Real-time Updates**: Polling for receipt status updates (every 3 seconds)
- **Evidence Explorer**: Navigable folder tree structure showing immutable artifacts
- **Batch Management**: View batch progress and failed rows
- **Mock Authentication**: Role switcher for easy testing
- **Enterprise Minimal Design**: Clean, professional UI

## Getting Started

### Prerequisites

- Node.js 18+ 
- npm or yarn

### Installation

```bash
npm install
```

### Development

```bash
npm run dev
```

Open [http://localhost:3000](http://localhost:3000) in your browser.

## Project Structure

```
/app                          # Next.js App Router pages
 ├─ /console                  # Customer console routes
 ├─ /ops                      # Operations console routes
 └─ /admin                    # Admin console routes

/components                   # React components (feature-based)
 ├─ /auth                     # Authentication components
 ├─ /ingestion                # Ingestion-related components
 └─ /common                   # Shared components

/services                     # Business logic & API services
 ├─ /api                      # API service layer
 └─ /auth                     # Authentication service

/hooks                        # Custom React hooks
/utils                        # Utility functions
 ├─ /errors                   # Error handling
 ├─ /validation               # Form validation
 └─ /formatting               # Data formatting

/types                        # TypeScript type definitions
/constants                    # Application constants
/config                       # Configuration files
```

See [ARCHITECTURE.md](./ARCHITECTURE.md) for detailed structure and [CONTRIBUTING.md](./CONTRIBUTING.md) for development guidelines.

## Documentation Index

- [ARCHITECTURE.md](./ARCHITECTURE.md) - Detailed architecture and structure
- [CONTRIBUTING.md](./CONTRIBUTING.md) - Development workflow and guidelines
- [DEBUGGING.md](./DEBUGGING.md) - Debugging guide and tips
- [PROJECT_STRUCTURE.md](./PROJECT_STRUCTURE.md) - Project structure overview

## Authentication

The app uses **mock authentication** with localStorage. You can:

1. Login at any of the three login pages:
   - `/console/login` - Customer console
   - `/ops/login` - Ops console
   - `/admin/login` - Admin console

2. Use the **Role Switcher** (top-right corner) to switch between roles without re-logging in.

## Key Features

### Evidence Trail

Every ingestion creates evidence, even on failure:

```
Evidence /
 └─ 2026-01-10 /
    └─ 09:45:12 /
       ├─ raw-envelope.json
       ├─ canonical-intent.json
       ├─ validation-report.json
       └─ signatures.json
```

### Receipt Status Timeline

Live timeline showing:
- Received
- Raw Stored
- Validating
- Canonicalized / Failed

### Polling

Receipts automatically poll every 3 seconds until reaching a terminal state (CANONICALIZED or FAILED).

### Role-Based Permissions

- **Customer User**: Can view receipts and evidence
- **Customer Admin**: Can access inbox, re-upload failed rows
- **Ops**: Can view monitor, DLQ, retry, download evidence
- **Admin**: Full access to tenant management

## Mock Data

The app includes mock data for:
- Receipts (with various statuses)
- Batches
- Evidence trees
- Evidence files

All data is stored in-memory and persists during the session.

## API Contracts (Mocked)

The frontend expects these endpoints (currently mocked):

- `GET /v1/receipts/:id` - Get receipt details
- `GET /v1/receipts/:id/evidence` - Get evidence tree
- `GET /v1/batches/:id` - Get batch details
- `GET /v1/evidence/:receiptId/tree` - Get evidence tree structure
- `GET /v1/evidence/:receiptId/file` - Get evidence file content

## Building for Production

```bash
npm run build
npm start
```

## Design Principles

1. **Evidence First**: Every ingestion must create evidence
2. **Time Visible**: Timestamps shown everywhere (UTC + local)
3. **No Dead Ends**: All screens are navigable
4. **Role-Based UI**: Buttons hidden (not disabled) based on permissions
5. **Enterprise Minimal**: Clean, professional design

## Testing the Flow

1. **Login** as Customer User at `/console/login`
2. **Create Intent** or **Upload File** from ingestion home
3. **View Receipt** - see timeline and status
4. **View Evidence** - navigate the folder tree
5. **Switch to Ops** - use role switcher to see monitor and DLQ

## Notes

- All authentication is mocked (localStorage)
- All API calls are mocked (in-memory data)
- Polling stops automatically on terminal states
- Evidence exists even for failed receipts
- Timestamps are displayed in both UTC and local time

## Development

### Code Organization

The codebase is organized for **agile development** and **easy debugging**:

- **Feature-based structure**: Components grouped by domain
- **Separation of concerns**: Services, components, hooks, utils
- **Index exports**: Clean imports via barrel exports
- **Type safety**: Comprehensive TypeScript types
- **Error handling**: Centralized error utilities

### Documentation

All documentation files are in the `documents/` folder:

- [ARCHITECTURE.md](./ARCHITECTURE.md) - Detailed architecture and structure
- [CONTRIBUTING.md](./CONTRIBUTING.md) - Development workflow and guidelines
- [DEBUGGING.md](./DEBUGGING.md) - Debugging guide and tips
- [PROJECT_STRUCTURE.md](./PROJECT_STRUCTURE.md) - Project structure overview

### Quick Commands

```bash
# Development
npm run dev

# Build
npm run build

# Lint
npm run lint

# Type check
npm run build  # TypeScript checks during build
```
