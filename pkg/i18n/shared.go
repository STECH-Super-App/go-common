package i18n

import (
	"embed"
	"io/fs"

	"golang.org/x/text/language"
)

//go:embed translations
var sharedFS embed.FS

// SharedBundle returns a Bundle loaded with go-common's embedded shared
// COMMON_* translations (the error.* reason strings).
//
// These strings are FRONTEND-rendered: a service emits a machine-readable
// Reason code in its HTTP error (see e.g. notification-service/REASONS.md),
// and the frontend localizes that code into user-facing text. No Go service
// renders them server-side — SharedBundle has no production caller (only
// tests). The embedded files exist as the canonical en source that the i18n
// push workflow ships to Tolgee for the frontend to pull and translate.
func SharedBundle() (*Bundle, error) {
	sub, err := fs.Sub(sharedFS, "translations")
	if err != nil {
		return nil, err
	}
	return LoadBundle(sub, language.English)
}
