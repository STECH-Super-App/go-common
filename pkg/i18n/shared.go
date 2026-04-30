package i18n

import (
	"embed"
	"io/fs"

	"golang.org/x/text/language"
)

//go:embed translations
var sharedFS embed.FS

// SharedBundle returns a Bundle loaded with go-common's embedded shared
// translations. Services use this as a layered fallback after their
// own service bundle.
func SharedBundle() (*Bundle, error) {
	sub, err := fs.Sub(sharedFS, "translations")
	if err != nil {
		return nil, err
	}
	return LoadBundle(sub, language.English)
}
