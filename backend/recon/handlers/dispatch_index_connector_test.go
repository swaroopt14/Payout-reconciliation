package handlers

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// L7 / D44 (EM decision): dispatch_index.connector_id stays UUID NOT NULL.
// Relay sends the resolved tenant connector UUID in connector_id and the PSP
// slug (e.g. "razorpayx-v1") in connector_ref.

func TestDispatchIndex_ConnectorRefSlugLandsInConnectorRefColumn(t *testing.T) {
	idx := useFakeDispatchIndex(t)
	if err := HandleDispatchEvent(createdMsg(t, testConnector)); err != nil {
		t.Fatalf("DispatchCreated: %v", err)
	}
	row, ok := idx.rows[testDispatchID]
	if !ok {
		t.Fatal("dispatch_index row not written")
	}
	if row.connectorRef != "razorpayx-v1" {
		t.Fatalf("connector_ref column = %q, want payload slug razorpayx-v1", row.connectorRef)
	}
	if row.connectorID != testConnector {
		t.Fatalf("connector_id column = %q, want UUID %s", row.connectorID, testConnector)
	}
}

func TestDispatchIndex_ConnectorIDMustBeNonNilUUID(t *testing.T) {
	idx := useFakeDispatchIndex(t)
	for _, bad := range []string{
		"razorpayx-v1",
		"con_razorpay_live_1a2b3c4d",
		"00000000-0000-0000-0000-000000000000",
	} {
		if err := HandleDispatchEvent(createdMsg(t, bad)); !errors.Is(err, ErrInvalidDispatchConnectorID) {
			t.Fatalf("connector_id %q: err = %v, want ErrInvalidDispatchConnectorID", bad, err)
		}
	}
	if len(idx.rows) != 0 || idx.inserts != 0 {
		t.Fatalf("invalid connector_id wrote rows=%d inserts=%d", len(idx.rows), idx.inserts)
	}
}

// No migration may relax dispatch_index.connector_id NOT NULL.
func TestDispatchIndexMigrationsKeepConnectorIDNotNull(t *testing.T) {
	files, err := filepath.Glob("../db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	dropNotNull := regexp.MustCompile(`(?is)ALTER\s+COLUMN\s+connector_id\s+DROP\s+NOT\s+NULL`)
	var sawCreate bool
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.SplitN(string(raw), "-- +goose Down", 2)[0]
		if strings.Contains(up, "dispatch_index") && dropNotNull.MatchString(up) {
			t.Fatalf("%s drops NOT NULL on dispatch_index.connector_id", filepath.Base(f))
		}
		if regexp.MustCompile(`(?is)CREATE TABLE[^;]*dispatch_index[^;]*connector_id\s+UUID\s+NOT\s+NULL`).MatchString(up) {
			sawCreate = true
		}
	}
	if !sawCreate {
		t.Fatal("dispatch_index create migration no longer declares connector_id UUID NOT NULL")
	}
}
