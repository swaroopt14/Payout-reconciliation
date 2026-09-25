package services

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Errors returned by amountMinorFromDecimalString. They are sentinel values so
// callers and tests can tell the rejection reasons apart with errors.Is.
var (
	errAmountEmpty    = errors.New("amount is empty")
	errAmountNegative = errors.New("amount must not be negative")
	errAmountSyntax   = errors.New("amount is not a plain decimal number")
	errAmountSubPaise = errors.New("amount has more precision than one paisa")
	errAmountOverflow = errors.New("amount overflows int64 paise")
)

// amountMinorFromDecimalString converts a major-unit decimal string (rupees,
// e.g. "100.50") into exact int64 minor units (paise, e.g. 10050).
//
// It is the single rupee→paise conversion for relay (D10, L3, L6). It never
// uses float64. Rules:
//   - surrounding whitespace is trimmed;
//   - the value must be digits, optionally followed by '.' and at least one
//     digit ("100", "100.5", "100.50", "0.01");
//   - a sign, exponent notation, thousands separators, a bare "." / "100." /
//     ".5", or any other character is rejected;
//   - negative amounts are rejected (a payout amount cannot be negative);
//   - fractional digits beyond the second must all be zero ("1.500" is exactly
//     150 paise); any non-zero sub-paise digit ("1.005") is rejected, never
//     rounded or truncated;
//   - a result that does not fit in int64 is rejected.
func amountMinorFromDecimalString(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errAmountEmpty
	}
	if s[0] == '-' {
		return 0, fmt.Errorf("%w: %q", errAmountNegative, s)
	}

	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if intPart == "" || (hasDot && fracPart == "") {
		return 0, fmt.Errorf("%w: %q", errAmountSyntax, s)
	}
	if !allASCIIDigits(intPart) || !allASCIIDigits(fracPart) {
		return 0, fmt.Errorf("%w: %q", errAmountSyntax, s)
	}

	// Paise digits: first two fractional digits, right-padded with zeros.
	// Anything beyond must be zero.
	var paise int64
	for i := 0; i < 2; i++ {
		paise *= 10
		if i < len(fracPart) {
			paise += int64(fracPart[i] - '0')
		}
	}
	if len(fracPart) > 2 && strings.Trim(fracPart[2:], "0") != "" {
		return 0, fmt.Errorf("%w: %q", errAmountSubPaise, s)
	}

	var rupees int64
	for i := 0; i < len(intPart); i++ {
		d := int64(intPart[i] - '0')
		if rupees > (math.MaxInt64-d)/10 {
			return 0, fmt.Errorf("%w: %q", errAmountOverflow, s)
		}
		rupees = rupees*10 + d
	}
	if rupees > (math.MaxInt64-paise)/100 {
		return 0, fmt.Errorf("%w: %q", errAmountOverflow, s)
	}
	return rupees*100 + paise, nil
}

func allASCIIDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
