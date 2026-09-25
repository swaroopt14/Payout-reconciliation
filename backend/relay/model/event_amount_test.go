package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEventTotalAmountMinorExact guards D10 on the batch event relay
// passes through: the paise total from the intent engine must survive the
// lease-decode → Kafka-encode round trip exactly, with no float in between.
func TestEventTotalAmountMinorExact(t *testing.T) {
	for _, want := range []int64{1, 10050, 10000010, 9223372036854775807} {
		lease := []byte(`{"events":[{"tenant_id":"t","batch_id":"b","total_amount_minor":` +
			jsonInt(want) + `}]}`)
		var resp struct {
			Events []BatchCanonicalizationCompletedEvent `json:"events"`
		}
		if err := json.Unmarshal(lease, &resp); err != nil {
			t.Fatalf("decode lease: %v", err)
		}
		evt := resp.Events[0]
		if evt.TotalAmountMinor == nil || *evt.TotalAmountMinor != want {
			t.Fatalf("decoded TotalAmountMinor = %v, want %d", evt.TotalAmountMinor, want)
		}
		out, err := json.Marshal(evt)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if !strings.Contains(string(out), `"total_amount_minor":`+jsonInt(want)) {
			t.Fatalf("published event %s lost exact total_amount_minor %d", out, want)
		}
	}

	// Absent upstream value stays absent — never defaulted to 0 paise.
	var evt BatchCanonicalizationCompletedEvent
	if err := json.Unmarshal([]byte(`{"tenant_id":"t","batch_id":"b"}`), &evt); err != nil {
		t.Fatal(err)
	}
	if evt.TotalAmountMinor != nil {
		t.Fatalf("TotalAmountMinor = %d for absent field, want nil", *evt.TotalAmountMinor)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
