// Package i18n wraps nicksnyder/go-i18n/v2 with a small, opinionated API
// for STECH services. It loads translations from an fs.FS containing
// TOML or JSON files named <locale>.toml / <locale>.json (e.g., en.json,
// ru.json), resolves keys with locale-fallback semantics, and
// emits warn logs
// when a target-locale translation is missing but a default-locale one
// exists.
//
// Example:
//
//	//go:embed translations
//	var translationsFS embed.FS
//
//	func newBundle() (*i18n.Bundle, error) {
//	    sub, _ := fs.Sub(translationsFS, "translations")
//	    return i18n.LoadBundle(sub, language.English)
//	}
//
//	str, err := bundle.Resolve("ru", "tenant.transfer.expired_sms", map[string]any{
//	    "ExpiryTime": t.ExpiryTime.Format(time.RFC3339),
//	})
package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/STECH-Super-App/go-common/pkg/logger"
)

// Bundle wraps a go-i18n bundle with STECH-specific semantics.
type Bundle struct {
	inner   *goi18n.Bundle
	matcher language.Matcher
	tags    []language.Tag
	def     language.Tag
}

// LoadBundle reads every <locale>.toml / <locale>.json from fsys and returns
// a Bundle with the given default locale. Default is used as the final
// fallback when a requested locale's translation is missing.
func LoadBundle(fsys fs.FS, def language.Tag) (*Bundle, error) {
	inner := goi18n.NewBundle(def)
	inner.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	inner.RegisterUnmarshalFunc("json", json.Unmarshal)

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read fs: %w", err)
	}

	var tags []language.Tag
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := path.Ext(e.Name()) // ".toml" or ".json"
		if ext != ".toml" && ext != ".json" {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		if _, err := inner.ParseMessageFileBytes(data, e.Name()); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		base := strings.TrimSuffix(path.Base(e.Name()), ext)
		tag, err := language.Parse(base)
		if err != nil {
			return nil, fmt.Errorf("parse locale %q: %w", base, err)
		}
		tags = append(tags, tag)
	}

	return &Bundle{
		inner:   inner,
		matcher: language.NewMatcher(tags),
		tags:    tags,
		def:     def,
	}, nil
}

// Resolve looks up key in the bundle for the requested locale.
// Locale matching uses golang.org/x/text/language: "kk-KZ" resolves
// to "kk" if kk is loaded; if the key is missing in the matched locale
// but exists in the default locale, the default rendering is returned
// and a warn log is emitted. If the key is missing everywhere,
// ErrKeyNotFound is returned. If the engine reports a template error,
// ErrTranslationFailed is returned.
func (b *Bundle) Resolve(locale, key string, params map[string]any) (string, error) {
	tag, err := language.Parse(locale)
	if err != nil {
		tag = b.def
	}
	matched, _, _ := b.matcher.Match(tag)

	loc := goi18n.NewLocalizer(b.inner, matched.String(), b.def.String())
	str, err := loc.Localize(&goi18n.LocalizeConfig{
		MessageID:    key,
		TemplateData: params,
	})
	if err == nil {
		return str, nil
	}
	var notFound *goi18n.MessageNotFoundErr
	if errors.As(err, &notFound) {
		// Try the default locale explicitly to detect "key exists in default but not target".
		defLoc := goi18n.NewLocalizer(b.inner, b.def.String())
		defStr, defErr := defLoc.Localize(&goi18n.LocalizeConfig{
			MessageID:    key,
			TemplateData: params,
		})
		if defErr == nil {
			logger.Warn("i18n: translation missing in target locale, served default",
				logger.String("key", key),
				logger.String("requested_locale", locale),
				logger.String("default_locale", b.def.String()),
			)
			return defStr, nil
		}
		return "", ErrKeyNotFound
	}
	return "", fmt.Errorf("%w: %v", ErrTranslationFailed, err)
}
