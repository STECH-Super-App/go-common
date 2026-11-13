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

	// compiled holds every value above parsed once, at snapshot build, so a
	// render is an Execute and not a Parse (SL-07). Keyed [layer][key]; the
	// baseline's layer is baselineLayer. *template.Template is safe for
	// concurrent Execute.
	compiled map[string]map[string]*compiledTemplate
	logger   *zap.Logger
	ns       string
}

// baselineLayer is the compiled-map layer name of the compiled-in baseline.
// It cannot collide with a locale tag.
const baselineLayer = ""

type compiledTemplate struct {
	tmpl *template.Template
	err  error // parse error; every Execute of this value fails with it
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
	dirPath  string // "" => baselines-only; re-stat'd each Load/Reload for late mounts
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

// NewOverlayBundle builds a bundle over the given baselines and an overlay
// directory PATH (not a frozen fs.FS). The path is re-stat'd on every
// Load/Reload, so an overlay mount that appears after boot is picked up by the
// next Reload — no restart needed. An empty dirPath means baselines-only (still
// valid for tests and local runs without a mount). The returned bundle already
// holds an empty (baselines-only) Snapshot so Snapshot() is safe to call before
// the first Load.
func NewOverlayBundle(baseline map[string]string, dirPath string, opts ...Option) *OverlayBundle {
	if baseline == nil {
		baseline = map[string]string{}
	}
	b := &OverlayBundle{
		baseline: baseline,
		dirPath:  dirPath,
		logger:   zap.NewNop(),
	}
	for _, o := range opts {
		o(b)
	}
	b.cur.Store(newSnapshot(baseline, map[string]map[string]string{}, nil, b.logger, b.ns))
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
func newSnapshot(baseline map[string]string, overlay map[string]map[string]string, tagSet map[string]language.Tag, logger *zap.Logger, ns string) *Snapshot {
	tags := []language.Tag{language.English}
	for tagKey, tag := range tagSet {
		if tagKey == "en" {
			continue
		}
		tags = append(tags, tag)
	}
	compiled := make(map[string]map[string]*compiledTemplate, len(overlay)+1)
	compiled[baselineLayer] = compileAll(baseline)
	for tag, values := range overlay {
		compiled[tag] = compileAll(values)
	}
	return &Snapshot{
		overlay:  overlay,
		baseline: baseline,
		tags:     tags,
		compiled: compiled,
		logger:   logger,
		ns:       ns,
	}
}

func compileAll(values map[string]string) map[string]*compiledTemplate {
	out := make(map[string]*compiledTemplate, len(values))
	for key, val := range values {
		t, err := parseTemplate(val)
		out[key] = &compiledTemplate{tmpl: t, err: err}
	}
	return out
}

// Resolve renders key for the requested locale. Resolution order (spec F6):
// matched-locale overlay -> en overlay -> baseline -> ErrKeyNotFound. Matching
// is by base language, so "ru-RU" resolves to a supported "ru" overlay and any
// unsupported locale (e.g. "kk") falls through to en. We deliberately avoid
// language.Matcher's fuzzy distance data here: it maps kk->ru on regional
// closeness, which would wrongly beat the en default.
func (s *Snapshot) Resolve(locale, key string, params map[string]any) (string, error) {
	matchedTag := s.matchLocale(locale)

	// A layer whose value cannot be rendered is skipped, not returned: one bad
	// translation must degrade to the next layer (finally the English baseline),
	// not fail every message of its type (SL-07).
	layers := []string{matchedTag}
	if matchedTag != "en" {
		layers = append(layers, "en")
	}
	layers = append(layers, baselineLayer)

	var firstErr error
	for _, layer := range layers {
		c, ok := s.compiled[layer][key]
		if !ok {
			continue
		}
		out, err := c.render(params)
		if err == nil {
			return out, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		s.logger.Warn("i18n value failed to render; falling back to the next layer",
			zap.String("namespace", s.ns), zap.String("locale", layer),
			zap.String("key", key), zap.Error(err))
	}
	if firstErr != nil {
		return "", firstErr
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

// parseTemplate parses a translation value with missingkey=error, so an
// undeclared placeholder surfaces as ErrTranslationFailed rather than an
// "<no value>" leak.
func parseTemplate(tmplStr string) (*template.Template, error) {
	t, err := template.New("i18n").Option("missingkey=error").Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	return t, nil
}

// render executes the compiled template. Both parse and execute failures map to
// the same sentinel.
func (c *compiledTemplate) render(params map[string]any) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	var buf bytes.Buffer
	if err := c.tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTranslationFailed, err)
	}
	return buf.String(), nil
}
