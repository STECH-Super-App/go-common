package i18n_test

import (
	"context"
	"errors"
	"testing"

	i18n "github.com/STECH-Super-App/go-common/pkg/i18n"
)

// helloA is the baseline greeting rendered for name "A".
const helloA = "Hello A"

func nameOnly(_ string) ([]string, bool) { return []string{"name"}, true }

// SL-07: an overlay value was accepted at load without being parsed as a
// template. `{{.name}` (an unclosed action) loaded fine and then failed every
// render of that key.
func TestLoadRejectsUnparseableOverlayValue(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"ru.json": `{"greet":{"title":"Привет {{.name}"}}`,
	}), i18n.WithAllowedParams(nameOnly))

	sum := b.Load(context.Background())

	if len(sum.Rejected) != 1 || sum.Rejected[0] != "ru/greet.title" {
		t.Fatalf("a value that does not parse must be rejected at load, rejected = %v", sum.Rejected)
	}
	if got, err := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "A"}); err != nil || got != helloA {
		t.Fatalf("a rejected value falls back to the baseline, got %q err %v", got, err)
	}
}

// SL-07: the placeholder allow-list was a regexp that only saw a bare
// {{ .name }}; an undeclared name inside any other action form passed the check
// and then failed at render under missingkey=error.
func TestLoadRejectsUndeclaredPlaceholderInEveryActionForm(t *testing.T) {
	forms := map[string]string{
		"if":          `{{if .reqest_no}}x{{end}}`,
		"trim":        `{{- .reqest_no -}}`,
		"pipeline":    `{{ .reqest_no | printf "%s" }}`,
		"index":       `{{index . "reqest_no"}}`,
		"else if":     `{{if .name}}a{{else if .reqest_no}}b{{end}}`,
		"nested with": `{{if eq .name "x"}}{{ .reqest_no }}{{end}}`,
		"field chain": `{{ .reqest_no.sub }}`,
	}
	for name, val := range forms {
		t.Run(name, func(t *testing.T) {
			b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
				"ru.json": `{"greet":{"title":` + jsonString(val) + `}}`,
			}), i18n.WithAllowedParams(nameOnly))
			sum := b.Load(context.Background())
			if len(sum.Rejected) != 1 {
				t.Fatalf("undeclared name in %q must be rejected, rejected = %v", val, sum.Rejected)
			}
		})
	}
}

func TestLoadAcceptsDeclaredPlaceholderInEveryActionForm(t *testing.T) {
	forms := []string{
		`{{if .name}}Hi {{.name}}{{end}}`,
		`{{- .name -}}`,
		`{{ .name | printf "%s" }}`,
		`{{index . "name"}}`,
		`{{if eq .name "x"}}a{{else}}b{{end}}`,
		`no placeholders at all`,
	}
	for _, val := range forms {
		b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
			"ru.json": `{"greet":{"title":` + jsonString(val) + `}}`,
		}), i18n.WithAllowedParams(nameOnly))
		sum := b.Load(context.Background())
		if len(sum.Rejected) != 0 {
			t.Fatalf("%q is valid and declared, but rejected = %v", val, sum.Rejected)
		}
	}
}

// SL-07: Resolve returned the render error of the FIRST layer that had the key
// instead of falling back, so a value that parses but fails at runtime (here a
// comparison of incompatible types) broke every RU message of that type instead
// of degrading to the English baseline.
func TestResolveFallsThroughOnRenderError(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"ru.json": `{"greet":{"title":"{{if gt .name 1}}x{{end}}"}}`,
	}), i18n.WithAllowedParams(nameOnly))
	b.Load(context.Background())

	got, err := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "A"})
	if err != nil || got != helloA {
		t.Fatalf("a render error in an overlay layer must fall through to the next layer, got %q err %v", got, err)
	}
}

func TestResolveReturnsBaselineErrorWhenEveryLayerFails(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, "")
	b.Load(context.Background())

	// "name" is missing: the baseline itself cannot render.
	_, err := b.Snapshot().Resolve("ru", "greet.title", map[string]any{})
	if !errors.Is(err, i18n.ErrTranslationFailed) {
		t.Fatalf("err = %v, want ErrTranslationFailed", err)
	}
}

func TestPlaceholdersWalksTheParseTree(t *testing.T) {
	names, err := i18n.Placeholders(`{{if .a}}{{.b}}{{end}} {{index . "c"}} {{ .d | printf "%s" }}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c", "d"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	if _, err := i18n.Placeholders(`{{.a}`); err == nil {
		t.Fatal("an unparseable template must be an error")
	}
}

func jsonString(s string) string {
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		default:
			out = append(out, string(r)...)
		}
	}
	return string(append(out, '"'))
}
