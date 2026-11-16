package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/logger"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
	"github.com/STECH-Super-App/go-common/pkg/middleware"
)

const registrationRejectedMetric = "stech_registration_auth_rejected_total"

// rejectedByCause reads every cause series of the counter off the shared
// registry. A series created at zero is listed too, which is what the
// pre-registration test relies on.
func rejectedByCause(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := metrics.Registry.Gather()
	require.NoError(t, err)
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != registrationRejectedMetric {
			continue
		}
		for _, m := range f.GetMetric() {
			require.Len(t, m.GetLabel(), 1, "the counter has exactly one label")
			require.Equal(t, "cause", m.GetLabel()[0].GetName())
			out[m.GetLabel()[0].GetValue()] = m.GetCounter().GetValue()
		}
	}
	return out
}

type refusal struct {
	rec    *httptest.ResponseRecorder
	logs   []observer.LoggedEntry
	called bool
}

// runRegistrationAuth sends one request through the middleware with a
// request-scoped logger that carries a request_id, as RequestLogger would.
func runRegistrationAuth(t *testing.T, headers map[string]string) refusal {
	t.Helper()
	core, obs := observer.New(zapcore.DebugLevel)
	reqLogger := zap.New(core).With(zap.String("request_id", "req-123"))

	req := httptest.NewRequest(http.MethodPost, "/register", nil)
	req = req.WithContext(logger.IntoContext(req.Context(), reqLogger))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()

	var r refusal
	handler := middleware.RegistrationAuthMiddleware(func(c echo.Context) error {
		r.called = true
		return c.NoContent(http.StatusNoContent)
	})
	require.NoError(t, handler(echo.New().NewContext(req, rec)))
	r.rec = rec
	// response.JSONError logs its own Warn "request rejected" line (reason, no
	// cause) for every 4xx; this test is about the middleware's cause line.
	r.logs = obs.FilterMessage("registration token refused").All()
	return r
}

const secretPhone = "+79991234567"

func TestRegistrationAuthMiddleware_RefusalCauseIsCountedAndLogged(t *testing.T) {
	cases := map[string]struct {
		headers map[string]string
		cause   string
	}{
		"no headers at all":              {nil, "no_phone"},
		"user token, no phone":           {map[string]string{auth.HeaderUserID: "u1", auth.HeaderScope: "full"}, "no_phone"},
		"scope but no phone":             {map[string]string{auth.HeaderScope: "registration"}, "no_phone"},
		"phone, empty scope":             {map[string]string{auth.HeaderPhone: secretPhone}, "wrong_scope"},
		"phone, another scope":           {map[string]string{auth.HeaderPhone: secretPhone, auth.HeaderScope: "full"}, "wrong_scope"},
		"gateway: expired":               {map[string]string{auth.HeaderAuthOutcome: auth.OutcomeExpired}, "auth_outcome_expired"},
		"gateway: untrusted":             {map[string]string{auth.HeaderAuthOutcome: auth.OutcomeUntrusted}, "auth_outcome_untrusted"},
		"gateway: revoked, phone leaked": {map[string]string{auth.HeaderAuthOutcome: auth.OutcomeRevoked, auth.HeaderPhone: secretPhone}, "auth_outcome_revoked"},
		"outcome the gateway never sets": {map[string]string{auth.HeaderAuthOutcome: "totally-new-" + secretPhone}, "auth_outcome_unknown"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before := rejectedByCause(t)

			r := runRegistrationAuth(t, tc.headers)

			assert.False(t, r.called, "a refused request never reaches the handler")
			assert.Equal(t, http.StatusUnauthorized, r.rec.Code)
			assert.Contains(t, r.rec.Body.String(), "COMMON_REGISTRATION_TOKEN_REQUIRED")

			after := rejectedByCause(t)
			assert.Equal(t, before[tc.cause]+1, after[tc.cause], "the cause series moves by one")
			for cause, v := range after {
				if cause != tc.cause {
					assert.Equal(t, before[cause], v, "no other series moves (%s)", cause)
				}
			}

			require.Len(t, r.logs, 1, "exactly one line per refusal")
			entry := r.logs[0]
			assert.Equal(t, zapcore.InfoLevel, entry.Level)
			assert.Equal(t, tc.cause, entry.ContextMap()["cause"])
			assert.Equal(t, "req-123", entry.ContextMap()["request_id"])
			assert.Len(t, entry.ContextMap(), 2, "only cause and request_id: %v", entry.ContextMap())

			assert.NotContains(t, entry.Message, secretPhone)
			for cause := range after {
				assert.NotContains(t, cause, secretPhone, "a header value never becomes a label")
				assert.NotContains(t, cause, "totally-new")
			}
		})
	}
}

// The client sees the same answer whatever the cause: the cause is for the
// operator. (AuthMiddleware, by contrast, answers per outcome; that difference
// is deliberate to leave alone here, see CHANGELOG.)
func TestRegistrationAuthMiddleware_ClientAnswerIsTheSameForEveryCause(t *testing.T) {
	a := runRegistrationAuth(t, nil)
	b := runRegistrationAuth(t, map[string]string{auth.HeaderPhone: secretPhone, auth.HeaderScope: "full"})
	c := runRegistrationAuth(t, map[string]string{auth.HeaderAuthOutcome: auth.OutcomeExpired})

	for _, r := range []refusal{b, c} {
		assert.Equal(t, a.rec.Code, r.rec.Code)
		assert.Equal(t, a.rec.Body.String(), r.rec.Body.String())
		assert.Equal(t, a.rec.Header(), r.rec.Header())
	}
	assert.Empty(t, a.rec.Header().Get(auth.HeaderAuthAction))
}

func TestRegistrationAuthMiddleware_AdmittedRequestLeavesNoTrace(t *testing.T) {
	before := rejectedByCause(t)

	r := runRegistrationAuth(t, map[string]string{auth.HeaderPhone: secretPhone, auth.HeaderScope: "registration"})

	assert.True(t, r.called)
	assert.Equal(t, http.StatusNoContent, r.rec.Code)
	assert.Empty(t, r.logs)
	assert.Equal(t, before, rejectedByCause(t))
}

// Pre-registered at zero: the first refusal of a kind must already show as an
// increase() against a baseline.
func TestRegistrationAuthMiddleware_EveryCauseSeriesExistsAtStart(t *testing.T) {
	got := rejectedByCause(t)

	want := []string{"no_phone", "wrong_scope", "auth_outcome_unknown"}
	for _, o := range auth.KnownOutcomes() {
		want = append(want, "auth_outcome_"+o)
	}
	for _, cause := range want {
		assert.Contains(t, got, cause)
	}
	assert.Len(t, got, len(want), "no series outside the closed set: %v", got)
}
