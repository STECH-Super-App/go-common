package i18n

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/text/language"
)

// LoadSummary reports what a Load/Reload accepted, rejected, and flagged.
// Source is "overlay" when the overlay dir was read (even if some files were
// skipped) or "baselines-only" when the dir was nil or unreadable.
//
// Entry formats:
//   - Rejected: "<localeTag>/<dotted.key>" (a locale value dropped by placeholder policy)
//   - Loaded/Shadowed/Missing: bare "<dotted.key>"
type LoadSummary struct {
	Namespace string
	Source    string
	Loaded    []string // en-locale overlay keys accepted into the snapshot
	Rejected  []string // "<tag>/<key>" values dropped for undeclared placeholders
	Missing   []string // baseline keys absent from the en overlay
	Shadowed  []string // en overlay keys whose value differs from the baseline
}

// placeholderRe matches a Go text/template field reference like {{ .name }}.
var placeholderRe = regexp.MustCompile(`\{\{\s*\.\s*([A-Za-z0-9_]+)\s*\}\}`)

// Load builds a fresh Snapshot from the baselines + overlay dir and publishes
// it. It never returns an error and never panics on a bad overlay: a missing or
// unreadable dir degrades to baselines-only, and individual malformed locale
// files are skipped with a warning (spec D6, F13).
func (b *OverlayBundle) Load(ctx context.Context) *LoadSummary {
	return b.load(ctx)
}

// Reload is Load re-run against the current dir contents. The new Snapshot is
// swapped in atomically; Snapshots already handed out keep their old view.
func (b *OverlayBundle) Reload(ctx context.Context) *LoadSummary {
	return b.load(ctx)
}

func (b *OverlayBundle) load(_ context.Context) *LoadSummary {
	sum := &LoadSummary{Namespace: b.ns}
	overlay := map[string]map[string]string{}
	tagSet := map[string]language.Tag{}

	if b.dir == nil {
		sum.Source = "baselines-only"
		b.logger.Warn("i18n_overlay dir unavailable",
			zap.String("namespace", b.ns), zap.String("reason", "nil dir"))
		b.publish(overlay, tagSet, sum)
		return sum
	}

	entries, err := fs.ReadDir(b.dir, ".")
	if err != nil {
		sum.Source = "baselines-only"
		b.logger.Warn("i18n_overlay dir unavailable",
			zap.String("namespace", b.ns), zap.Error(err))
		b.publish(overlay, tagSet, sum)
		return sum
	}

	sum.Source = "overlay"
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b.loadFile(e.Name(), overlay, tagSet, sum)
	}

	b.computeShadowMissing(overlay["en"], sum)
	b.publish(overlay, tagSet, sum)
	return sum
}

// loadFile parses one <locale>.json overlay file. An unparseable locale name or
// invalid JSON is a skip-with-warn, not a fatal error (spec F13).
func (b *OverlayBundle) loadFile(name string, overlay map[string]map[string]string, tagSet map[string]language.Tag, sum *LoadSummary) {
	base := strings.TrimSuffix(name, ".json")
	tag, err := language.Parse(base)
	if err != nil {
		b.logger.Warn("i18n_overlay locale file skipped",
			zap.String("namespace", b.ns), zap.String("file", name),
			zap.String("reason", "unparseable locale"), zap.Error(err))
		return
	}

	data, err := fs.ReadFile(b.dir, name)
	if err != nil {
		b.logger.Warn("i18n_overlay locale file skipped",
			zap.String("namespace", b.ns), zap.String("file", name),
			zap.String("reason", "read failed"), zap.Error(err))
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		b.logger.Warn("i18n_overlay locale file skipped",
			zap.String("namespace", b.ns), zap.String("file", name),
			zap.String("reason", "invalid json"), zap.Error(err))
		return
	}

	flat := map[string]string{}
	flatten("", raw, flat)

	tagKey := tag.String()
	dst := overlay[tagKey]
	if dst == nil {
		dst = map[string]string{}
	}
	for key, val := range flat {
		if !subset(extractPlaceholders(val), b.allowedFor(key)) {
			sum.Rejected = append(sum.Rejected, tagKey+"/"+key)
			b.logger.Warn("i18n_overlay value rejected: undeclared placeholder",
				zap.String("namespace", b.ns), zap.String("locale", tagKey),
				zap.String("key", key))
			continue
		}
		dst[key] = val
		if tagKey == "en" {
			sum.Loaded = append(sum.Loaded, key)
		}
	}
	overlay[tagKey] = dst
	tagSet[tagKey] = tag
}

// computeShadowMissing derives the Shadowed and Missing lists from the accepted
// en overlay against the baselines (spec F3, D5). Only meaningful when an
// overlay was read; for baselines-only the en overlay is empty and both lists
// stay empty.
func (b *OverlayBundle) computeShadowMissing(enOverlay map[string]string, sum *LoadSummary) {
	for key, val := range enOverlay {
		if bval, ok := b.baseline[key]; ok && bval != val {
			sum.Shadowed = append(sum.Shadowed, key)
			b.logger.Warn("i18n_overlay en value shadows baseline",
				zap.String("namespace", b.ns), zap.String("key", key))
		}
	}
	for key := range b.baseline {
		if _, ok := enOverlay[key]; !ok {
			sum.Missing = append(sum.Missing, key)
		}
	}
}

// publish sorts the summary lists for stable output and swaps the new Snapshot
// in atomically.
func (b *OverlayBundle) publish(overlay map[string]map[string]string, tagSet map[string]language.Tag, sum *LoadSummary) {
	sort.Strings(sum.Loaded)
	sort.Strings(sum.Rejected)
	sort.Strings(sum.Missing)
	sort.Strings(sum.Shadowed)
	b.cur.Store(newSnapshot(b.baseline, overlay, tagSet))
}

// allowedFor returns the permitted placeholder set for a key. A configured
// policy that claims the key (returns true) wins; otherwise the key defers to
// its baseline's own placeholders (spec F2).
func (b *OverlayBundle) allowedFor(key string) map[string]bool {
	if b.allowed != nil {
		if names, ok := b.allowed(key); ok {
			out := make(map[string]bool, len(names))
			for _, n := range names {
				out[n] = true
			}
			return out
		}
	}
	return extractPlaceholders(b.baseline[key])
}

// flatten collapses nested JSON objects into dotted keys, matching the
// jq `paths(scalars)|join(".")` convention the old parity script used.
func flatten(prefix string, node map[string]any, out map[string]string) {
	for k, v := range node {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch child := v.(type) {
		case map[string]any:
			flatten(key, child, out)
		case string:
			out[key] = child
		default:
			out[key] = fmt.Sprint(child)
		}
	}
}

// extractPlaceholders returns the set of template field names referenced in s.
func extractPlaceholders(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out[m[1]] = true
	}
	return out
}

// subset reports whether every element of sub is present in super.
func subset(sub, super map[string]bool) bool {
	for k := range sub {
		if !super[k] {
			return false
		}
	}
	return true
}
