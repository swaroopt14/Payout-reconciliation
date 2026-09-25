package persistence

import (
	"context"
	"database/sql"
	"log"
	"time"

	"zord-intent-engine/internal/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type BatchPullRepository interface {
	LeaseBatch(ctx context.Context, limit int, leaseTTLSeconds int, leasedBy string) (string, *time.Time, []models.CanonicalBatch, error)
	AckBatch(ctx context.Context, leaseID string, batchIDs []string) (int64, error)
	NackBatch(ctx context.Context, leaseID string, batchIDs []string) (int64, error)
}

type BatchPullRepo struct {
	db *sql.DB
}

const (
	maxBatchAttempts = 5
	maxBatchAgeHours = 24
)

func NewBatchPullRepo(db *sql.DB) *BatchPullRepo {
	return &BatchPullRepo{db: db}
}

func (r *BatchPullRepo) LeaseBatch(ctx context.Context, limit int, leaseTTLSeconds int, leasedBy string) (string, *time.Time, []models.CanonicalBatch, error) {
	const maxLeaseLimit = 1000

	if limit <= 0 {
		limit = 500
	}
	if limit > maxLeaseLimit {
		limit = maxLeaseLimit
	}

	leaseUUID := uuid.New()
	leaseID := leaseUUID.String()

	// Prefer the explicit lifecycle signal: intent_ingest_runs.status = 'COMPLETED'
	// means ingestion for this batch is genuinely done. Fall back to the old
	// quiet-5-minutes heuristic only when no ingest run is tracked for this
	// batch at all (e.g. legacy data, or a batch that predates this migration) —
	// see refactor blueprint Phase 6 ("replace quiet-5-minute logic with
	// lifecycle gate where possible").
	query := `
WITH picked AS (
	SELECT cb.tenant_id, cb.batch_id
	FROM canonical_batches cb
	LEFT JOIN intent_ingest_runs ir
		ON ir.tenant_id = cb.tenant_id AND ir.batch_id = cb.batch_id
	WHERE cb.dispatched_at IS NULL
	  AND cb.retry_count < $5
	  AND (cb.lease_until IS NULL OR cb.lease_until < NOW())
	  AND (cb.next_attempt_at IS NULL OR cb.next_attempt_at <= NOW())
	  AND (
	        ir.status = 'COMPLETED'
	        OR (ir.run_id IS NULL AND cb.updated_at < NOW() - INTERVAL '5 minutes')
	      )
	ORDER BY cb.created_at ASC
	LIMIT $1
	FOR UPDATE OF cb SKIP LOCKED
),
leased AS (
	UPDATE canonical_batches cb
	SET lease_id = $2::uuid,
	    leased_by = $3,
	    lease_until = NOW() + ($4::int * INTERVAL '1 second')
	FROM picked p
	WHERE cb.tenant_id = p.tenant_id AND cb.batch_id = p.batch_id
	RETURNING
		cb.tenant_id::text as tenant_id,
		cb.batch_id,
		COALESCE(cb.source_system, '') as source_system,
		cb.received_count,
		cb.canonicalized_count,
		cb.dlq_count,
		cb.review_count,
		cb.low_matchability_count,
		cb.low_proof_readiness_count,
		cb.duplicate_risk_count,
		cb.canonicalization_success_rate,
		cb.avg_schema_completeness_score,
		cb.avg_mapping_confidence_score,
		cb.avg_matchability_score,
		cb.avg_proof_readiness_score,
		cb.avg_intent_quality_score,
		cb.duplicate_risk_amount_minor,
		COALESCE(cb.total_amount, 0)::text as total_amount_text,
		cb.batch_quality_score,
		cb.score_breakdown_json,
		cb.created_at,
		cb.updated_at,
		COALESCE(cb.lease_id::text, '') as lease_id,
		COALESCE(cb.leased_by, '') as leased_by,
		cb.lease_until,
		cb.retry_count,
		cb.next_attempt_at,
		cb.dispatched_at
)
SELECT
	tenant_id,
	batch_id,
	source_system,
	received_count,
	canonicalized_count,
	dlq_count,
	review_count,
	low_matchability_count,
	low_proof_readiness_count,
	duplicate_risk_count,
	canonicalization_success_rate,
	avg_schema_completeness_score,
	avg_mapping_confidence_score,
	avg_matchability_score,
	avg_proof_readiness_score,
	avg_intent_quality_score,
	duplicate_risk_amount_minor,
	total_amount_text,
	batch_quality_score,
	score_breakdown_json,
	created_at,
	updated_at,
	lease_id,
	leased_by,
	lease_until,
	retry_count,
	next_attempt_at,
	dispatched_at
FROM leased
ORDER BY created_at ASC;
`

	rows, err := r.db.QueryContext(ctx, query, limit, leaseID, leasedBy, leaseTTLSeconds, maxBatchAttempts)
	if err != nil {
		return "", nil, nil, err
	}
	defer rows.Close()

	entries := make([]models.CanonicalBatch, 0, limit)
	var leaseUntil *time.Time

	for rows.Next() {
		var e models.CanonicalBatch
		var nextRetry sql.NullTime
		var lu sql.NullTime
		var dispAt sql.NullTime
		var totalAmountText string

		if err := rows.Scan(
			&e.TenantID,
			&e.BatchID,
			&e.SourceSystem,
			&e.ReceivedCount,
			&e.CanonicalizedCount,
			&e.DLQCount,
			&e.ReviewCount,
			&e.LowMatchabilityCount,
			&e.LowProofReadinessCount,
			&e.DuplicateRiskCount,
			&e.CanonicalizationSuccessRate,
			&e.AvgSchemaCompletenessScore,
			&e.AvgMappingConfidenceScore,
			&e.AvgMatchabilityScore,
			&e.AvgProofReadinessScore,
			&e.AvgIntentQualityScore,
			&e.DuplicateRiskAmountMinor,
			&totalAmountText,
			&e.BatchQualityScore,
			&e.ScoreBreakdownJSON,
			&e.CreatedAt,
			&e.UpdatedAt,
			&e.LeaseID,
			&e.LeasedBy,
			&lu,
			&e.RetryCount,
			&nextRetry,
			&dispAt,
		); err != nil {
			return "", nil, nil, err
		}

		// canonical_batches.total_amount is NUMERIC rupees; publish it as
		// exact int64 paise (D10). If it cannot be converted exactly the
		// field is left absent — never rounded or guessed.
		if minor, convErr := models.BatchTotalAmountMinor(totalAmountText); convErr == nil {
			e.TotalAmountMinor = &minor
		} else {
			log.Printf("⚠️ canonical_batches total_amount not exact paise for batch=%s: %v", e.BatchID, convErr)
		}

		if nextRetry.Valid {
			t := nextRetry.Time
			e.NextAttemptAt = &t
		}
		if dispAt.Valid {
			t := dispAt.Time
			e.DispatchedAt = &t
		}
		if lu.Valid {
			t := lu.Time
			e.LeaseUntil = &t
			if leaseUntil == nil {
				leaseUntil = &t
			}
		}

		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return "", nil, nil, err
	}

	if len(entries) == 0 {
		return "", nil, []models.CanonicalBatch{}, nil
	}

	return leaseID, leaseUntil, entries, nil
}

func (r *BatchPullRepo) AckBatch(ctx context.Context, leaseID string, batchIDs []string) (int64, error) {
	query := `
UPDATE canonical_batches
SET dispatched_at = NOW(),
    updated_at = NOW(),
    lease_id = NULL,
    leased_by = NULL,
    lease_until = NULL
WHERE lease_id = $1::uuid
  AND batch_id = ANY($2::text[]);
`
	res, err := r.db.ExecContext(ctx, query, leaseID, pq.Array(batchIDs))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *BatchPullRepo) NackBatch(ctx context.Context, leaseID string, batchIDs []string) (int64, error) {
	// NOTE: bumping updated_at here also resets the LeaseBatch fallback
	// quiet-wait heuristic (cb.updated_at < NOW() - 5 minutes) for any batch
	// that still has no intent_ingest_runs row. That's the correct behavior —
	// a just-nacked batch shouldn't be immediately re-picked — but it does mean
	// such a batch waits another 5 minutes after every nack before retry.
	query := `
UPDATE canonical_batches
SET retry_count = retry_count + 1,
    next_attempt_at = CASE
        WHEN retry_count + 1 >= $3 OR created_at < NOW() - ($4::int * INTERVAL '1 hour') THEN NULL
        ELSE NOW() + (
			LEAST(3600, GREATEST(1, POWER(2, retry_count))) * (0.8 + random() * 0.4)
		) * INTERVAL '1 second'
    END,
    updated_at = NOW(),
    lease_id = NULL,
    leased_by = NULL,
    lease_until = NULL
WHERE lease_id = $1::uuid
  AND batch_id = ANY($2::text[])
  AND dispatched_at IS NULL;
`
	res, err := r.db.ExecContext(ctx, query, leaseID, pq.Array(batchIDs), maxBatchAttempts, maxBatchAgeHours)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
