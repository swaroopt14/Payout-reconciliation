package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrConnectorUnresolved is returned when the per-tenant connectors.id UUID
// for a connector slug (e.g. "razorpayx-v1") cannot be determined. Dispatch
// fails closed on it: governance HOLDs the dispatch with reason
// CONNECTOR_UNRESOLVED and no PSP request is built (D44, L7).
var ErrConnectorUnresolved = errors.New("connector unresolved")

// ReasonConnectorUnresolved is the governance reason code used when the
// connector UUID cannot be resolved.
const ReasonConnectorUnresolved = "CONNECTOR_UNRESOLVED"

// ConnectorResolver maps (tenant_id, provider, connector slug) to the
// tenant's connectors.id UUID (edge's connectors table). Implementations must
// never return uuid.Nil with a nil error.
type ConnectorResolver interface {
	ResolveConnectorUUID(ctx context.Context, tenantID, provider, connectorRef string) (uuid.UUID, error)
}

// providerFromConnectorRef derives the provider from a connector slug:
// "razorpayx-v1" → "razorpayx".
func providerFromConnectorRef(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if i := strings.IndexByte(ref, '-'); i > 0 {
		return ref[:i]
	}
	return ref
}

// resolveConnectorUUID applies the fail-closed rules around any resolver:
// a nil resolver, an error, or a nil UUID are all ErrConnectorUnresolved.
func resolveConnectorUUID(ctx context.Context, r ConnectorResolver, tenantID, connectorRef string) (uuid.UUID, error) {
	if r == nil {
		return uuid.Nil, fmt.Errorf("%w: no connector resolver configured (tenant=%s ref=%s)", ErrConnectorUnresolved, tenantID, connectorRef)
	}
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(connectorRef) == "" {
		return uuid.Nil, fmt.Errorf("%w: tenant_id and connector ref are required", ErrConnectorUnresolved)
	}
	id, err := r.ResolveConnectorUUID(ctx, tenantID, providerFromConnectorRef(connectorRef), connectorRef)
	if err != nil {
		if errors.Is(err, ErrConnectorUnresolved) {
			return uuid.Nil, err
		}
		return uuid.Nil, fmt.Errorf("%w: %v", ErrConnectorUnresolved, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: resolver returned nil UUID (tenant=%s ref=%s)", ErrConnectorUnresolved, tenantID, connectorRef)
	}
	return id, nil
}

// StaticConnectorResolver is a config-provided per-tenant map
// tenant_id → connector slug → connectors.id UUID. It is the interim source
// until relay has a sanctioned read path to edge's connectors table; it is
// disabled unless explicitly configured (a nil resolver fails closed).
type StaticConnectorResolver struct {
	byTenant map[string]map[string]uuid.UUID
}

// ParseStaticConnectorMap parses JSON of the form
// {"<tenant_uuid>": {"razorpayx-v1": "<connectors.id uuid>"}}.
// Invalid or nil UUIDs are rejected at load time.
func ParseStaticConnectorMap(raw string) (*StaticConnectorResolver, error) {
	var in map[string]map[string]string
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, fmt.Errorf("connector map: invalid JSON: %w", err)
	}
	out := &StaticConnectorResolver{byTenant: make(map[string]map[string]uuid.UUID, len(in))}
	for tenant, refs := range in {
		tenant = strings.ToLower(strings.TrimSpace(tenant))
		if _, err := uuid.Parse(tenant); err != nil {
			return nil, fmt.Errorf("connector map: tenant %q is not a UUID", tenant)
		}
		m := make(map[string]uuid.UUID, len(refs))
		for ref, idStr := range refs {
			id, err := uuid.Parse(strings.TrimSpace(idStr))
			if err != nil || id == uuid.Nil {
				return nil, fmt.Errorf("connector map: tenant %s ref %q: %q is not a non-nil UUID", tenant, ref, idStr)
			}
			m[strings.ToLower(strings.TrimSpace(ref))] = id
		}
		out.byTenant[tenant] = m
	}
	return out, nil
}

func (s *StaticConnectorResolver) ResolveConnectorUUID(_ context.Context, tenantID, _ string, connectorRef string) (uuid.UUID, error) {
	if s == nil {
		return uuid.Nil, ErrConnectorUnresolved
	}
	id, ok := s.byTenant[strings.ToLower(strings.TrimSpace(tenantID))][strings.ToLower(strings.TrimSpace(connectorRef))]
	if !ok || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: no mapping for tenant=%s ref=%s", ErrConnectorUnresolved, tenantID, connectorRef)
	}
	return id, nil
}
