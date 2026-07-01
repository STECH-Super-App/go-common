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
