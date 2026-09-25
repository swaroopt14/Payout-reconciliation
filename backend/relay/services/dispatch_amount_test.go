package services

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"zord-relay/internal/railrouter"
	"zord-relay/logger"
	"zord-relay/model"
	"zord-relay/psp"

	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────────────────
// Minimal in-memory database/sql driver. It accepts every statement, reports
// one affected row for Exec, and returns no rows for Query. That is enough to
// drive DispatchLoop's real atomicStep/repo code without a Postgres, while
// recording the SQL that ran so tests can assert which dispatch path fired.
// ─────────────────────────────────────────────────────────────────────────────

type amountTestDBRecorder struct {
	mu    sync.Mutex
	execs []string
	args  [][]driver.Value
}

func (r *amountTestDBRecorder) record(q string, args []driver.Value) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execs = append(r.execs, q)
	r.args = append(r.args, args)
}

// outboxPayloads returns the JSON payload (arg $8) of every relay_outbox
// INSERT, keyed by event type (arg $2), in order.
func (r *amountTestDBRecorder) outboxPayloads() map[string][][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string][][]byte{}
	for i, q := range r.execs {
		if !strings.Contains(q, "INSERT INTO relay_outbox") || len(r.args[i]) < 8 {
			continue
		}
		typ, _ := r.args[i][1].(string)
		b, _ := r.args[i][7].([]byte)
		out[typ] = append(out[typ], b)
	}
	return out
}

func (r *amountTestDBRecorder) anyArgEquals(v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, args := range r.args {
		for _, a := range args {
			if s, ok := a.(string); ok && s == v {
				return true
			}
		}
	}
	return false
}

func (r *amountTestDBRecorder) anyExecContains(sub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.execs {
		if strings.Contains(q, sub) {
			return true
		}
	}
	return false
}

var (
	amountTestDriverOnce sync.Once
	amountTestRecorders  sync.Map // dsn -> *amountTestDBRecorder
)

type amountTestDriver struct{}

func (amountTestDriver) Open(dsn string) (driver.Conn, error) {
	rec, ok := amountTestRecorders.Load(dsn)
	if !ok {
		return nil, errors.New("amount test driver: unknown dsn " + dsn)
	}
	return &amountTestConn{rec: rec.(*amountTestDBRecorder)}, nil
}

type amountTestConn struct{ rec *amountTestDBRecorder }

func (c *amountTestConn) Prepare(q string) (driver.Stmt, error) {
	return &amountTestStmt{q: q, rec: c.rec}, nil
}
func (c *amountTestConn) Close() error              { return nil }
func (c *amountTestConn) Begin() (driver.Tx, error) { return amountTestTx{}, nil }

type amountTestTx struct{}

func (amountTestTx) Commit() error   { return nil }
func (amountTestTx) Rollback() error { return nil }

type amountTestStmt struct {
	q   string
	rec *amountTestDBRecorder
}

func (s *amountTestStmt) Close() error  { return nil }
func (s *amountTestStmt) NumInput() int { return -1 }
func (s *amountTestStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.rec.record(s.q, args)
	return driver.RowsAffected(1), nil
}
func (s *amountTestStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return amountTestRows{}, nil
}

type amountTestRows struct{}

func (amountTestRows) Columns() []string           { return nil }
func (amountTestRows) Close() error                { return nil }
func (amountTestRows) Next(_ []driver.Value) error { return io.EOF }

func newAmountTestDB(t *testing.T) (*sql.DB, *amountTestDBRecorder) {
	t.Helper()
	amountTestDriverOnce.Do(func() { sql.Register("relay-amount-test", amountTestDriver{}) })
	rec := &amountTestDBRecorder{}
	amountTestRecorders.Store(t.Name(), rec)
	t.Cleanup(func() { amountTestRecorders.Delete(t.Name()) })
	db, err := sql.Open("relay-amount-test", t.Name())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, rec
}

// capturingPSPClient wraps the real DemoClient and records every request.
type capturingPSPClient struct {
	inner *psp.DemoClient
	mu    sync.Mutex
	reqs  []psp.PayoutRequest
}

func (c *capturingPSPClient) Do(ctx context.Context, req psp.PayoutRequest) (psp.PayoutResponse, error) {
	c.mu.Lock()
	c.reqs = append(c.reqs, req)
	c.mu.Unlock()
	return c.inner.Do(ctx, req)
}

func (c *capturingPSPClient) QueryByReference(ctx context.Context, ref string) (*psp.PayoutResponse, error) {
	return c.inner.QueryByReference(ctx, ref)
}

func (c *capturingPSPClient) requests() []psp.PayoutRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]psp.PayoutRequest, len(c.reqs))
	copy(out, c.reqs)
	return out
}

type fakeTokenClient struct{}

func (fakeTokenClient) Detokenize(_ context.Context, _ DetokenizeRequest) (*DetokenizeResponse, error) {
	return &DetokenizeResponse{AccountNumber: "000111222333", Name: "Test Beneficiary", IFSC: "HDFC0000001"}, nil
}

// capturingRouter records the router request and returns a fixed decision.
type capturingRouter struct {
	mu   sync.Mutex
	reqs []railrouter.Request
}

func (r *capturingRouter) Route(_ context.Context, in railrouter.Request) (railrouter.Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, in)
	return railrouter.Decision{PSP: "razorpay", ConnectorID: "razorpayx-v1", Rail: "IMPS"}, nil
}

func ensureTestLogger() {
	if logger.Logger == nil {
		logger.Logger = zap.NewNop()
	}
}

func newAmountTestLoop(t *testing.T) (*DispatchLoop, *capturingPSPClient, *capturingRouter, *amountTestDBRecorder) {
	t.Helper()
	ensureTestLogger()
	db, rec := newAmountTestDB(t)
	pspClient := &capturingPSPClient{inner: psp.NewDemoClient("http://psp.invalid", 1)}
	loop := NewDispatchLoop(db, NewRelayOutboxRepo(db), NewDispatchRepo(db), pspClient, fakeTokenClient{}, nil,
		&DispatchLoopConfig{ConnectorID: "razorpayx-v1", CorridorID: "IMPS"})
	router := &capturingRouter{}
	loop.SetRouter(router)
	resolver, err := ParseStaticConnectorMap(`{"` + amountTestTenant + `":{"razorpayx-v1":"` + amountTestConnectorUUID + `"}}`)
	if err != nil {
		t.Fatalf("connector map: %v", err)
	}
	loop.SetConnectorResolver(resolver)
	return loop, pspClient, router, rec
}

const (
	amountTestTenant        = "11111111-1111-4111-8111-111111111111"
	amountTestConnectorUUID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func amountTestEvent(t *testing.T, amount string) model.OutboxEvent {
	t.Helper()
	payload := model.OutboxPayload{
		IntentID:   "intent-1",
		TenantID:   amountTestTenant,
		TraceID:    "trace-1",
		IntentType: "PAYOUT",
		PIITokens:  model.OutboxPIITokens{AccountNumber: "tok-acc", Name: "tok-name", IFSC: "tok-ifsc"},
		Beneficiary: model.OutboxBeneficiary{
			Instrument: model.OutboxInstrument{Kind: "BANK"},
		},
		Amount:   amount,
		Currency: "INR",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return model.OutboxEvent{
		EventID:       "evt-1",
		TraceID:       "trace-1",
		TenantID:      amountTestTenant,
		AggregateID:   "intent-1",
		ContractID:    "contract-1",
		SchemaVersion: "v1",
		Payload:       b,
	}
}

// TestDispatch_PSPRequestAmountIsPaise drives the real processEvent →
// runSteps2to5 path and asserts the PSP PayoutRequest.Amount is exact paise
// (L3, L6: ₹1 must be exactly 100 paise on the provider request). The router
// must see the same paise value.
func TestDispatch_PSPRequestAmountIsPaise(t *testing.T) {
	cases := []struct {
		amount string
		want   int64
	}{
		{"100.50", 10050},
		{"0.01", 1},
		{"100000.10", 10000010},
		{"100", 10000},
		{"1", 100},
	}
	for _, tc := range cases {
		t.Run(tc.amount, func(t *testing.T) {
			loop, pspClient, router, _ := newAmountTestLoop(t)
			if ok := loop.processEvent(context.Background(), 1, amountTestEvent(t, tc.amount)); !ok {
				t.Fatalf("processEvent returned false")
			}
			reqs := pspClient.requests()
			if len(reqs) != 1 {
				t.Fatalf("PSP requests = %d, want 1", len(reqs))
			}
			if reqs[0].Amount != tc.want {
				t.Fatalf("PSP request amount for %q = %d paise, want %d", tc.amount, reqs[0].Amount, tc.want)
			}
			if len(router.reqs) != 1 || router.reqs[0].AmountMinor != tc.want {
				t.Fatalf("router AmountMinor for %q = %+v, want %d", tc.amount, router.reqs, tc.want)
			}
		})
	}
}

// TestDispatch_InvalidAmountSendsNoPSPRequest proves an amount that cannot be
// converted exactly never reaches the PSP and the dispatch is failed
// terminally through the existing markFailedTerminal path.
func TestDispatch_InvalidAmountSendsNoPSPRequest(t *testing.T) {
	for _, amount := range []string{"1.005", "", "abc", "-1", "1e3", "0", "99999999999999999999"} {
		t.Run(amount, func(t *testing.T) {
			loop, pspClient, router, rec := newAmountTestLoop(t)
			if ok := loop.processEvent(context.Background(), 1, amountTestEvent(t, amount)); !ok {
				t.Fatalf("processEvent returned false")
			}
			if n := len(pspClient.requests()); n != 0 {
				t.Fatalf("PSP requests = %d for invalid amount %q, want 0", n, amount)
			}
			if !rec.anyExecContains("FAILED_TERMINAL") {
				t.Fatalf("invalid amount %q did not mark the dispatch FAILED_TERMINAL", amount)
			}
			if rec.anyExecContains("status = 'SENT'") {
				t.Fatalf("invalid amount %q reached AttemptSent", amount)
			}
			// "0" parses fine, so the router may be consulted; every
			// unparseable amount must skip the router.
			if amount != "0" && len(router.reqs) != 0 {
				t.Fatalf("router consulted for unparseable amount %q", amount)
			}
		})
	}
}

func TestAmountMinorFromDecimalString(t *testing.T) {
	valid := []struct {
		in   string
		want int64
	}{
		{"100", 10000},
		{"100.5", 10050},
		{"100.50", 10050},
		{"0.01", 1},
		{"0", 0},
		{" 42.07 ", 4207},
		{"100000.10", 10000010},
		{"1.500", 150}, // trailing zeros beyond paise are exact
		{"92233720368547758.07", 9223372036854775807}, // MaxInt64 paise
	}
	for _, tc := range valid {
		got, err := amountMinorFromDecimalString(tc.in)
		if err != nil {
			t.Errorf("amountMinorFromDecimalString(%q) error = %v, want %d", tc.in, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("amountMinorFromDecimalString(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}

	invalid := []struct {
		in      string
		wantErr error
	}{
		{"1.005", errAmountSubPaise},
		{"0.001", errAmountSubPaise},
		{"", errAmountEmpty},
		{"   ", errAmountEmpty},
		{"abc", errAmountSyntax},
		{"-1", errAmountNegative},
		{"-0.01", errAmountNegative},
		{"1e3", errAmountSyntax},
		{"1E3", errAmountSyntax},
		{"+1", errAmountSyntax},
		{"1,000", errAmountSyntax},
		{".5", errAmountSyntax},
		{"100.", errAmountSyntax},
		{"1.2.3", errAmountSyntax},
		{"NaN", errAmountSyntax},
		{"92233720368547758.08", errAmountOverflow},
		{"99999999999999999999", errAmountOverflow},
	}
	for _, tc := range invalid {
		got, err := amountMinorFromDecimalString(tc.in)
		if err == nil {
			t.Errorf("amountMinorFromDecimalString(%q) = %d, want error %v", tc.in, got, tc.wantErr)
			continue
		}
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("amountMinorFromDecimalString(%q) error = %v, want %v", tc.in, err, tc.wantErr)
		}
	}
}

// TestAmountMinorFromMajor_RouterPathExact guards L3: the router conversion
// used float64 (int64(f*100+0.5)). It must be exact for ₹0.01 and ₹1,00,000.10.
func TestAmountMinorFromMajor_RouterPathExact(t *testing.T) {
	for in, want := range map[string]int64{"0.01": 1, "100000.10": 10000010, "100.50": 10050} {
		got, err := amountMinorFromMajor(in)
		if err != nil || got != want {
			t.Errorf("amountMinorFromMajor(%q) = %d, %v; want %d, nil", in, got, err, want)
		}
	}
	if _, err := amountMinorFromMajor("1.005"); !errors.Is(err, errAmountSubPaise) {
		t.Errorf("amountMinorFromMajor(\"1.005\") error = %v, want sub-paise rejection", err)
	}
}
