package notifyrender

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"
)

var placeholderRe = regexp.MustCompile(`{{\s*\.(\w+)\s*}}`)

type section struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// readBundle reads and parses en.json from fsys. It also builds the reverse
// typeKey lookup (section name → NotificationType). Called by both validators.
func readBundle(fsys fs.FS) (map[string]section, map[string]notificationv1.NotificationType, error) {
	raw, err := fs.ReadFile(fsys, "en.json")
	if err != nil {
		return nil, nil, fmt.Errorf("notifyrender: read en.json: %w", err)
	}
	var sections map[string]section
	if err := json.Unmarshal(raw, &sections); err != nil {
		return nil, nil, fmt.Errorf("notifyrender: parse en.json: %w", err)
	}

	byName := make(map[string]notificationv1.NotificationType, len(typeKey))
	for t, name := range typeKey {
		byName[name] = t
	}

	return sections, byName, nil
}

// consistencyCheck validates the sections that are present in the bundle:
//   - each section name maps to a known NotificationType (via byName),
//   - every {{.placeholder}} used is declared for that type (required OR optional),
//   - every requiredParam appears in at least one placeholder.
//
// Optional params are deliberately NOT required to appear: a translation may
// legitimately drop the delivery request number from a sentence, and the
// baseline guards it behind an {{if}} anyway.
//
// Returns a (possibly empty) slice of problem strings. The caller aggregates
// and formats them.
func consistencyCheck(sections map[string]section, byName map[string]notificationv1.NotificationType) []string {
	var problems []string

	for name, sec := range sections {
		t, ok := byName[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("section %q has no matching NotificationType", name))
			continue
		}
		used := map[string]bool{}
		for _, m := range placeholderRe.FindAllStringSubmatch(sec.Title+" "+sec.Body, -1) {
			used[m[1]] = true
		}
		allowed := map[string]bool{}
		for _, p := range allowedParams(t) {
			allowed[p] = true
		}
		for p := range used {
			if !allowed[p] {
				problems = append(problems, fmt.Sprintf("section %q: undeclared placeholder {{.%s}}", name, p))
			}
		}
		for _, p := range requiredParams[t] {
			if !used[p] {
				problems = append(problems, fmt.Sprintf("section %q: required param %q never used in a placeholder", name, p))
			}
		}
	}

	return problems
}

// ValidateBundle asserts the source-locale bundle (en.json in fsys) is
// consistent with the compiled catalog contract:
//   - every section name maps to a known NotificationType (reverse typeKey),
//   - every {{.placeholder}} used is declared in requiredParams for that type,
//   - every requiredParam appears in at least one placeholder.
//
// A partial bundle (only a subset of catalog types) is accepted; the function
// validates consistency of what is present, not completeness. Run it against
// the full en.json in i18n-catalog CI to catch missing sections there.
//
// Returns a single aggregated error, or nil when clean.
func ValidateBundle(fsys fs.FS) error {
	sections, byName, err := readBundle(fsys)
	if err != nil {
		return err
	}

	problems := consistencyCheck(sections, byName)
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("notifyrender: bundle validation failed:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// ValidateBundleComplete runs ValidateBundle's consistency checks AND
// asserts every catalog NotificationType has a section in en.json. Use
// this against the real shipped bundle (i18n-catalog CI). ValidateBundle
// stays consistency-only so unit tests can use minimal single-type fixtures.
func ValidateBundleComplete(fsys fs.FS) error {
	sections, byName, err := readBundle(fsys)
	if err != nil {
		return err
	}

	problems := consistencyCheck(sections, byName)

	// Completeness: every catalog type must have a section present.
	for t, name := range typeKey {
		if _, ok := sections[name]; !ok {
			problems = append(problems, fmt.Sprintf("catalog type %s (%q) has no section in en.json", t, name))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("notifyrender: bundle validation failed:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}
