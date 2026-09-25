package handlers

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"zord-outcome-engine/db"
)

// ─────────────────────────────────────────────────────────────────────────────
// In-memory dispatch_index emulating the SQL contract the handler relies on:
// INSERT ... ON CONFLICT (dispatch_id) DO NOTHING (1 row new, 0 on replay)
// and UPDATE ... WHERE dispatch_id [AND tenant_id] (rows affected = matches).
// ─────────────────────────────────────────────────────────────────────────────

type fakeDispatchRow struct {
	tenantID, connectorID, connectorRef, status, statusReason string
	refHashes                                                 any
}

type fakeDispatchIndex struct {
	mu        sync.Mutex
	rows      map[string]*fakeDispatchRow
	inserts   int
	failWrite error
}

var (
	fakeDispatchDriverOnce sync.Once
	fakeDispatchIndexes    sync.Map
)

type fakeDispatchDriver struct{}

func (fakeDispatchDriver) Open(dsn string) (driver.Conn, error) {
	v, ok := fakeDispatchIndexes.Load(dsn)
	if !ok {
		return nil, errors.New("unknown dsn")
	}
	return &fakeDispatchConn{idx: v.(*fakeDispatchIndex)}, nil
}

type fakeDispatchConn struct{ idx *fakeDispatchIndex }

func (c *fakeDispatchConn) Prepare(q string) (driver.Stmt, error) {
	return &fakeDispatchStmt{q: q, idx: c.idx}, nil
}
func (c *fakeDispatchConn) Close() error              { return nil }
func (c *fakeDispatchConn) Begin() (driver.Tx, error) { return nil, errors.New("no tx") }

type fakeDispatchStmt struct {
	q   string
	idx *fakeDispatchIndex
}

func (s *fakeDispatchStmt) Close() error  { return nil }
func (s *fakeDispatchStmt) NumInput() int { return -1 }
func (s *fakeDispatchStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("query not supported")
}

func str(v driver.Value) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	}
	return ""
}

func (s *fakeDispatchStmt) Exec(args []driver.Value) (driver.Result, error) {
	idx := s.idx
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.failWrite != nil {
		return nil, idx.failWrite
	}
	q := strings.Join(strings.Fields(s.q), " ")
	switch {
	case strings.HasPrefix(q, "INSERT INTO dispatch_index"):
		if !strings.Contains(q, "ON CONFLICT (dispatch_id) DO NOTHING") {
			return nil, errors.New("insert without ON CONFLICT (dispatch_id) DO NOTHING")
		}
		id := str(args[0])
		if _, ok := idx.rows[id]; ok {
			return driver.RowsAffected(0), nil
		}
		idx.inserts++
		idx.rows[id] = &fakeDispatchRow{
			tenantID: str(args[3]), connectorID: str(args[5]),
			refHashes: args[8], connectorRef: str(args[9]), status: "CREATED",
		}
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE dispatch_index SET status"):
		r, ok := idx.rows[str(args[2])]
		if !ok || r.tenantID != str(args[3]) {
			return driver.RowsAffected(0), nil
		}
		r.status, r.statusReason = str(args[0]), str(args[1])
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE dispatch_index SET attempt_count"):
		if _, ok := idx.rows[str(args[2])]; !ok {
			return driver.RowsAffected(0), nil
		}
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE dispatch_index SET provider_attempt_id"):
		if _, ok := idx.rows[str(args[1])]; !ok {
			return driver.RowsAffected(0), nil
		}
		return driver.RowsAffected(1), nil
	}
	return nil, errors.New("unexpected SQL: " + q)
}

func useFakeDispatchIndex(t *testing.T) *fakeDispatchIndex {
	t.Helper()
	fakeDispatchDriverOnce.Do(func() { sql.Register("recon-fake-dispatch-index", fakeDispatchDriver{}) })
	idx := &fakeDispatchIndex{rows: map[string]*fakeDispatchRow{}}
	fakeDispatchIndexes.Store(t.Name(), idx)
	conn, err := sql.Open("recon-fake-dispatch-index", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	prev := db.DB
	db.DB = conn
	t.Cleanup(func() {
		db.DB = prev
		_ = conn.Close()
		fakeDispatchIndexes.Delete(t.Name())
	})
	return idx
}

const (
	testTenant     = "11111111-1111-4111-8111-111111111111"
	testDispatchID = "d1d1d1d1-d1d1-4d1d-8d1d-d1d1d1d1d1d1"
	testConnector  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func dispatchMsg(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	p, _ := json.Marshal(payload)
	b, _ := json.Marshal(map[string]any{
		"event_id":       "e-" + eventType,
		"event_type":     eventType,
		"tenant_id":      testTenant,
		"intent_id":      "22222222-2222-4222-8222-222222222222",
		"contract_id":    "33333333-3333-4333-8333-333333333333",
		"trace_id":       "44444444-4444-4444-8444-444444444444",
		"schema_version": "v1",
		"payload":        json.RawMessage(p),
	})
	return b
}

func createdMsg(t *testing.T, connectorID string) []byte {
	return dispatchMsg(t, "DispatchCreated", map[string]any{
		"dispatch_id":   testDispatchID,
		"connector_id":  connectorID,
		"connector_ref": "razorpayx-v1",
		"corridor_id":   "IMPS",
		"attempt_count": 1,
		"correlation_carriers": map[string]string{
			"reference_id": testDispatchID, "narration": "ZRD:33333333",
		},
	})
}

// TestDispatchIndex_EveryRelayDispatchCreatesExactlyOneRow is the L7 guard:
// a DispatchCreated writes one dispatch_index row; a replay keeps one row;
// follow-up status events update that row and never insert.
func TestDispatchIndex_EveryRelayDispatchCreatesExactlyOneRow(t *testing.T) {
	idx := useFakeDispatchIndex(t)

	if err := HandleDispatchEvent(createdMsg(t, testConnector)); err != nil {
		t.Fatalf("DispatchCreated: %v", err)
	}
	if err := HandleDispatchEvent(createdMsg(t, testConnector)); err != nil {
		t.Fatalf("DispatchCreated replay must be idempotent, got %v", err)
	}
	if len(idx.rows) != 1 || idx.inserts != 1 {
		t.Fatalf("rows=%d inserts=%d, want exactly one", len(idx.rows), idx.inserts)
	}
	row := idx.rows[testDispatchID]
	if row.connectorID != testConnector || row.connectorRef != "razorpayx-v1" {
		t.Fatalf("row connector_id=%q connector_ref=%q", row.connectorID, row.connectorRef)
	}
	if _, isSlice := row.refHashes.([]string); isSlice {
		t.Fatal("provider_ref_hashes passed as raw []string; must be pq.Array")
	}

	steps := []struct {
		eventType string
		payload   map[string]any
		status    string
		reason    string
	}{
		{"DispatchGovernanceEvaluated", map[string]any{"dispatch_id": testDispatchID, "decision": "HOLD_DISPATCH", "reason_codes": []string{"CIRCUIT_BREAKER_OPEN"}}, "HELD", "CIRCUIT_BREAKER_OPEN"},
		{"DispatchHeld", map[string]any{"dispatch_id": testDispatchID, "reason": "manual hold"}, "HELD", "manual hold"},
		{"DispatchAwaitingProviderSignal", map[string]any{"dispatch_id": testDispatchID, "reason": "timeout"}, "AWAITING_PROVIDER_SIGNAL", "timeout"},
		{"DispatchRetryScheduled", map[string]any{"dispatch_id": testDispatchID, "retry_class": "RETRYABLE_AFTER_BACKOFF", "failure_reason": "CRASH_RECOVERY_NO_PSP_RECORD"}, "RETRY_SCHEDULED", "RETRYABLE_AFTER_BACKOFF: CRASH_RECOVERY_NO_PSP_RECORD"},
		{"DispatchFailed", map[string]any{"dispatch_id": testDispatchID, "reason": "AMOUNT_INVALID: x"}, "FAILED", "AMOUNT_INVALID: x"},
	}
	for _, st := range steps {
		if err := HandleDispatchEvent(dispatchMsg(t, st.eventType, st.payload)); err != nil {
			t.Fatalf("%s: %v", st.eventType, err)
		}
		if row.status != st.status || row.statusReason != st.reason {
			t.Fatalf("%s: status=%q reason=%q, want %q %q", st.eventType, row.status, row.statusReason, st.status, st.reason)
		}
	}
	// ALLOW is not a state change.
	if err := HandleDispatchEvent(dispatchMsg(t, "DispatchGovernanceEvaluated", map[string]any{"dispatch_id": testDispatchID, "decision": "ALLOW_DISPATCH"})); err != nil {
		t.Fatal(err)
	}
	if row.status != "FAILED" {
		t.Fatalf("ALLOW changed status to %q", row.status)
	}
	if len(idx.rows) != 1 || idx.inserts != 1 {
		t.Fatalf("status events inserted rows: rows=%d inserts=%d", len(idx.rows), idx.inserts)
	}

	// Status for an unknown dispatch is an error (never an insert)...
	unknown := map[string]any{"dispatch_id": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "reason": "x"}
	if err := HandleDispatchEvent(dispatchMsg(t, "DispatchFailed", unknown)); !errors.Is(err, ErrDispatchIndexRowMissing) {
		t.Fatalf("unknown dispatch status err = %v, want ErrDispatchIndexRowMissing", err)
	}
	// ...except a CONNECTOR_UNRESOLVED hold, which relay emits before any DispatchCreated.
	held := map[string]any{"dispatch_id": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "decision": "HOLD_DISPATCH", "reason_codes": []string{"CONNECTOR_UNRESOLVED"}}
	if err := HandleDispatchEvent(dispatchMsg(t, "DispatchGovernanceEvaluated", held)); err != nil {
		t.Fatalf("connector-unresolved hold: %v", err)
	}
	if len(idx.rows) != 1 {
		t.Fatalf("rows=%d after unknown-dispatch events", len(idx.rows))
	}
}

func TestDispatchIndex_RejectsSlugOrNilConnectorID(t *testing.T) {
	idx := useFakeDispatchIndex(t)
	for _, bad := range []string{"razorpayx-v1", "00000000-0000-0000-0000-000000000000", ""} {
		if err := HandleDispatchEvent(createdMsg(t, bad)); !errors.Is(err, ErrInvalidDispatchConnectorID) {
			t.Fatalf("connector_id %q: err = %v, want ErrInvalidDispatchConnectorID", bad, err)
		}
	}
	if len(idx.rows) != 0 {
		t.Fatalf("rows written for invalid connector_id: %d", len(idx.rows))
	}
}

// TestDispatchIndex_InsertErrorNotLoggedAsSuccess: the old handler logged
// "Dispatch index inserted" before checking the INSERT error.
func TestDispatchIndex_InsertErrorNotLoggedAsSuccess(t *testing.T) {
	idx := useFakeDispatchIndex(t)
	idx.failWrite = errors.New(`pq: invalid input syntax for type uuid: "razorpayx-v1"`)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	err := HandleDispatchEvent(createdMsg(t, testConnector))
	if err == nil {
		t.Fatal("insert error was swallowed")
	}
	if strings.Contains(buf.String(), "Dispatch index inserted") {
		t.Fatalf("insert failure logged as success:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Dispatch index insert failed") {
		t.Fatalf("insert failure not logged:\n%s", buf.String())
	}
}
