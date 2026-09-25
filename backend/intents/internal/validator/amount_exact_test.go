package validator

import "testing"

func TestValidateAmount_ExactPaise(t *testing.T) {
	for _, in := range []string{"100", "100.5", "100.50", "0.01", "100000.10"} {
		if err := validateAmount(in); err != nil {
			t.Errorf("validateAmount(%q) = %v, want nil", in, err)
		}
	}
	// "15e-4" (= 0.0015) has no '.', so the old scale check let it through.
	for _, in := range []string{"1.005", "15e-4", "1e-3", "0", "-1", "abc"} {
		if err := validateAmount(in); err == nil {
			t.Errorf("validateAmount(%q) = nil, want error", in)
		}
	}
}
