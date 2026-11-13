package notifyrender

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestValidateBundle_Valid(t *testing.T) {
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
			"listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."}
		}`)},
	}
	if err := ValidateBundle(fsys); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateBundle_UnknownPlaceholder(t *testing.T) {
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
			"listing_approved": {"title": "Listing approved", "body": "{{.listing_title}} {{.bogus}}"}
		}`)},
	}
	err := ValidateBundle(fsys)
	if err == nil {
		t.Fatal("expected error for undeclared placeholder {{.bogus}}")
	}
}

func TestValidateBundle_MissingRequiredParam(t *testing.T) {
	// listing_approved requires listing_title; template omits it entirely.
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
			"listing_approved": {"title": "Listing approved", "body": "static text"}
		}`)},
	}
	err := ValidateBundle(fsys)
	if err == nil {
		t.Fatal("expected error for unused required param listing_title")
	}
}

func TestValidateBundle_UnknownSection(t *testing.T) {
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
			"not_a_real_type": {"title": "x", "body": "y"}
		}`)},
	}
	if err := ValidateBundle(fsys); err == nil {
		t.Fatal("expected error for section with no matching NotificationType")
	}
}

// buildMinimalCatalogFS returns a MapFS with a minimal valid en.json covering
// every catalog type in typeKey. Each section includes all requiredParams as
// placeholders in the body, satisfying the consistency checks in ValidateBundle.
func buildMinimalCatalogFS() fstest.MapFS {
	type sec struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	m := make(map[string]sec, len(typeKey))
	for t, name := range typeKey {
		parts := make([]string, 0, len(requiredParams[t])+1)
		parts = append(parts, "text")
		for _, p := range requiredParams[t] {
			parts = append(parts, "{{."+p+"}}")
		}
		m[name] = sec{Title: name, Body: strings.Join(parts, " ")}
	}
	data, err := json.Marshal(m)
	if err != nil {
		panic("buildMinimalCatalogFS: " + err.Error())
	}
	return fstest.MapFS{"en.json": {Data: data}}
}

func TestValidateBundleComplete_AllPresent(t *testing.T) {
	fsys := buildMinimalCatalogFS()
	if err := ValidateBundleComplete(fsys); err != nil {
		t.Fatalf("expected nil for complete catalog, got: %v", err)
	}
}

func TestValidateBundleComplete_MissingSection(t *testing.T) {
	// Single-type fixture: only listing_approved is present; all other catalog
	// types are absent, so ValidateBundleComplete must return a non-nil error.
	fsys := fstest.MapFS{
		"en.json": {Data: []byte(`{
			"listing_approved": {"title": "Listing approved", "body": "Your listing '{{.listing_title}}' is now live."}
		}`)},
	}
	if err := ValidateBundleComplete(fsys); err == nil {
		t.Fatal("expected non-nil error: single-type fixture is incomplete")
	}
}

// SL-07: the validator's regexp saw only a bare {{ .name }}, so an undeclared
// name inside {{if}} / {{- -}} / a pipeline passed CI in i18n-catalog and then
// failed at render; an unparseable value passed too.
func TestValidateBundle_UndeclaredPlaceholderInOtherActionForms(t *testing.T) {
	for name, body := range map[string]string{
		"if":       `{{.listing_title}} {{if .bogus}}x{{end}}`,
		"trim":     `{{.listing_title}} {{- .bogus -}}`,
		"pipeline": `{{.listing_title}} {{ .bogus | printf "%s" }}`,
		"index":    `{{.listing_title}} {{index . "bogus"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{"en.json": {Data: []byte(`{"listing_approved": {"title": "t", "body": ` + quoteJSON(body) + `}}`)}}
			if err := ValidateBundle(fsys); err == nil {
				t.Fatalf("expected an undeclared-placeholder error for %q", body)
			}
		})
	}
}

func TestValidateBundle_UnparseableTemplate(t *testing.T) {
	fsys := fstest.MapFS{"en.json": {Data: []byte(`{"listing_approved": {"title": "t", "body": "{{.listing_title}"}}`)}}
	if err := ValidateBundle(fsys); err == nil {
		t.Fatal("expected an error for a value that is not a valid template")
	}
}

func TestValidateBundle_RequiredParamUsedInsideIf(t *testing.T) {
	fsys := fstest.MapFS{"en.json": {Data: []byte(`{"listing_approved": {"title": "t", "body": "{{if .listing_title}}{{.listing_title}}{{end}}"}}`)}}
	if err := ValidateBundle(fsys); err != nil {
		t.Fatalf("a required param read inside {{if}} is used: %v", err)
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
