// Package i18n provides an overlay-based localization engine for STECH
// services. A bundle holds a compiled-in en baseline (the source of truth) and
// an optional filesystem overlay of per-locale JSON translations; Load/Reload
// build an immutable Snapshot that resolves keys with locale fallback
// (matched-locale overlay -> en overlay -> baseline). Reason codes emitted in
// typed errors are the canonical i18n keys the frontend renders from.
package i18n

import (
	"bytes"
	"fmt"
	"io/fs"
	"sync/atomic"
	"text/template"

	"go.uber.org/zap"
	"golang.org/x/text/language"
)

// Snapshot is an immutable view of the resolved overlay + baselines. A given
// Snapshot never changes after publication; Reload swaps in a brand-new one.
// Multi-key operations (e.g. a notification's title + body) MUST resolve every
// key against ONE Snapshot so a concurrent Reload cannot tear the pair (spec F4).
type Snapshot struct {
	overlay  map[string]map[string]string // localeTag -> dotted key -> template
	baseline map[string]string            // en, dotted keys
	tags     []language.Tag               // supported tags; index 0 is en (default)
}

// Resolver hands out the current immutable Snapshot. Consumers resolve through
// the Snapshot, never through the bundle directly, so a title/body pair is
// always rendered against one consistent view (spec F4).
type Resolver interface {
	Snapshot() *Snapshot
}

// OverlayBundle holds the baselines (compiled-in en source of truth) and an
// optional filesystem overlay of per-locale JSON translations. Load/Reload
// build an immutable Snapshot and publish it atomically.
type OverlayBundle struct {
	baseline map[string]string
	dir      fs.FS // nil => baselines-only
	ns       string
	logger   *zap.Logger
	allowed  func(key string) ([]string, bool)
	cur      atomic.Pointer[Snapshot]
}

// Option configures an OverlayBundle at construction.
type Option func(*OverlayBundle)

// WithNamespace tags the bundle (and its LoadSummary) with the catalog
// namespace it serves (e.g. "notifyrender"). Empty string if unset.
func WithNamespace(ns string) Option {
	return func(b *OverlayBundle) { b.ns = ns }
}

// WithLogger injects a structured logger for load-time warnings. A nil logger
// is ignored (the bundle keeps its no-op default).
func WithLogger(l *zap.Logger) Option {
	return func(b *OverlayBundle) {
		if l != nil {
			b.logger = l
		}
	}
}

// WithAllowedParams supplies the placeholder allow-list policy. For a key it
// returns (allowed placeholder names, true) to enforce that exact set, or
// (_, false) to defer to the baseline's own placeholders (spec F2). When unset,
// every key defers to its baseline placeholders.
func WithAllowedParams(fn func(key string) ([]string, bool)) Option {
	return func(b *OverlayBundle) { b.allowed = fn }
}

// NewOverlayBundle builds a bundle over the given baselines and overlay dir.
// dir may be nil, in which case Load resolves from baselines only. The returned
// bundle already holds an empty (baselines-only) Snapshot so Snapshot() is safe
// to call before the first Load.
func NewOverlayBundle(baseline map[string]string, dir fs.FS, opts ...Option) *OverlayBundle {
	if baseline == nil {
		baseline = map[string]string{}
	}
	b := &OverlayBundle{
		baseline: baseline,
		dir:      dir,
		logger:   zap.NewNop(),
	}
	for _, o := range opts {
		o(b)
	}
	b.cur.Store(newSnapshot(baseline, map[string]map[string]string{}, nil))
	return b
}

// Snapshot returns the current immutable view. It never returns nil once the
// bundle is constructed.
func (b *OverlayBundle) Snapshot() *Snapshot {
	return b.cur.Load()
}

// newSnapshot assembles an immutable Snapshot. tagSet maps a locale tag string
// (e.g. "ru") to its parsed language.Tag; en is always the default/fallback and
// is placed first in the supported-tag list (spec F6).
func newSnapshot(baseline map[string]string, overlay map[string]map[string]string, tagSet map[string]language.Tag) *Snapshot {
	tags := []language.Tag{language.English}
	for tagKey, tag := range tagSet {
		if tagKey == "en" {
			continue
		}
		tags = append(tags, tag)
	}
	return &Snapshot{
		overlay:  overlay,
		baseline: baseline,
		tags:     tags,
	}
}

// Resolve renders key for the requested locale. Resolution order (spec F6):
// matched-locale overlay -> en overlay -> baseline -> ErrKeyNotFound. Matching
// is by base language, so "ru-RU" resolves to a supported "ru" overlay and any
// unsupported locale (e.g. "kk") falls through to en. We deliberately avoid
// language.Matcher's fuzzy distance data here: it maps kk->ru on regional
// closeness, which would wrongly beat the en default.
func (s *Snapshot) Resolve(locale, key string, params map[string]any) (string, error) {
	matchedTag := s.matchLocale(locale)

	if tmpl, ok := s.overlay[matchedTag][key]; ok {
		return render(tmpl, params)
	}
	if tmpl, ok := s.overlay["en"][key]; ok {
		return render(tmpl, params)
	}
	if tmpl, ok := s.baseline[key]; ok {
		return render(tmpl, params)
	}
	return "", ErrKeyNotFound
}

// matchLocale resolves a requested locale to a supported locale tag string by
// base-language equality, defaulting to "en" when nothing matches.
func (s *Snapshot) matchLocale(locale string) string {
	tag, err := language.Parse(locale)
	if err != nil {
		return "en"
	}
	base, conf := tag.Base()
	if conf == language.No {
		return "en"
	}
	for _, t := range s.tags {
		if tb, _ := t.Base(); tb == base {
			return t.String()
		}
	}
	return "en"
}

// render executes a template string with missingkey=error so an undeclared
// placeholder surfaces as ErrTranslationFailed rather than an "<no value>"
// leak. Both parse and execute failures map to the same sentinel.
func render(tmplStr string, params map[string]any) (string, error) {
	t, err := template.New("i18n").Option("missingkey=error").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	return buf.String(), nil
}
