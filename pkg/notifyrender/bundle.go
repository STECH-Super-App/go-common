package notifyrender

import (
	"embed"
	"io/fs"

	"golang.org/x/text/language"

	"github.com/STECH-Super-App/go-common/pkg/i18n"
)

//go:embed translations
var translationsFS embed.FS

// bundle is the package-level i18n bundle wired to the embedded
// TOML translations. Initialized in init; panics on configuration
// failure (translations directory missing or malformed) because that
// is a build-time/asset error, not a runtime condition.
var bundle *i18n.Bundle

func init() {
	sub, err := fs.Sub(translationsFS, "translations")
	if err != nil {
		panic("notifyrender: failed to sub embed FS: " + err.Error())
	}
	b, err := i18n.LoadBundle(sub, language.English)
	if err != nil {
		panic("notifyrender: failed to build i18n bundle: " + err.Error())
	}
	bundle = b
}
