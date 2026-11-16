package outbox

import (
	"github.com/STECH-Super-App/go-common/pkg/errors"
)

// Reasons of the typed errors this package returns (Critical Rule 7: every
// error from a shared library is an *errors.AppError). All are 500s: each is a
// storage or programmer fault, never a client mistake; the driver error is kept
// as the Cause, so errors.As to *pgconn.PgError still works for a caller that
// maps a constraint.
const (
	ReasonInsertFailed   = "COMMON_OUTBOX_INSERT_FAILED"
	ReasonFetchFailed    = "COMMON_OUTBOX_FETCH_FAILED"
	ReasonStatsFailed    = "COMMON_OUTBOX_STATS_FAILED"
	ReasonMarkFailed     = "COMMON_OUTBOX_MARK_FAILED"
	ReasonCleanupFailed  = "COMMON_OUTBOX_CLEANUP_FAILED"
	ReasonTxFailed       = "COMMON_OUTBOX_TX_FAILED"
	ReasonTxInvalid      = "COMMON_OUTBOX_TX_INVALID"
	ReasonPublishInvalid = "COMMON_OUTBOX_PUBLISH_INVALID"
	ReasonMarshalFailed  = "COMMON_OUTBOX_MARSHAL_FAILED"
	ReasonDedupFailed    = "COMMON_OUTBOX_DEDUP_FAILED"
	ReasonMigrateFailed  = "COMMON_OUTBOX_MIGRATE_FAILED"

	// ReasonEventTooLarge is the one 422 of this package: PublishProto refused an
	// event that would not fit one Kafka record (see DefaultMaxMessageBytes).
	ReasonEventTooLarge = "COMMON_OUTBOX_EVENT_TOO_LARGE"
)

// internalError builds the 500 AppError for a failure inside this package.
// message is the English fallback and keeps the wording the plain errors had.
func internalError(reason, message string, cause error) *errors.AppError {
	return errors.New(500).Reason(reason).Message(message).Cause(cause).Build()
}
