# Evidence

Evidence builds cryptographically verifiable evidence packs for payout traceability.

## Scope

- Multi-tenant REST + JSON API
- Postgres metadata store for evidence packs/items/signatures
- S3 archive object reference persistence (adapter in place)
- Deterministic Merkle root generation for all evidence items
- ed25519 signature over `pack_id + merkle_root + intent_id + contract_id + created_at + ruleset_version`
- Replay API that regenerates pack and checks equivalence
- Kafka consumer scaffold for future event enrichment hooks

## API

- `POST /v1/evidence/packs`
- `GET /v1/evidence/packs/:packID`
- `POST /v1/evidence/replay`
- `GET /healthz`

## Run

```bash
cd backend/evidence
go mod tidy
go run ./cmd
```

## Important Notes

- `recon` is untouched; this service consumes references and hashes, not plaintext PII.
- Current S3 adapter is an interface-backed in-memory implementation for v1 integration speed; replace with AWS SDK implementation when ready.
