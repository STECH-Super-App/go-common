package notifyrender

import (
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

// typeKeyForTest exposes the unexported typeKey map to the contract test.
func typeKeyForTest() map[notificationv1.NotificationType]string { return typeKey }

// extractPlaceholders returns the {{.field}} names referenced in s, in match
// order (duplicates preserved — callers only test membership).
func extractPlaceholders(s string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// TestBaselineMatchesCatalogContract is the in-Go successor to the old
// ValidateBundleComplete run against a shipped en.json. It asserts, for every
// catalog type, that BaselineEN carries both a .title and .body entry, that
// every placeholder used is declared — required or optional — (forward), and
// that every REQUIRED param is actually used in title or body (reverse — this
// is the unused-param check ValidateBundleComplete performed).
//
// Optional params are exempt from the reverse check by design: they are a
// may-be-empty tier, so a template is free to omit one. TestBaselineGuardsOptionalParams
// covers the shape they must take when a template DOES use one.
func TestBaselineMatchesCatalogContract(t *testing.T) {
	for typ, key := range typeKeyForTest() {
		title, okTitle := BaselineEN[key+".title"]
		body, okBody := BaselineEN[key+".body"]
		if !okTitle {
			t.Errorf("type %v: baseline missing %s.title", typ, key)
		}
		if !okBody {
			t.Errorf("type %v: baseline missing %s.body", typ, key)
		}

		// forward: every placeholder used is a declared param (required or optional).
		for _, part := range []string{title, body} {
			for _, ph := range extractPlaceholders(part) {
				if !contains(allowedParams(typ), ph) {
					t.Errorf("%s uses undeclared {{.%s}}", key, ph)
				}
			}
		}

		// reverse: every REQUIRED param appears in title or body.
		used := append(extractPlaceholders(title), extractPlaceholders(body)...)
		for _, req := range requiredParams[typ] {
			if !contains(used, req) {
				t.Errorf("%s: declared param %q never used in title or body", key, req)
			}
		}
	}
}

// TestBaselineGuardsOptionalParams asserts every baseline string that references
// an OPTIONAL param reads it behind an {{if}} guard on that same param. Without
// the guard an empty value would render a dangling fragment ("Request # expired")
// — the exact defect the guard exists to prevent. This is a structural lock on
// the copy; TestRenderDelivery_RequestNumber locks the rendered strings.
func TestBaselineGuardsOptionalParams(t *testing.T) {
	for typ, key := range typeKeyForTest() {
		for _, opt := range optionalParams[typ] {
			for _, part := range []string{key + ".title", key + ".body"} {
				tmpl := BaselineEN[part]
				if !contains(extractPlaceholders(tmpl), opt) {
					continue // this half does not use the optional param at all.
				}
				if !strings.Contains(tmpl, "{{if ."+opt+"}}") {
					t.Errorf("%s references optional {{.%s}} without an {{if .%s}} guard: %q",
						part, opt, opt, tmpl)
				}
			}
		}
	}
}

// TestBaselineHasNoOrphanSections asserts BaselineEN carries no key whose
// section is absent from the catalog (a stray generated entry).
func TestBaselineHasNoOrphanSections(t *testing.T) {
	for key := range BaselineEN {
		if _, ok := AllowedParamsByKey(key); !ok {
			t.Errorf("baseline key %q maps to no catalog type", key)
		}
	}
}

func TestAllowedParamsByKey(t *testing.T) {
	// Known section, both parts resolve to the same declared param set.
	wantOrder := requiredParams[notificationv1.NotificationType_NOTIFICATION_TYPE_ORDER_CANCELLED]
	for _, key := range []string{"order_cancelled.title", "order_cancelled.body"} {
		got, ok := AllowedParamsByKey(key)
		if !ok {
			t.Fatalf("%s: expected ok", key)
		}
		if len(got) != len(wantOrder) {
			t.Fatalf("%s: params = %v, want %v", key, got, wantOrder)
		}
		for _, p := range wantOrder {
			if !contains(got, p) {
				t.Errorf("%s: missing declared param %q", key, p)
			}
		}
	}

	// Unknown section and missing suffix both return false.
	if _, ok := AllowedParamsByKey("no_such_section.title"); ok {
		t.Error("unknown section must return false")
	}
	if _, ok := AllowedParamsByKey("order_cancelled"); ok {
		t.Error("key without .title/.body suffix must return false")
	}
	if _, ok := AllowedParamsByKey("order_cancelled.footer"); ok {
		t.Error("unknown suffix must return false")
	}
}
