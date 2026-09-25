package models

import "testing"

func TestBatchTotalAmountMinor_Exact(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "0.01": 1, "100.50": 10050, "100000.10": 10000010, "100000.1000": 10000010} {
		got, err := BatchTotalAmountMinor(in)
		if err != nil || got != want {
			t.Errorf("BatchTotalAmountMinor(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"1.005", "", "abc", "99999999999999999999"} {
		if got, err := BatchTotalAmountMinor(in); err == nil {
			t.Errorf("BatchTotalAmountMinor(%q) = %d, want error", in, got)
		}
	}
}
