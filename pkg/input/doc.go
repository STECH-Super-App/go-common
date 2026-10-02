// Package input is the fleet's one validation package for values that enter a
// service: REST bodies, path and query parameters, and gRPC fields.
//
// Every validator returns the CLEANED value next to a *Failure. The caller
// stores, forwards and publishes the cleaned value only — never the raw input —
// so "the value that was validated" and "the value that was stored" cannot
// differ.
//
// The package is status-free: it knows nothing about HTTP, Echo or logging. A
// Failure carries a COMMON_* Reason (the i18n key) and Params; the service picks
// the HTTP status when it wraps the Failure into an AppError, and the field name
// is attached with Failure.At by whoever knows it.
//
// Caps are mandatory by signature: every text validator takes a max length in
// runes, every number validator takes a min and a max. A non-positive text cap
// or an inverted range is a programming error and panics at the call site, so
// the first unit test that exercises the call catches it.
//
// Spec: dev-setup docs/superpowers/specs/2026-10-02-input-validation-standard-design.md §4.
package input
