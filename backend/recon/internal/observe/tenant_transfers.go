package observe

import (
	"context"
	"sync"

	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
)

// TenantTransfers builds Processor.TransfersFor (D52): each tenant
// connector gets a Razorpay client made from ITS OWN key pair, resolved via
// the credential resolver (edge, cached in memory). Clients are cached by
// tenant+connector+mode+key id, so a rotated key yields a new client. There is
// no platform-key fallback: a resolve error is returned as-is
// (poll.ErrNoTenantSecret / poll.ErrCredentialSourceUnavailable).
type TenantTransfers struct {
	Resolver poll.CredentialResolver
	// NewLookup builds a lookup from a config; nil uses razorpay.NewClient.
	NewLookup func(cfg razorpay.Config) (TransferLookup, error)

	mu      sync.Mutex
	clients map[string]TransferLookup
}

func (t *TenantTransfers) For(ctx context.Context, tenantID, connectorID, mode string) (TransferLookup, error) {
	cfg, err := t.Resolver.Resolve(ctx, tenantID, connectorID, mode)
	if err != nil {
		return nil, err
	}
	key := tenantID + "|" + connectorID + "|" + mode + "|" + cfg.KeyID
	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.clients[key]; ok {
		return c, nil
	}
	build := t.NewLookup
	if build == nil {
		build = func(cfg razorpay.Config) (TransferLookup, error) {
			c, err := razorpay.NewClient(cfg, nil, nil, nil)
			if err != nil {
				return nil, err
			}
			return c, nil
		}
	}
	c, err := build(cfg)
	if err != nil {
		return nil, poll.ErrCredentialSourceUnavailable
	}
	if t.clients == nil {
		t.clients = map[string]TransferLookup{}
	}
	// Drop older clients for the same tenant connector (key rotated).
	prefix := tenantID + "|" + connectorID + "|" + mode + "|"
	for k := range t.clients {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(t.clients, k)
		}
	}
	t.clients[key] = c
	return c, nil
}
