package services

import (
	"errors"
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

var (
	errAmountSubPaise = errors.New("amount has more precision than one paisa")
	errAmountOverflow = errors.New("amount overflows int64 paise")
)

var (
	paisePerRupee = decimal.NewFromInt(100)
	maxInt64Dec   = decimal.NewFromInt(math.MaxInt64)
	minInt64Dec   = decimal.NewFromInt(math.MinInt64)
)

// amountMinorExact converts a major-unit decimal (rupees) to exact int64 minor
// units (paise) without float64 (D10, L3). Unlike d.Mul(100).IntPart(), it
// rejects sub-paise values (e.g. 1.005) instead of truncating them, and
// rejects values that do not fit in int64. The sign is preserved so negative
// amounts still reach the existing NEGATIVE_AMOUNT_NOT_ALLOWED governance path.
func amountMinorExact(d decimal.Decimal) (int64, error) {
	m := d.Mul(paisePerRupee)
	if !m.Equal(m.Truncate(0)) {
		return 0, fmt.Errorf("%w: %s", errAmountSubPaise, d.String())
	}
	if m.GreaterThan(maxInt64Dec) || m.LessThan(minInt64Dec) {
		return 0, fmt.Errorf("%w: %s", errAmountOverflow, d.String())
	}
	return m.IntPart(), nil
}
