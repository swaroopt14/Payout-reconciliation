package services

import (
	"context"
	"log"

	"zord-intent-engine/internal/persistence"

	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// 4.2.7: the daily-limit reservation (R-05) was atomic and currency-safe but
// otherwise invisible — no metric anywhere in this codebase exported
// accept/hold volume, no dedicated audit trail distinguished "genuinely over
// limit" from "usage-tracking infra failed and we held for safety", and
// approvals left no record beyond the row's own governance_state flipping.
// This file adds exactly those three things around the existing
// ReserveIfWithinLimit call sites in intent_service.go, without changing the
// reservation decision logic itself.

// PolicyFlagDailyLimitExceeded marks a hold caused by a genuine over-limit
// projection — the reservation succeeded and correctly reported the tenant
// would exceed today's configured limit.
const PolicyFlagDailyLimitExceeded = "TENANT_DAILY_LIMIT_EXCEEDED"

// PolicyFlagDailyLimitReservationFailed marks a fail-safe hold caused by the
// usage-tracking reservation itself erroring (DB/infra failure), not by the
// tenant actually being over limit. Kept distinct from
// PolicyFlagDailyLimitExceeded so an infra outage that fails every
// reservation shows up as a distinct spike instead of being indistinguishable
// from ordinary high-volume holds on a dashboard or in DLQ analytics.
const PolicyFlagDailyLimitReservationFailed = "TENANT_DAILY_LIMIT_RESERVATION_FAILED"

var (
	dailyLimitMeter = otel.Meter("zord-intent-engine/daily-limit")

	dailyLimitAcceptedAmount, _ = dailyLimitMeter.Float64Counter(
		"zord.intent_engine.daily_limit.accepted_amount",
		metric.WithDescription("Sum of intent amounts accepted against the tenant daily limit, by currency"),
	)
	dailyLimitHeldAmount, _ = dailyLimitMeter.Float64Counter(
		"zord.intent_engine.daily_limit.held_amount",
		metric.WithDescription("Sum of intent amounts held for review because they would exceed the tenant daily limit, by currency"),
	)
	dailyLimitReservationFailures, _ = dailyLimitMeter.Int64Counter(
		"zord.intent_engine.daily_limit.reservation_failures",
		metric.WithDescription("Count of tenant daily-usage reservation attempts that errored and fell back to a fail-safe hold, by currency"),
	)
	dailyLimitApprovals, _ = dailyLimitMeter.Int64Counter(
		"zord.intent_engine.daily_limit.approvals",
		metric.WithDescription("Count of held-intent approval attempts, by currency and outcome (accepted/still_held)"),
	)
)

// recordDailyLimitReservation emits the accepted/held-value metrics and a
// structured audit log line for one ReserveIfWithinLimit outcome on the main
// ingest path. amount is this single intent's amount, not a running total.
func recordDailyLimitReservation(ctx context.Context, tenantID, currency, businessDate, decision string, amount decimal.Decimal) {
	// OBSERVABILITY ONLY (D10): OTel counters take float64, so this lossy
	// conversion exists solely to emit metrics. amountF must never feed a
	// money decision (limits, holds, matching, dispatch); those use the exact
	// decimal / int64 paise values.
	amountF, _ := amount.Float64()
	attrs := metric.WithAttributes(attribute.String("currency", currency))

	switch decision {
	case persistence.DailyLimitDecisionAccept:
		dailyLimitAcceptedAmount.Add(ctx, amountF, attrs)
	case persistence.DailyLimitDecisionRequiresReview:
		dailyLimitHeldAmount.Add(ctx, amountF, attrs)
	}

	log.Printf("daily_limit_reservation tenant=%s currency=%s business_date=%s amount=%s decision=%s",
		tenantID, currency, businessDate, amount.String(), decision)
}

// recordDailyLimitReservationFailure emits the reservation-failure metric
// and a structured audit log line — this is the case where the tenant's
// actual standing against the limit is unknown, and the caller has already
// decided to fail safe (hold for review) rather than risk an unverified
// amount passing as ACCEPTED.
func recordDailyLimitReservationFailure(ctx context.Context, tenantID, currency, businessDate string, amount decimal.Decimal, cause error) {
	dailyLimitReservationFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("currency", currency)))

	log.Printf("⚠️ daily_limit_reservation_failed tenant=%s currency=%s business_date=%s amount=%s error=%v — holding for review (fail-safe, not fail-open)",
		tenantID, currency, businessDate, amount.String(), cause)
}

// recordDailyLimitApproval emits the approval-audit metric and log line for
// ApproveHeldIntent's re-check outcome.
func recordDailyLimitApproval(ctx context.Context, tenantID, intentID, currency, businessDate, decision string) {
	outcome := "still_held"
	if decision == persistence.DailyLimitDecisionAccept {
		outcome = "accepted"
	}
	dailyLimitApprovals.Add(ctx, 1, metric.WithAttributes(
		attribute.String("currency", currency),
		attribute.String("outcome", outcome),
	))

	log.Printf("daily_limit_approval tenant=%s intent=%s currency=%s business_date=%s decision=%s",
		tenantID, intentID, currency, businessDate, decision)
}

// DailyLimitPolicyFlagFor returns the policy flag that should be recorded
// for a REQUIRES_REVIEW daily-limit hold, distinguishing a genuine
// over-limit projection from a fail-safe hold caused by a reservation error.
func DailyLimitPolicyFlagFor(reservationErrored bool) string {
	if reservationErrored {
		return PolicyFlagDailyLimitReservationFailed
	}
	return PolicyFlagDailyLimitExceeded
}
