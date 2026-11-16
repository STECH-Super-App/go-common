package middleware

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/STECH-Super-App/go-common/pkg/auth"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

// Causes of a RegistrationAuthMiddleware refusal — the whole value set of the
// `cause` label. Closed on purpose: the header values behind it are clamped, so
// a caller cannot mint a series.
const (
	// RegistrationCauseNoPhone: X-Phone is empty — the request carries no
	// registration identity at all (no token, or an ordinary user token).
	RegistrationCauseNoPhone = "no_phone"
	// RegistrationCauseWrongScope: a phone is present but X-Scope is not
	// "registration" (including empty).
	RegistrationCauseWrongScope = "wrong_scope"

	registrationCauseOutcomePrefix = "auth_outcome_"
	// registrationCauseOutcomeUnknown: X-Auth-Outcome held a value the gateway
	// never sets. The raw value is never a label.
	registrationCauseOutcomeUnknown = registrationCauseOutcomePrefix + "unknown"
)

// registrationAuthRejected counts the requests RegistrationAuthMiddleware
// answered with 401, by cause. No phone, scope value, user id or token ever
// reaches a label.
var registrationAuthRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "stech_registration_auth_rejected_total",
	Help: "Requests refused by RegistrationAuthMiddleware (401 COMMON_REGISTRATION_TOKEN_REQUIRED), by cause: no_phone, wrong_scope, auth_outcome_<gateway outcome> or auth_outcome_unknown.",
}, []string{"cause"})

func init() {
	metrics.Registry.MustRegister(registrationAuthRejected)
	// Every series at 0 from the first scrape, so increase() has a baseline.
	registrationAuthRejected.WithLabelValues(RegistrationCauseNoPhone)
	registrationAuthRejected.WithLabelValues(RegistrationCauseWrongScope)
	for _, o := range auth.KnownOutcomes() {
		registrationAuthRejected.WithLabelValues(registrationCauseOutcomePrefix + o)
	}
	registrationAuthRejected.WithLabelValues(registrationCauseOutcomeUnknown)
}

// registrationRefusalCause names why a request failed the phone/scope check.
// The gateway's X-Auth-Outcome, when set, is the earliest and most precise
// reason (the token never authenticated, so it has no phone or scope either),
// then a missing phone; with a phone present the scope is what was wrong.
func registrationRefusalCause(outcome, phone string) string {
	switch {
	case outcome != "":
		if _, ok := auth.ResponseForOutcome(outcome); ok {
			return registrationCauseOutcomePrefix + outcome
		}
		return registrationCauseOutcomeUnknown
	case phone == "":
		return RegistrationCauseNoPhone
	default:
		return RegistrationCauseWrongScope
	}
}
