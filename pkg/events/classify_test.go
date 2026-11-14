package events_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	commonerrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/events"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

func appErr(status int) error {
	return commonerrors.New(status).Reason("X_REASON").Message("x").Build()
}

// The one table (spec §3.1, SL-11). A service cites it in its own tests; the
// PHP consumers copy it by hand.
func TestClassify(t *testing.T) {
	_, inputFailure := input.Line("", 10, input.Required())
	if inputFailure == nil {
		t.Fatal("test setup: input.Line must refuse an empty required value")
	}

	cases := []struct {
		name    string
		err     error
		want    events.Class
		deadNow bool
	}{
		// dead-lettered at once
		{"poison pill", events.ErrPoisonPill, events.ClassPoisonPill, true},
		{"poison pill, wrapped", fmt.Errorf("unknown tenant: %w", events.ErrPoisonPill), events.ClassPoisonPill, true},
		{"poison pill joined with a transient", errors.Join(events.ErrPoisonPill, errors.New("db down")), events.ClassPoisonPill, true},
		{"poison pill under an AppError cause", commonerrors.New(500).Reason("X").Message("x").Cause(events.ErrPoisonPill).Build(), events.ClassPoisonPill, true},
		{"validation: AppError 400", appErr(http.StatusBadRequest), events.ClassValidation, true},
		{"validation: AppError 422", appErr(http.StatusUnprocessableEntity), events.ClassValidation, true},
		{"validation: AppError 422, wrapped", fmt.Errorf("apply: %w", appErr(http.StatusUnprocessableEntity)), events.ClassValidation, true},
		{"validation: input.Failure", inputFailure, events.ClassValidation, true},
		{"validation: input.Failure, wrapped", fmt.Errorf("name: %w", inputFailure), events.ClassValidation, true},

		// a recovered handler panic is not a handler's return value: the dispatcher
		// builds it (see TestClassify_PanicIsDeadLetteredAtOnce), so the table
		// here has no row for it.

		// retried in place, whatever they are
		{"404 (the parent row is not there yet)", appErr(http.StatusNotFound), events.ClassRetryable, false},
		{"409 (a race)", appErr(http.StatusConflict), events.ClassRetryable, false},
		{"403", appErr(http.StatusForbidden), events.ClassRetryable, false},
		{"500", appErr(http.StatusInternalServerError), events.ClassRetryable, false},
		{"503 (a dependency is down)", appErr(http.StatusServiceUnavailable), events.ClassRetryable, false},
		{"504", appErr(http.StatusGatewayTimeout), events.ClassRetryable, false},
		{"timeout", errors.New("context deadline exceeded"), events.ClassRetryable, false},
		{"unknown error", errors.New("boom"), events.ClassRetryable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := events.Classify(tc.err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.deadNow, got.DeadLetterNow())
		})
	}
}

func inputRequired() (string, *input.Failure) { return input.Line("", 10, input.Required()) }
