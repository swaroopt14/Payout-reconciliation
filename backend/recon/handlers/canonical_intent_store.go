package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"zord-outcome-engine/models"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func insertCanonicalIntent(ctx context.Context, tx *sql.Tx, intent models.CanonicalIntent) (sql.Result, error) {
	return tx.ExecContext(ctx, `
		INSERT INTO canonical_intents (
			intent_id, tenant_id, trace_id, contract_id,
			client_payout_ref, client_batch_ref, business_idempotency_key,
			amount, currency_code,
			intended_execution_at, payout_type, provider_hint, corridor,
			proof_readiness_score, matchability_score,
			canonical_hash, governance_state,
			source_row_num,
			created_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19
		) ON CONFLICT (intent_id) DO NOTHING`,
		intent.IntentID, intent.TenantID, intent.TraceID, intent.ContractID,
		intent.ClientPayoutRef, intent.ClientBatchRef, intent.BusinessIdempotencyKey,
		intent.Amount, intent.CurrencyCode,
		intent.IntendedExecutionAt, intent.PayoutType, intent.ProviderHint, intent.Corridor,
		intent.ProofReadinessScore, intent.MatchabilityScore,
		intent.CanonicalHash, intent.GovernanceState,
		intent.SourceRowNum,
		intent.CreatedAt,
	)
}

func validateAmount(amount string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(amount)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("amount is empty")
	}
	val, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid amount format: %w", err)
	}
	return val, nil
}

func canonicalIntentFromPayload(payload models.IntentPayload, traceID string) (models.CanonicalIntent, error) {
	intentID, err := parseRequiredUUID(payload.IntentID, "intent_id")
	if err != nil {
		return models.CanonicalIntent{}, err
	}
	tenantID, err := parseRequiredUUID(payload.TenantID, "tenant_id")
	if err != nil {
		return models.CanonicalIntent{}, err
	}
	var parsedTraceID *uuid.UUID
	if traceID != "" {
		if t, err := uuid.Parse(traceID); err == nil {
			parsedTraceID = &t
		}
	}
	contractID, _ := uuid.Parse(payload.ContractID)

	amount, err := validateAmount(payload.Amount)
	if err != nil {
		return models.CanonicalIntent{}, err
	}
	if strings.TrimSpace(payload.Currency) == "" {
		return models.CanonicalIntent{}, fmt.Errorf("currency is required")
	}
	createdAt := payload.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	var payoutType *string
	if payload.IntentType != "" {
		payoutType = &payload.IntentType
	}
	intendedExecutionAt := payload.IntendedExecutionAt
	if intendedExecutionAt == nil {
		intendedExecutionAt = payload.DeadlineAt
	}

	var providerHint *string
	if payload.ProviderHint != "" {
		providerHint = &payload.ProviderHint
	} else if payload.SourceSystem != "" {
		providerHint = &payload.SourceSystem
	}
	// var signatureCarrier *string
	// if payload.CanonicalSnapshotRef != "" {
	// 	signatureCarrier = &payload.CanonicalSnapshotRef
	// }
	var clientPayoutRef *string
	if payload.ClientPayoutRef != "" {
		clientPayoutRef = &payload.ClientPayoutRef
	}
	var clientBatchRef *string
	if payload.ClientBatchRef != "" {
		clientBatchRef = &payload.ClientBatchRef
	}
	var bizIdemKey *string
	if payload.BusinessIdempotencyKey != "" {
		bizIdemKey = &payload.BusinessIdempotencyKey
	}
	var bizIdemKeyV1 *string
	if payload.BusinessIdempotencyKeyV1 != "" {
		bizIdemKeyV1 = &payload.BusinessIdempotencyKeyV1
	}

	return models.CanonicalIntent{
		IntentID:               intentID,
		TenantID:               tenantID,
		TraceID:                parsedTraceID,
		ContractID:             contractID,
		ClientPayoutRef:        clientPayoutRef,
		ClientBatchRef:         clientBatchRef,
		BusinessIdempotencyKey: bizIdemKey,
		Amount:                 amount,
		CurrencyCode:           payload.Currency,
		IntendedExecutionAt:    intendedExecutionAt,
		PayoutType:             payoutType,
		ProviderHint:           providerHint,
		ProofReadinessScore:    payload.ProofReadinessScore,
		MatchabilityScore:      payload.MatchabilityScore,
		CanonicalHash:          payload.CanonicalHash,
		GovernanceState:        payload.GovernanceState,
		// ZordSignatureCarrier:   signatureCarrier,
		SourceRowNum: payload.SourceRowNum,
		CreatedAt:    createdAt,
		// Legacy v1 business key (compare-only, Slice 8 v1->v2 transition).
		BusinessIdempotencyKeyV1: bizIdemKeyV1,
	}, nil
}
