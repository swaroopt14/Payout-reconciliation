package services

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func TestAmountMinorExact(t *testing.T) {
	valid := map[string]int64{
		"100":                  10000,
		"100.5":                10050,
		"100.50":               10050,
		"0.01":                 1,
		"100000.10":            10000010,
		"-5.25":                -525, // sign preserved for the NEGATIVE_AMOUNT_NOT_ALLOWED path
		"92233720368547758.07": 9223372036854775807,
	}
	for in, want := range valid {
		got, err := amountMinorExact(decimal.RequireFromString(in))
		if err != nil || got != want {
			t.Errorf("amountMinorExact(%s) = %d, %v; want %d, nil", in, got, err, want)
		}
	}

	invalid := map[string]error{
		"1.005":                errAmountSubPaise,
		"15e-4":                errAmountSubPaise,
		"92233720368547758.08": errAmountOverflow,
		"99999999999999999999": errAmountOverflow,
	}
	for in, wantErr := range invalid {
		if got, err := amountMinorExact(decimal.RequireFromString(in)); !errors.Is(err, wantErr) {
			t.Errorf("amountMinorExact(%s) = %d, %v; want %v", in, got, err, wantErr)
		}
	}
}

func TestParseAmount_RejectsSubPaise(t *testing.T) {
	for _, in := range []string{"1.005", "15e-4", "", "abc"} {
		if _, err := parseAmount(in); err == nil {
			t.Errorf("parseAmount(%q) = nil error, want rejection", in)
		}
	}
	d, err := parseAmount(" 100000.10 ")
	if err != nil || !d.Equal(decimal.RequireFromString("100000.10")) {
		t.Errorf("parseAmount(100000.10) = %s, %v", d, err)
	}
}
