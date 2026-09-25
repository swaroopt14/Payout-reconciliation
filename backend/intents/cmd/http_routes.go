package main

import (
	"net/http"

	"zord-intent-engine/internal/auth"
)

// adminRouteHandlers are the /v1/admin/* handlers. Every admin route runs
// behind auth.Protect (verified user JWT + tenant match); the held-intent
// approval additionally requires a PAYOUT_APPROVER user (D39).
type adminRouteHandlers struct {
	MappingProfilesListOrCreate http.HandlerFunc
	MappingProfileItem          http.HandlerFunc
	TenantSynonymsListOrCreate  http.HandlerFunc
	TenantSynonymDeactivate     http.HandlerFunc
	IntentApprove               http.HandlerFunc
}

func registerAdminRoutes(mux *http.ServeMux, h adminRouteHandlers) {
	// ── Admin: Mapping Profile CRUD ───────────────────────────────────────
	mux.HandleFunc("/v1/admin/mapping-profiles", auth.Protect(h.MappingProfilesListOrCreate))
	mux.HandleFunc("/v1/admin/mapping-profiles/", auth.Protect(h.MappingProfileItem))

	// ── Admin: Tenant Synonym CRUD ────────────────────────────────────────
	mux.HandleFunc("/v1/admin/tenant-synonyms", auth.Protect(h.TenantSynonymsListOrCreate))
	mux.HandleFunc("/v1/admin/tenant-synonyms/", auth.Protect(h.TenantSynonymDeactivate))

	// ── Admin: R-05 held-intent (payout) approval ─────────────────────────
	// PAYOUT_APPROVER users only; CUSTOMER_ADMIN and API keys are refused.
	mux.HandleFunc("/v1/admin/intents/", auth.Protect(auth.RequireRole(auth.RolePayoutApprover, h.IntentApprove)))
}

// internalRouteHandlers are the /internal/* handlers. Each route is wrapped
// in auth.RequireInternalScope with the narrowest scope that covers it. The
// handlers keep their own X-Relay-Token check as defence in depth.
type internalRouteHandlers struct {
	DLQCount             http.HandlerFunc
	OutboxLease          http.HandlerFunc
	OutboxAck            http.HandlerFunc
	OutboxNack           http.HandlerFunc
	DLQLease             http.HandlerFunc
	DLQAck               http.HandlerFunc
	DLQNack              http.HandlerFunc
	BatchLease           http.HandlerFunc
	BatchAck             http.HandlerFunc
	BatchNack            http.HandlerFunc
	AirflowTransform     http.HandlerFunc
	NormalizationQuality http.HandlerFunc
}

// internalRoutes is the single source of truth for /internal/* guards, so a
// test can assert every route is scoped.
func internalRoutes(h internalRouteHandlers) []struct {
	Pattern string
	Scope   string
	Handler http.HandlerFunc
} {
	return []struct {
		Pattern string
		Scope   string
		Handler http.HandlerFunc
	}{
		// R-02: cross-tenant read, console token only.
		{"/internal/dlq/count", auth.ScopeIntentReadCrossTenant, h.DLQCount},
		{"/internal/outbox/lease", auth.ScopeOutboxRelay, h.OutboxLease},
		{"/internal/outbox/ack", auth.ScopeOutboxRelay, h.OutboxAck},
		{"/internal/outbox/nack", auth.ScopeOutboxRelay, h.OutboxNack},
		{"/internal/dlq/lease", auth.ScopeOutboxRelay, h.DLQLease},
		{"/internal/dlq/ack", auth.ScopeOutboxRelay, h.DLQAck},
		{"/internal/dlq/nack", auth.ScopeOutboxRelay, h.DLQNack},
		{"/internal/relay/canonical_batches/lease", auth.ScopeOutboxRelay, h.BatchLease},
		{"/internal/relay/canonical_batches/ack", auth.ScopeOutboxRelay, h.BatchAck},
		{"/internal/relay/canonical_batches/nack", auth.ScopeOutboxRelay, h.BatchNack},
		{"/internal/airflow/transform", auth.ScopeETLTransform, h.AirflowTransform},
		{"/internal/normalization/quality", auth.ScopeNormalizationRead, h.NormalizationQuality},
	}
}

func registerInternalRoutes(mux *http.ServeMux, h internalRouteHandlers) {
	for _, rt := range internalRoutes(h) {
		mux.HandleFunc(rt.Pattern, auth.RequireInternalScope(rt.Scope, rt.Handler))
	}
}
