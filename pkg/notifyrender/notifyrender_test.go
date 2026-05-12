package notifyrender

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	notificationv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/notification/v1"

	commonerr "github.com/STECH-Super-App/go-common/pkg/errors"
)

// validParamsFor builds a complete params map for the given type so
// Render's required-param check passes.
func validParamsFor(t notificationv1.NotificationType) map[string]string {
	out := make(map[string]string)
	for _, p := range requiredParams[t] {
		out[p] = "test_" + p
	}
	return out
}

// assertReason unwraps an AppError and checks its Reason matches want.
func assertReason(t *testing.T, err error, want string) {
	t.Helper()
	var appErr *commonerr.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T: %v", err, err)
	}
	if appErr.Reason != want {
		t.Errorf("Reason = %q, want %q", appErr.Reason, want)
	}
}

func TestRenderEveryTypeEveryLocale(t *testing.T) {
	locales := []string{"en", "ru", "kk"}
	for nt := range typeKey {
		nt := nt
		for _, loc := range locales {
			loc := loc
			t.Run(nt.String()+"_"+loc, func(t *testing.T) {
				title, body, err := Render(nt, validParamsFor(nt), loc)
				if err != nil {
					t.Fatalf("Render returned err: %v", err)
				}
				if title == "" {
					t.Errorf("empty title for %s/%s", nt, loc)
				}
				if body == "" {
					t.Errorf("empty body for %s/%s", nt, loc)
				}
			})
		}
	}
}

func TestRenderMissingParam(t *testing.T) {
	nt := notificationv1.NotificationType_NOTIFICATION_TYPE_LISTING_REJECTED
	params := map[string]string{"listing_title": "X"} // missing "reason"
	_, _, err := Render(nt, params, "en")
	assertReason(t, err, ReasonMissingParam)
	var appErr *commonerr.AppError
	if errors.As(err, &appErr) {
		if appErr.Params["param"] != "reason" {
			t.Errorf("params[param] = %v, want 'reason'", appErr.Params["param"])
		}
	}
}

func TestRenderUnknownType(t *testing.T) {
	_, _, err := Render(notificationv1.NotificationType_NOTIFICATION_TYPE_UNSPECIFIED,
		map[string]string{}, "en")
	assertReason(t, err, ReasonUnknownType)

	_, _, err = Render(notificationv1.NotificationType_NOTIFICATION_TYPE_SYSTEM,
		map[string]string{}, "en")
	assertReason(t, err, ReasonUnknownType)
}

// TestCatalogVsTemplateConsistency parses each locale's TOML file and
// verifies every {{.field}} placeholder is in requiredParams[T] for the
// matching type, and vice versa.
func TestCatalogVsTemplateConsistency(t *testing.T) {
	placeholderRe := regexp.MustCompile(`\{\{\.([a-z_]+)\}\}`)
	for _, loc := range []string{"en", "ru", "kk"} {
		loc := loc
		t.Run(loc, func(t *testing.T) {
			// loc is a hard-coded literal from the slice above, not user input.
			data, err := os.ReadFile("translations/" + loc + ".toml") // #nosec G304
			if err != nil {
				t.Fatalf("read translations: %v", err)
			}
			content := string(data)
			for nt, key := range typeKey {
				placeholdersInTemplate := map[string]bool{}
				section := extractSection(content, key)
				for _, m := range placeholderRe.FindAllStringSubmatch(section, -1) {
					placeholdersInTemplate[m[1]] = true
				}
				required := requiredParams[nt]
				requiredSet := map[string]bool{}
				for _, r := range required {
					requiredSet[r] = true
				}
				for p := range placeholdersInTemplate {
					if !requiredSet[p] {
						t.Errorf("%s/%s: placeholder %q in TOML but not in requiredParams",
							loc, key, p)
					}
				}
				for r := range requiredSet {
					if !placeholdersInTemplate[r] {
						t.Errorf("%s/%s: required param %q not used in TOML placeholders",
							loc, key, r)
					}
				}
			}
		})
	}
}

// extractSection returns the lines belonging to a single [<section>] block.
func extractSection(content, section string) string {
	marker := "[" + section + "]"
	idx := strings.Index(content, marker)
	if idx < 0 {
		return ""
	}
	rest := content[idx:]
	nextHeader := regexp.MustCompile(`\n\[[a-z_]+\]`).FindStringIndex(rest[1:])
	if nextHeader == nil {
		return rest
	}
	return rest[:nextHeader[0]+1]
}
