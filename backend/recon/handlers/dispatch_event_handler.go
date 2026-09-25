package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"zord-outcome-engine/db"
	"zord-outcome-engine/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Relay dispatch event types (zord-relay services/dispatch_loop.go,
// services/recovery_sweeper.go, model/dispatch_events.go).
const (
	dispatchEventCreated                = "DispatchCreated"
	dispatchEventAttemptSent            = "AttemptSent"
	dispatchEventProviderAcked          = "ProviderAcked"
	dispatchEventFailed                 = "DispatchFailed"
	dispatchEventRetryScheduled         = "DispatchRetryScheduled"
	dispatchEventHeld                   = "DispatchHeld"
	dispatchEventGovernanceEvaluated    = "DispatchGovernanceEvaluated"
	dispatchEventAwaitingProviderSignal = "DispatchAwaitingProviderSignal"

	// relay model.GovernanceAllow
	governanceAllowDispatch = "ALLOW_DISPATCH"
	// relay services.ReasonConnectorUnresolved: the dispatch was held before
	// a DispatchCreated could be published, so no dispatch_index row exists.
	reasonConnectorUnresolved = "CONNECTOR_UNRESOLVED"
)

// ErrInvalidDispatchConnectorID is returned when a DispatchCreated event does
// not carry a non-nil connectors.id UUID (D44, L7). The row is not written:
// a slug or zero UUID must never be stored as connector_id.
var ErrInvalidDispatchConnectorID = errors.New("dispatch event connector_id is not a non-nil UUID")

// ErrDispatchIndexRowMissing is returned when a follow-up dispatch event
// refers to a dispatch_id with no dispatch_index row for that tenant.
var ErrDispatchIndexRowMissing = errors.New("dispatch_id not found in dispatch_index")

func HandleDispatchEvent(msg []byte) error {
	var event models.DispatchEvent
	ctx := context.Background()
	if err := json.Unmarshal(msg, &event); err != nil {
		return err
	}

	decode := func(v any) error {
		if err := json.Unmarshal(event.Payload, v); err != nil {
			return fmt.Errorf("%s payload: %w", event.EventType, err)
		}
		return nil
	}

	switch event.EventType {
	case dispatchEventCreated:
		var payload models.DispatchCreatedPayload
		if err := decode(&payload); err != nil {
			return err
		}
		log.Println("Received Dispatch Event")
		return handleDispatchCreated(ctx, payload, event)

	case dispatchEventAttemptSent:
		var payload models.AttemptSentPayload
		if err := decode(&payload); err != nil {
			return err
		}
		log.Println("Received Attempt Sent Event")
		return handleAttemptSent(ctx, payload)

	case dispatchEventProviderAcked:
		var payload models.ProviderAckedPayload
		if err := decode(&payload); err != nil {
			return err
		}
		log.Println("Received Provider Ack Event")
		return handleProviderAcked(ctx, payload)

	case dispatchEventFailed:
		var payload models.DispatchFailedPayload
		if err := decode(&payload); err != nil {
			return err
		}
		return updateDispatchStatus(ctx, event, payload.DispatchID, "FAILED", payload.Reason, false)

	case dispatchEventRetryScheduled:
		var payload models.DispatchRetryScheduledPayload
		if err := decode(&payload); err != nil {
			return err
		}
		reason := strings.Trim(payload.RetryClass+": "+payload.FailureReason, ": ")
		return updateDispatchStatus(ctx, event, payload.DispatchID, "RETRY_SCHEDULED", reason, false)

	case dispatchEventHeld:
		var payload models.DispatchHeldPayload
		if err := decode(&payload); err != nil {
			return err
		}
		reason := payload.Reason
		if reason == "" {
			reason = strings.Join(payload.ReasonCodes, ",")
		}
		return updateDispatchStatus(ctx, event, payload.DispatchID, "HELD", reason, containsCode(payload.ReasonCodes, reasonConnectorUnresolved))

	case dispatchEventGovernanceEvaluated:
		var payload models.DispatchGovernanceEvaluatedPayload
		if err := decode(&payload); err != nil {
			return err
		}
		if payload.Decision == governanceAllowDispatch {
			return nil // ALLOW is not a state change worth indexing
		}
		return updateDispatchStatus(ctx, event, payload.DispatchID, governanceStatus(payload.Decision),
			strings.Join(payload.ReasonCodes, ","), containsCode(payload.ReasonCodes, reasonConnectorUnresolved))

	case dispatchEventAwaitingProviderSignal:
		var payload models.DispatchAwaitingProviderSignalPayload
		if err := decode(&payload); err != nil {
			return err
		}
		return updateDispatchStatus(ctx, event, payload.DispatchID, "AWAITING_PROVIDER_SIGNAL", payload.Reason, false)
	}
	return nil
}

// governanceStatus maps a relay governance decision to relay's dispatch
// status vocabulary (model.DispatchStatus*). Dispatch state only.
func governanceStatus(decision string) string {
	switch decision {
	case "HOLD_DISPATCH":
		return "HELD"
	case "TERMINAL_FAIL":
		return "FAILED_TERMINAL"
	case "REQUIRE_MANUAL_REVIEW":
		return "REQUIRES_MANUAL_REVIEW"
	case "RETRY_LATER":
		return "RETRY_LATER"
	default:
		return decision
	}
}

func containsCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// updateDispatchStatus records dispatch state on the existing dispatch_index
// row, scoped by dispatch_id and tenant_id. It never inserts: only
// DispatchCreated creates rows. missingRowOK is set for holds that happen
// before relay could publish DispatchCreated (CONNECTOR_UNRESOLVED).
func updateDispatchStatus(ctx context.Context, event models.DispatchEvent, dispatchID, status, reason string, missingRowOK bool) error {
	if dispatchID == "" {
		return fmt.Errorf("dispatch_id missing in %s event", event.EventType)
	}
	if event.TenantID == "" {
		return fmt.Errorf("tenant_id missing in %s event", event.EventType)
	}
	res, err := db.DB.ExecContext(ctx, `UPDATE dispatch_index
		SET status = $1, status_reason = $2, updated_at = now()
		WHERE dispatch_id = $3 AND tenant_id = $4`,
		status, reason, dispatchID, event.TenantID,
	)
	if err != nil {
		return fmt.Errorf("dispatch_index status update (%s): %w", event.EventType, err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		if missingRowOK {
			log.Printf("dispatch_index: %s for dispatch_id=%s has no row (held before DispatchCreated: %s)", event.EventType, dispatchID, reason)
			return nil
		}
		return fmt.Errorf("%w: %s dispatch_id=%s", ErrDispatchIndexRowMissing, event.EventType, dispatchID)
	}
	log.Printf("dispatch_index: dispatch_id=%s status=%s", dispatchID, status)
	return nil
}

func handleDispatchCreated(ctx context.Context, payload models.DispatchCreatedPayload, data models.DispatchEvent) error {
	log.Println("DispatchCreated Event Processing started")
	if payload.DispatchID == "" {
		return errors.New("dispatch_id missing in DispatchCreated Event")
	}
	if data.ContractID == "" || data.IntentID == "" || data.TenantID == "" {
		return errors.New("invalid dispatch event metadata")
	}
	connectorUUID, err := uuid.Parse(strings.TrimSpace(payload.ConnectorID))
	if err != nil || connectorUUID == uuid.Nil {
		return fmt.Errorf("%w: dispatch_id=%s connector_id=%q", ErrInvalidDispatchConnectorID, payload.DispatchID, payload.ConnectorID)
	}

	carriersJSON, err := json.Marshal(payload.CorrelationCarriers)
	if err != nil {
		return err
	}

	var providerRefHashes []string
	if payload.CorrelationCarriers.ReferenceID != "" {
		h := sha256.Sum256([]byte(payload.CorrelationCarriers.ReferenceID))
		providerRefHashes = append(providerRefHashes, hex.EncodeToString(h[:]))
	}
	if payload.CorrelationCarriers.Narration != "" {
		h := sha256.Sum256([]byte(payload.CorrelationCarriers.Narration))
		providerRefHashes = append(providerRefHashes, hex.EncodeToString(h[:]))
	}

	var connectorRef sql.NullString
	if ref := strings.TrimSpace(payload.ConnectorRef); ref != "" {
		connectorRef = sql.NullString{String: ref, Valid: true}
	}

	// Idempotent on replay: one row per dispatch_id (L7).
	res, err := db.DB.ExecContext(ctx, `INSERT INTO dispatch_index (
		dispatch_id,
		contract_id,
		intent_id,
		tenant_id,
		trace_id,
		connector_id,
		corridor_id,
		correlation_carriers,
		provider_ref_hashes,
		connector_ref,
		status,
		updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'CREATED',now())
	ON CONFLICT (dispatch_id) DO NOTHING`,
		payload.DispatchID,
		data.ContractID,
		data.IntentID,
		data.TenantID,
		data.TraceID,
		connectorUUID.String(),
		payload.CorridorID,
		carriersJSON,
		pq.Array(providerRefHashes),
		connectorRef,
	)
	if err != nil {
		log.Printf("Dispatch index insert failed dispatch_id=%s: %v", payload.DispatchID, err)
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Printf("Dispatch index already present (replay) dispatch_id=%s", payload.DispatchID)
		return nil
	}
	log.Printf("Dispatch index inserted dispatch_id=%s", payload.DispatchID)
	return nil
}

func handleAttemptSent(ctx context.Context, payload models.AttemptSentPayload) error {
	log.Println("AttemptSent Event Processing started")
	if payload.DispatchID == "" {
		return errors.New("dispatch_id missing in AttemptSent Event")
	}
	carriersJSON, err := json.Marshal(payload.CorrelationCarriers)
	if err != nil {
		return err
	}
	res, err := db.DB.ExecContext(ctx, `UPDATE dispatch_index SET attempt_count=$1,correlation_carriers=$2 WHERE dispatch_id=$3`,
		payload.AttemptCount, carriersJSON, payload.DispatchID,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("dispatch_id not found in dispatch_index")
	}
	return nil
}

func handleProviderAcked(ctx context.Context, payload models.ProviderAckedPayload) error {
	log.Println("ProviderAcked Event Processing started")
	if payload.DispatchID == "" {
		return errors.New("dispatch_id missing in ProviderAcked Event")
	}
	res, err := db.DB.ExecContext(ctx, `UPDATE dispatch_index SET provider_attempt_id=$1 WHERE dispatch_id=$2`,
		payload.ProviderAttemptID, payload.DispatchID,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("dispatch_id not found in dispatch_index")
	}
	return nil
}
