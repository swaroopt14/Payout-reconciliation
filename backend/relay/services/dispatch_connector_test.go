package services

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

type connectorEventPayload struct {
	Payload struct {
		ConnectorID  string `json:"connector_id"`
		ConnectorRef string `json:"connector_ref"`
	} `json:"payload"`
}

func decodeConnector(t *testing.T, b []byte) connectorEventPayload {
	t.Helper()
	var p connectorEventPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("decode outbox payload %s: %v", b, err)
	}
	return p
}

// TestDispatch_ConnectorUUIDResolvedPerTenant: DispatchCreated and
// AttemptSent carry the dispatching tenant's own connectors.id UUID as
// connector_id, with the slug only as connector_ref (D44, L7).
func TestDispatch_ConnectorUUIDResolvedPerTenant(t *testing.T) {
	tenants := map[string]string{
		"11111111-1111-4111-8111-111111111111": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"22222222-2222-4222-8222-222222222222": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	}
	raw, _ := json.Marshal(map[string]map[string]string{
		"11111111-1111-4111-8111-111111111111": {"razorpayx-v1": tenants["11111111-1111-4111-8111-111111111111"]},
		"22222222-2222-4222-8222-222222222222": {"razorpayx-v1": tenants["22222222-2222-4222-8222-222222222222"]},
	})
	resolver, err := ParseStaticConnectorMap(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	for tenant, wantUUID := range tenants {
		t.Run(tenant, func(t *testing.T) {
			loop, pspClient, _, rec := newAmountTestLoop(t)
			loop.SetConnectorResolver(resolver)
			e := amountTestEvent(t, "100.50")
			e.TenantID = tenant
			if !loop.processEvent(context.Background(), 1, e) {
				t.Fatal("processEvent returned false")
			}
			if n := len(pspClient.requests()); n != 1 {
				t.Fatalf("PSP requests = %d, want 1", n)
			}
			events := rec.outboxPayloads()
			for _, typ := range []string{"DispatchCreated", "AttemptSent"} {
				if len(events[typ]) != 1 {
					t.Fatalf("%s events = %d, want 1", typ, len(events[typ]))
				}
				p := decodeConnector(t, events[typ][0])
				if p.Payload.ConnectorID != wantUUID {
					t.Fatalf("%s connector_id = %q, want tenant UUID %q", typ, p.Payload.ConnectorID, wantUUID)
				}
				if p.Payload.ConnectorRef != "razorpayx-v1" {
					t.Fatalf("%s connector_ref = %q, want razorpayx-v1", typ, p.Payload.ConnectorRef)
				}
			}
		})
	}
}

type nilUUIDResolver struct{}

func (nilUUIDResolver) ResolveConnectorUUID(context.Context, string, string, string) (uuid.UUID, error) {
	return uuid.Nil, nil // buggy resolver: must still fail closed
}

// TestDispatch_ConnectorUnresolvedFailsClosed: with no resolver, an unmapped
// tenant, or a resolver returning the nil UUID, no PSP request is sent, the
// dispatch is HELD with CONNECTOR_UNRESOLVED, and no event ever carries a nil
// UUID or the slug as connector_id.
func TestDispatch_ConnectorUnresolvedFailsClosed(t *testing.T) {
	otherTenantMap, err := ParseStaticConnectorMap(`{"99999999-9999-4999-8999-999999999999":{"razorpayx-v1":"cccccccc-cccc-4ccc-8ccc-cccccccccccc"}}`)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]ConnectorResolver{
		"no_resolver":     nil,
		"tenant_unmapped": otherTenantMap,
		"nil_uuid":        nilUUIDResolver{},
	}
	for name, resolver := range cases {
		t.Run(name, func(t *testing.T) {
			loop, pspClient, _, rec := newAmountTestLoop(t)
			loop.SetConnectorResolver(resolver)
			if !loop.processEvent(context.Background(), 1, amountTestEvent(t, "100.50")) {
				t.Fatal("processEvent returned false")
			}
			if n := len(pspClient.requests()); n != 0 {
				t.Fatalf("PSP requests = %d, want 0", n)
			}
			if !rec.anyArgEquals("HOLD_DISPATCH") || !rec.anyArgEquals("HELD") {
				t.Fatal("dispatch was not HELD via governance")
			}
			events := rec.outboxPayloads()
			if len(events["DispatchCreated"]) != 0 || len(events["AttemptSent"]) != 0 {
				t.Fatalf("unresolved connector published DispatchCreated/AttemptSent: %v", events)
			}
			if len(events["DispatchGovernanceEvaluated"]) != 1 {
				t.Fatalf("governance events = %d, want 1", len(events["DispatchGovernanceEvaluated"]))
			}
			var gov struct {
				Payload struct {
					Decision    string   `json:"decision"`
					ReasonCodes []string `json:"reason_codes"`
				} `json:"payload"`
			}
			_ = json.Unmarshal(events["DispatchGovernanceEvaluated"][0], &gov)
			if gov.Payload.Decision != "HOLD_DISPATCH" || len(gov.Payload.ReasonCodes) == 0 || gov.Payload.ReasonCodes[len(gov.Payload.ReasonCodes)-1] != ReasonConnectorUnresolved {
				t.Fatalf("governance = %+v, want HOLD_DISPATCH with %s", gov.Payload, ReasonConnectorUnresolved)
			}
			for typ, list := range events {
				for _, b := range list {
					p := decodeConnector(t, b)
					if p.Payload.ConnectorID == uuid.Nil.String() || p.Payload.ConnectorID == "razorpayx-v1" {
						t.Fatalf("%s carried connector_id %q", typ, p.Payload.ConnectorID)
					}
				}
			}
		})
	}
}

func TestParseStaticConnectorMap_RejectsNilAndInvalid(t *testing.T) {
	for _, raw := range []string{
		`{"11111111-1111-4111-8111-111111111111":{"razorpayx-v1":"00000000-0000-0000-0000-000000000000"}}`,
		`{"11111111-1111-4111-8111-111111111111":{"razorpayx-v1":"razorpayx-v1"}}`,
		`{"tenant-1":{"razorpayx-v1":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}`,
		`not json`,
	} {
		if _, err := ParseStaticConnectorMap(raw); err == nil {
			t.Errorf("ParseStaticConnectorMap(%s) = nil error", raw)
		}
	}
}
