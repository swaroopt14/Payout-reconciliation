# Agents

Finance Q&A, investigation, and briefing service (Go). Default port `8086`.

```bash
cd backend/agents
go test ./agents/... ./tools/ -count=1
go run ./cmd
```

Health: `GET /health`. Finance query: `POST` on the agents HTTP API (see root README).
