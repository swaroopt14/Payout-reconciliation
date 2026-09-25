package models

import (
	"fmt"
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

var (
	batchPaisePerRupee = decimal.NewFromInt(100)
	batchMaxInt64      = decimal.NewFromInt(math.MaxInt64)
	batchMinInt64      = decimal.NewFromInt(math.MinInt64)
)

// BatchTotalAmountMinor converts a NUMERIC rupee value rendered as text
// (e.g. "100000.10") into exact int64 paise without float64. Sub-paise values
// and int64 overflow are errors, never rounded.
func BatchTotalAmountMinor(text string) (int64, error) {
	d, err := decimal.NewFromString(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("total_amount %q: %w", text, err)
	}
	m := d.Mul(batchPaisePerRupee)
	if !m.Equal(m.Truncate(0)) {
		return 0, fmt.Errorf("total_amount %q has sub-paise precision", text)
	}
	if m.GreaterThan(batchMaxInt64) || m.LessThan(batchMinInt64) {
		return 0, fmt.Errorf("total_amount %q overflows int64 paise", text)
	}
	return m.IntPart(), nil
}
