package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Payout is the stored provider-side payout.
type Payout struct {
	PayoutID    string `json:"payout_id"`
	ReferenceID string `json:"reference_id"`
	Status      string `json:"status"`
	Amount      int64  `json:"amount"`
	Mode        string `json:"mode"`
	Narration   string `json:"narration"`
	UTR         string `json:"utr,omitempty"`
	CreatedAt   int64  `json:"created_at"`

	bodyHash string
}

// RecordedRequest is one request as it arrived on the wire.
type RecordedRequest struct {
	Seq         int                 `json:"seq"`
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Query       string              `json:"query,omitempty"`
	Headers     map[string][]string `json:"headers"`
	RawBody     string              `json:"raw_body"`
	ReferenceID string              `json:"reference_id,omitempty"`
	Status      int                 `json:"status"`
	Outcome     string              `json:"outcome,omitempty"`
	ReceivedAt  time.Time           `json:"received_at"`
}

// WebhookDelivery records one webhook sent to edge (the secret is not stored here).
type WebhookDelivery struct {
	PayoutID   string    `json:"payout_id"`
	Event      string    `json:"event"`
	EventID    string    `json:"event_id"`
	URL        string    `json:"url"`
	StatusCode int       `json:"status_code"`
	Error      string    `json:"error,omitempty"`
	Body       string    `json:"body"`
	SentAt     time.Time `json:"sent_at"`
}

// Server is the fake PSP. All state is in memory and guarded by mu.
type Server struct {
	mu         sync.Mutex
	cfg        Config
	payouts    map[string]*Payout // idempotency key -> payout
	byRef      map[string]*Payout // reference_id -> payout
	byID       map[string]*Payout // payout_id -> payout
	modes      map[string]string  // reference_id -> forced mode
	requests   []*RecordedRequest
	webhooks   []WebhookDelivery
	seq        int
	httpClient *http.Client
	now        func() time.Time
}

func NewServer(cfg Config) *Server {
	s := &Server{cfg: cfg, httpClient: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	s.resetLocked(false)
	return s
}

func (s *Server) resetLocked(clearConfig bool) {
	s.payouts = map[string]*Payout{}
	s.byRef = map[string]*Payout{}
	s.byID = map[string]*Payout{}
	s.modes = map[string]string{}
	s.requests = nil
	s.webhooks = nil
	s.seq = 0
	if clearConfig {
		s.cfg.ConnectorID = ""
		s.cfg.WebhookSecret = ""
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "fake-psp"})
	})
	mux.HandleFunc("POST /v1/payouts", s.handleCreatePayout)
	mux.HandleFunc("GET /v1/payouts", s.handleGetPayout)
	mux.HandleFunc("POST /v1/detokenize", s.handleDetokenize)
	mux.HandleFunc("GET /_admin/config", s.handleGetConfig)
	mux.HandleFunc("POST /_admin/config", s.handleSetConfig)
	mux.HandleFunc("GET /_admin/requests", s.handleRequests)
	mux.HandleFunc("GET /_admin/webhooks", s.handleWebhooks)
	mux.HandleFunc("POST /_admin/webhook/{payout_id}", s.handleAdminWebhook)
	mux.HandleFunc("POST /_admin/reset", s.handleReset)
	mux.HandleFunc("POST /_admin/mode", s.handleMode)
	return logRequests(mux)
}

// ── POST /v1/payouts ────────────────────────────────────────────────────────

type payoutReq struct {
	ReferenceID string `json:"reference_id"`
	Narration   string `json:"narration"`
	Mode        string `json:"mode"`
	Beneficiary *struct {
		Name          string `json:"name"`
		AccountNumber string `json:"account_number"`
		IFSC          string `json:"ifsc"`
	} `json:"beneficiary"`
}

func (s *Server) record(r *http.Request, raw []byte, ref string) *RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	rec := &RecordedRequest{
		Seq: s.seq, Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Headers: cloneHeaders(r.Header), RawBody: string(raw), ReferenceID: ref,
		ReceivedAt: s.now().UTC(),
	}
	s.requests = append(s.requests, rec)
	return rec
}

func (s *Server) finish(rec *RecordedRequest, status int, outcome string) {
	s.mu.Lock()
	rec.Status, rec.Outcome = status, outcome
	s.mu.Unlock()
}

func (s *Server) handleCreatePayout(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "read_error", "cannot read body")
		return
	}
	// Best-effort reference_id for recording before validation.
	var peek struct {
		ReferenceID any `json:"reference_id"`
	}
	_ = json.Unmarshal(raw, &peek)
	ref, _ := peek.ReferenceID.(string)
	rec := s.record(r, raw, ref)

	fail := func(status int, code, msg string) {
		s.finish(rec, status, code)
		writeErr(w, status, code, msg)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		fail(http.StatusBadRequest, "invalid_json", "body must be a JSON object")
		return
	}
	amount, code := ParseIntegerAmount(fields["amount"])
	var req payoutReq
	if err := json.Unmarshal(raw, &req); err != nil {
		// e.g. reference_id with the wrong type
		fail(http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(req.ReferenceID) == "" {
		fail(http.StatusBadRequest, "reference_id_required", "reference_id is required")
		return
	}
	if code != "" {
		fail(http.StatusBadRequest, code, "amount must be a positive JSON integer in paise")
		return
	}
	if req.Beneficiary == nil || req.Beneficiary.Name == "" || req.Beneficiary.AccountNumber == "" {
		fail(http.StatusUnprocessableEntity, "beneficiary_required", "beneficiary fields are required")
		return
	}

	key := r.Header.Get("X-Payout-Idempotency")
	if key == "" {
		key = req.ReferenceID
	}
	hash := bodyHash(raw)

	s.mu.Lock()
	if existing, ok := s.payouts[key]; ok {
		s.mu.Unlock()
		if existing.bodyHash != hash {
			fail(http.StatusConflict, "idempotency_conflict", "same idempotency key with a different body")
			return
		}
		s.finish(rec, http.StatusOK, "idempotent_replay")
		writeJSON(w, http.StatusOK, payoutResponse(existing))
		return
	}
	mode := s.modes[req.ReferenceID]
	s.mu.Unlock()
	if mode == "" {
		mode = ModeForAmount(amount)
	}

	switch mode {
	case "500":
		fail(http.StatusInternalServerError, "psp_internal_error", "simulated PSP 500")
		return
	case "decline":
		fail(http.StatusUnprocessableEntity, "hard_decline", "simulated hard decline")
		return
	case "429":
		w.Header().Set("Retry-After", "1")
		fail(http.StatusTooManyRequests, "rate_limited", "simulated rate limit")
		return
	case "timeout":
		s.finish(rec, 0, "timeout_hold")
		select {
		case <-time.After(s.holdDuration()):
		case <-r.Context().Done():
			return
		}
		fail(http.StatusGatewayTimeout, "psp_timeout", "simulated timeout")
		return
	case "drop":
		s.finish(rec, 0, "network_drop")
		hj, ok := w.(http.Hijacker)
		if !ok {
			fail(http.StatusInternalServerError, "hijack_unsupported", "cannot drop connection")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}

	p := &Payout{
		PayoutID:    "pout_fake_" + req.ReferenceID,
		ReferenceID: req.ReferenceID,
		Status:      "pending",
		Amount:      amount,
		Mode:        req.Mode,
		Narration:   req.Narration,
		CreatedAt:   s.now().Unix(),
		bodyHash:    hash,
	}
	s.mu.Lock()
	// Re-check under lock (concurrent duplicate).
	if existing, ok := s.payouts[key]; ok {
		s.mu.Unlock()
		if existing.bodyHash != hash {
			fail(http.StatusConflict, "idempotency_conflict", "same idempotency key with a different body")
			return
		}
		s.finish(rec, http.StatusOK, "idempotent_replay")
		writeJSON(w, http.StatusOK, payoutResponse(existing))
		return
	}
	s.payouts[key] = p
	s.byRef[p.ReferenceID] = p
	s.byID[p.PayoutID] = p
	autoMS := s.cfg.AutoWebhookMS
	s.mu.Unlock()

	s.finish(rec, http.StatusOK, "created")
	writeJSON(w, http.StatusOK, payoutResponse(p))

	if autoMS > 0 {
		id := p.PayoutID
		time.AfterFunc(time.Duration(autoMS)*time.Millisecond, func() {
			if _, err := s.SendWebhook(id, "payout.processed"); err != nil {
				log.Printf("auto webhook for %s not delivered: %v", id, err)
			}
		})
	}
}

func (s *Server) holdDuration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.TimeoutHold
}

func payoutResponse(p *Payout) map[string]string {
	return map[string]string{"payout_id": p.PayoutID, "reference_id": p.ReferenceID, "status": "pending"}
}

// ParseIntegerAmount validates the raw JSON token for "amount". It returns an
// error code ("" on success). The token must be a bare JSON integer: no
// quotes, no '.', no exponent. It is decoded with UseNumber so large values
// never pass through float64.
func ParseIntegerAmount(raw json.RawMessage) (int64, string) {
	tok := strings.TrimSpace(string(raw))
	if tok == "" || tok == "null" {
		return 0, "amount_required"
	}
	if strings.ContainsAny(tok, `."eE`) {
		return 0, "amount_not_integer"
	}
	dec := json.NewDecoder(strings.NewReader(tok))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, "amount_not_integer"
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, "amount_not_integer"
	}
	i, err := n.Int64()
	if err != nil {
		return 0, "amount_not_integer"
	}
	if i <= 0 {
		return 0, "amount_not_positive"
	}
	return i, ""
}

// ModeForAmount maps amount (paise) % 100 to a failure mode ("ok" otherwise).
func ModeForAmount(amount int64) string {
	switch amount % 100 {
	case 13:
		return "500"
	case 99:
		return "timeout"
	case 22:
		return "decline"
	case 29:
		return "429"
	case 44:
		return "drop"
	}
	return "ok"
}

func bodyHash(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		buf.Reset()
		buf.Write(raw)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// ── GET /v1/payouts?reference_id= ──────────────────────────────────────────

func (s *Server) handleGetPayout(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("reference_id")
	rec := s.record(r, nil, ref)
	if ref == "" {
		s.finish(rec, http.StatusBadRequest, "reference_id_required")
		writeErr(w, http.StatusBadRequest, "reference_id_required", "reference_id query parameter is required")
		return
	}
	s.mu.Lock()
	p, ok := s.byRef[ref]
	var out Payout
	if ok {
		out = *p
	}
	s.mu.Unlock()
	if !ok {
		s.finish(rec, http.StatusNotFound, "not_found")
		writeErr(w, http.StatusNotFound, "not_found", "no payout for reference_id")
		return
	}
	s.finish(rec, http.StatusOK, "found")
	writeJSON(w, http.StatusOK, out)
}

// ── Interim token enclave stub ─────────────────────────────────────────────

// handleDetokenize mirrors relay/services/token_client.go: a flat map of
// field -> token in, the same field names with plaintext values out.
// Values that are not tokens are echoed back; tokens map to obviously fake
// deterministic placeholders.
func (s *Server) handleDetokenize(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	rec := s.record(r, raw, "")
	var in map[string]string
	if err := json.Unmarshal(raw, &in); err != nil {
		s.finish(rec, http.StatusBadRequest, "invalid_json")
		writeErr(w, http.StatusBadRequest, "invalid_json", "body must be a flat JSON object of strings")
		return
	}
	fake := map[string]string{
		"account_number": "000000000001",
		"name":           "E2E Payee",
		"ifsc":           "HDFC0000001",
		"vpa":            "e2e@fakepsp",
		"email":          "payee@example.test",
		"phone":          "+910000000000",
	}
	out := map[string]string{}
	for k, v := range in {
		if v == "" {
			continue
		}
		if strings.HasPrefix(v, "tok_") {
			if f, ok := fake[k]; ok {
				out[k] = f
			} else {
				out[k] = "e2e-" + k
			}
			continue
		}
		out[k] = v
	}
	s.finish(rec, http.StatusOK, "detokenized")
	writeJSON(w, http.StatusOK, out)
}

// ── Webhooks ───────────────────────────────────────────────────────────────

// Sign returns hex(HMAC-SHA256(body, secret)), the X-Razorpay-Signature value.
func Sign(body []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// FakeUTR derives a stable "FAKEUTR" + 12 digits from the dispatch/reference id.
func FakeUTR(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	n := binary.BigEndian.Uint64(sum[:8]) % 1_000_000_000_000
	return fmt.Sprintf("FAKEUTR%012d", n)
}

func newEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("evt_fake_%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// BuildWebhookBody builds the spec §3 payout webhook body.
func BuildWebhookBody(p Payout, event string, now int64) ([]byte, string) {
	status := strings.TrimPrefix(event, "payout.")
	utr := ""
	if status == "processed" || status == "reversed" {
		utr = FakeUTR(p.ReferenceID)
	}
	body := map[string]any{
		"entity":     "event",
		"event":      event,
		"created_at": now,
		"payload": map[string]any{
			"payout": map[string]any{
				"entity": map[string]any{
					"id":           p.PayoutID,
					"entity":       "payout",
					"amount":       p.Amount,
					"currency":     "INR",
					"status":       status,
					"mode":         p.Mode,
					"utr":          utr,
					"reference_id": p.ReferenceID,
					"created_at":   p.CreatedAt,
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	return b, status
}

var validEvents = map[string]bool{"payout.processed": true, "payout.reversed": true, "payout.failed": true}

// SendWebhook posts a signed webhook for payoutID to edge.
func (s *Server) SendWebhook(payoutID, event string) (WebhookDelivery, error) {
	if !validEvents[event] {
		return WebhookDelivery{}, fmt.Errorf("unsupported event %q", event)
	}
	s.mu.Lock()
	p, ok := s.byID[payoutID]
	connectorID, secret, edgeURL := s.cfg.ConnectorID, s.cfg.WebhookSecret, s.cfg.EdgeURL
	var snap Payout
	if ok {
		snap = *p
	}
	s.mu.Unlock()
	if !ok {
		return WebhookDelivery{}, errNotFound
	}
	if connectorID == "" || secret == "" {
		return WebhookDelivery{}, errNotConfigured
	}
	body, status := BuildWebhookBody(snap, event, s.now().Unix())
	d := WebhookDelivery{
		PayoutID: payoutID, Event: event, EventID: newEventID(),
		URL:  strings.TrimRight(edgeURL, "/") + "/v1/webhooks/razorpay/" + connectorID,
		Body: string(body), SentAt: s.now().UTC(),
	}
	req, err := http.NewRequest(http.MethodPost, d.URL, bytes.NewReader(body))
	if err != nil {
		return d, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Razorpay-Event-Id", d.EventID)
	req.Header.Set("X-Razorpay-Signature", Sign(body, secret))
	resp, err := s.httpClient.Do(req)
	if err != nil {
		d.Error = err.Error()
	} else {
		d.StatusCode = resp.StatusCode
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	s.mu.Lock()
	if err == nil && d.StatusCode < 300 {
		p.Status = status
		if status == "processed" || status == "reversed" {
			p.UTR = FakeUTR(p.ReferenceID)
		}
	}
	s.webhooks = append(s.webhooks, d)
	s.mu.Unlock()
	log.Printf("webhook %s %s -> %s status=%d err=%q", event, payoutID, d.URL, d.StatusCode, d.Error)
	return d, err
}

var (
	errNotFound      = fmt.Errorf("payout not found")
	errNotConfigured = fmt.Errorf("connector_id/webhook_secret not configured (POST /_admin/config)")
)

// ── Admin ──────────────────────────────────────────────────────────────────

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"connector_id":       s.cfg.ConnectorID,
		"webhook_secret_set": s.cfg.WebhookSecret != "",
		"edge_url":           s.cfg.EdgeURL,
		"auto_webhook_ms":    s.cfg.AutoWebhookMS,
		"timeout_hold_ms":    s.cfg.TimeoutHold.Milliseconds(),
	})
}

func (s *Server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectorID   *string `json:"connector_id"`
		WebhookSecret *string `json:"webhook_secret"`
		AutoWebhookMS *int    `json:"auto_webhook_ms"`
		TimeoutHoldMS *int    `json:"timeout_hold_ms"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "invalid config body")
		return
	}
	s.mu.Lock()
	if in.ConnectorID != nil {
		s.cfg.ConnectorID = *in.ConnectorID
	}
	if in.WebhookSecret != nil {
		s.cfg.WebhookSecret = *in.WebhookSecret
	}
	if in.AutoWebhookMS != nil {
		s.cfg.AutoWebhookMS = *in.AutoWebhookMS
	}
	if in.TimeoutHoldMS != nil && *in.TimeoutHoldMS > 0 {
		s.cfg.TimeoutHold = time.Duration(*in.TimeoutHoldMS) * time.Millisecond
	}
	s.mu.Unlock()
	s.handleGetConfig(w, r)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("reference_id")
	path := r.URL.Query().Get("path")
	s.mu.Lock()
	out := []RecordedRequest{}
	for _, rec := range s.requests {
		if ref != "" && rec.ReferenceID != ref {
			continue
		}
		if path != "" && rec.Path != path {
			continue
		}
		out = append(out, *rec)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("payout_id")
	s.mu.Lock()
	out := []WebhookDelivery{}
	for _, d := range s.webhooks {
		if id == "" || d.PayoutID == id {
			out = append(out, d)
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminWebhook(w http.ResponseWriter, r *http.Request) {
	event := r.URL.Query().Get("event")
	if event == "" {
		event = "payout.processed"
	}
	d, err := s.SendWebhook(r.PathValue("payout_id"), event)
	switch {
	case err == errNotFound:
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case err == errNotConfigured:
		writeErr(w, http.StatusConflict, "not_configured", err.Error())
	case err != nil && d.EventID == "":
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		writeJSON(w, http.StatusBadGateway, d)
	default:
		writeJSON(w, http.StatusOK, d)
	}
}

// handleReset clears payouts, requests, modes and webhooks. The connector
// config is kept unless ?all=1.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.resetLocked(r.URL.Query().Get("all") == "1")
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

var validModes = map[string]bool{"500": true, "timeout": true, "decline": true, "429": true, "drop": true, "ok": true}

func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReferenceID string `json:"reference_id"`
		Mode        string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil || in.ReferenceID == "" || !validModes[in.Mode] {
		writeErr(w, http.StatusBadRequest, "invalid_mode", "need reference_id and mode in 500|timeout|decline|429|drop|ok")
		return
	}
	s.mu.Lock()
	s.modes[in.ReferenceID] = in.Mode
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, in)
}

// ── helpers ────────────────────────────────────────────────────────────────

func cloneHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "description": msg}, "code": code})
}

// logRequests logs method, path and query only: never bodies or headers
// (bodies carry fake PII; /_admin/config carries the webhook secret).
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/health" {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}
