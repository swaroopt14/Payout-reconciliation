package railrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Request struct {
	TenantID    string `json:"tenant_id,omitempty"`
	EntityID    string `json:"entity_id"`
	Direction   string `json:"direction"`
	Rail        string `json:"rail"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type Fallback struct {
	PSP         string `json:"psp"`
	ConnectorID string `json:"connector_id"`
}

type Decision struct {
	RoutingID   string     `json:"routing_id"`
	PSP         string     `json:"psp"`
	ConnectorID string     `json:"connector_id"`
	Rail        string     `json:"rail"`
	Direction   string     `json:"direction"`
	Algorithm   string     `json:"algorithm"`
	Fallbacks   []Fallback `json:"fallbacks"`
}

type Outcome struct {
	RoutingID     string  `json:"routing_id,omitempty"`
	PaymentID     string  `json:"payment_id"`
	Processor     string  `json:"processor"`
	Success       bool    `json:"success"`
	LatencyMs     float64 `json:"latency_ms,omitempty"`
	FailureClass  string  `json:"failure_class,omitempty"`
	FailureDetail string  `json:"failure_detail,omitempty"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Route(ctx context.Context, in Request) (Decision, error) {
	if c == nil || c.baseURL == "" {
		return Decision{}, fmt.Errorf("router not configured")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Decision{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/route", bytes.NewReader(body))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Decision{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return Decision{}, fmt.Errorf("router http %d", resp.StatusCode)
	}
	var out Decision
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Decision{}, err
	}
	if strings.TrimSpace(out.ConnectorID) == "" {
		return Decision{}, fmt.Errorf("router returned empty connector_id")
	}
	return out, nil
}

func (c *Client) ReportOutcome(ctx context.Context, in Outcome) error {
	if c == nil || c.baseURL == "" {
		return nil
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/routing/outcome", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("router outcome http %d", resp.StatusCode)
	}
	return nil
}
