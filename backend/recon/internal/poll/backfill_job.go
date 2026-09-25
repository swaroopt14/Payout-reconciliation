package poll

import (
	"fmt"
	"time"
)

const (
	ResourcePayments    = "payments"
	ResourceSettlements = "settlements"
	// ResourcePayouts is the timer pull of provider payouts (D26). It feeds the
	// same payout-truth intake as payout webhooks.
	ResourcePayouts = "payouts"

	TriggerAirflow   = "airflow"
	TriggerManual    = "manual"
	TriggerRepair    = "repair"
	TriggerScheduled = "scheduled"

	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobPartial   = "partial"
	JobFailed    = "failed"
	JobCancelled = "cancelled"

	CursorActive   = "active"
	CursorComplete = "complete"
	CursorPaused   = "paused"
	CursorFailed   = "failed"

	DefaultLeaseTTL     = 2 * time.Minute
	DefaultPaymentCount = 100
	DefaultReconCount   = 1000
)

// CreateBackfillRequest is the validated input for a new or reused job.
type CreateBackfillRequest struct {
	TenantID       string
	ConnectorID    string
	Provider       string
	Mode           string
	ResourceType   string
	WindowFrom     time.Time
	WindowTo       time.Time
	OverlapMinutes int
	TriggerType    string
	RequestedBy    *string
	TraceID        string
}

func (r CreateBackfillRequest) Validate(now time.Time) error {
	if r.TenantID == "" || r.ConnectorID == "" {
		return fmt.Errorf("tenant_id and connector_id are required")
	}
	if r.ResourceType != ResourcePayments && r.ResourceType != ResourceSettlements && r.ResourceType != ResourcePayouts {
		return fmt.Errorf("resource_type must be payments, settlements or payouts")
	}
	if r.Mode != "test" && r.Mode != "live" {
		return fmt.Errorf("mode must be test or live")
	}
	trigger := r.TriggerType
	if trigger == "" {
		trigger = TriggerManual
	}
	switch trigger {
	case TriggerAirflow, TriggerManual, TriggerRepair, TriggerScheduled:
	default:
		return fmt.Errorf("invalid trigger_type")
	}
	if err := ValidateWindow(r.WindowFrom, r.WindowTo, now); err != nil {
		return err
	}
	return nil
}

// BackfillJob is the durable job record.
type BackfillJob struct {
	ID                  string
	TenantID            string
	ConnectorID         string
	Provider            string
	ProviderMode        string
	ResourceType        string
	WindowFrom          time.Time
	WindowTo            time.Time
	TriggerType         string
	Status              string
	RequestedBy         *string
	StartedAt           *time.Time
	CompletedAt         *time.Time
	FetchedCount        int64
	InsertedCount       int64
	UpdatedCount        int64
	DuplicateCount      int64
	MissingWebhookCount int64
	ErrorCount          int64
	LastErrorCode       string
	LastErrorMessage    string
	TraceID             string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// BackfillCursor is skip/count state for a fixed window.
type BackfillCursor struct {
	ID               string
	TenantID         string
	ConnectorID      string
	ResourceType     string
	WindowFrom       time.Time
	WindowTo         time.Time
	PageSkip         int
	PageCount        int
	PagesCompleted   int
	LastProviderID   string
	LastResponseHash string
	Status           string
	LeaseOwner       string
	LeaseExpiresAt   *time.Time
	UpdatedAt        time.Time
}

// BackfillSummary is the job output contract.
type BackfillSummary struct {
	JobID                 string        `json:"job_id"`
	Status                string        `json:"status"`
	FetchedCount          int64         `json:"fetched_count"`
	InsertedCount         int64         `json:"inserted_count"`
	UpdatedCount          int64         `json:"updated_count"`
	SkippedDuplicateCount int64         `json:"skipped_duplicate_count"`
	MissingWebhookCount   int64         `json:"missing_webhook_count"`
	APIErrorCount         int64         `json:"api_error_count"`
	Cursor                CursorSummary `json:"cursor"`
	FreshnessTimestamp    *time.Time    `json:"freshness_timestamp,omitempty"`
	// LastErrorCode makes a failed run's reason visible (e.g. NO_CREDENTIALS).
	LastErrorCode string `json:"last_error_code,omitempty"`
}

type CursorSummary struct {
	PageSkip       int    `json:"page_skip"`
	PagesCompleted int    `json:"pages_completed"`
	Status         string `json:"status"`
}

func SummaryFromJob(job BackfillJob, cursor BackfillCursor) BackfillSummary {
	return BackfillSummary{
		JobID:                 job.ID,
		Status:                job.Status,
		FetchedCount:          job.FetchedCount,
		InsertedCount:         job.InsertedCount,
		UpdatedCount:          job.UpdatedCount,
		SkippedDuplicateCount: job.DuplicateCount,
		MissingWebhookCount:   job.MissingWebhookCount,
		APIErrorCount:         job.ErrorCount,
		LastErrorCode:         job.LastErrorCode,
		Cursor: CursorSummary{
			PageSkip:       cursor.PageSkip,
			PagesCompleted: cursor.PagesCompleted,
			Status:         cursor.Status,
		},
	}
}

// PageCounters are per-page upsert results.
type PageCounters struct {
	Fetched        int64
	Inserted       int64
	Updated        int64
	Duplicate      int64
	MissingWebhook int64
}

type UpsertResult string

const (
	UpsertInserted  UpsertResult = "inserted"
	UpsertUpdated   UpsertResult = "updated"
	UpsertDuplicate UpsertResult = "duplicate"
)
