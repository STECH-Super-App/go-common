package input_test

import (
	"strings"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/input"
)

// SL-17: trimEdges stripped every leading and trailing Cf rune. Emoji tag
// sequences END in Cf tag characters (U+E0020–U+E007F), so a text ending in the
// flag of Scotland was stored as a plain black flag, silently different from
// what was typed; the Arabic number signs U+0600–U+0605 that START a number
// were dropped the same way.
func TestText_TrimKeepsMeaningfulFormatCharacters(t *testing.T) {
	scotland := "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F"
	cases := map[string]string{
		"flag sequence at the end":    "Go " + scotland,
		"flag sequence alone":         scotland,
		"arabic number sign at start": "\u0600123",
		"arabic end of ayah":          "\u06DD12",
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			assertAccepted(t, "Line", v, v, false)
			assertAccepted(t, "Body", v, v, true)
		})
	}
}

func TestText_TrimStillRemovesInvisibleEdgeJunk(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"BOM ZWSP ZWNJ ZWJ", "\uFEFF\u200B\u200C\u200Dhello\u200D\u200C\u200B\uFEFF", "hello"},
		{"bidi marks and controls", "\u200E\u200F\u202A\u202B\u202C\u202D\u202Ehello\u2066\u2067\u2068\u2069", "hello"},
		{"word joiner and invisible operators", "\u2060\u2061\u2062\u2063\u2064hello\u2060", "hello"},
		{"soft hyphen and arabic letter mark", "\u00AD\u061Chello\u061C\u00AD", "hello"},
		{"deprecated format controls", "\u206Ahello\u206F", "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertAccepted(t, "Line", tc.raw, tc.want, false)
			assertAccepted(t, "Body", tc.raw, tc.want, true)
		})
	}
}

// SL-18: Email put no rule on the local part beyond "no whitespace/Cc/Cf, one @".
func TestEmail_LocalPartIsDotAtomASCII(t *testing.T) {
	bad := []string{
		"a,b@x.ru",                        // two recipients in an SMTP To: header
		"<a>@x.ru",                        // angle brackets
		`"a b"@x.ru`,                      // quoted string
		"a(b)@x.ru",                       // comment
		"a;b@x.ru",                        //
		"аdmin@company.ru",                // Cyrillic «а» beside the real admin@company.ru
		"имя@example.com",                 // any non-ASCII letter
		".a@x.ru",                         // leading dot
		"a.@x.ru",                         // trailing dot
		"a..b@x.ru",                       // consecutive dots
		"a b@x.ru",                        //
		"a@127.0.0.1",                     // numeric host
		"a@1.2.3.4",                       //
		"a@x.123",                         // numeric TLD
		strings.Repeat("a", 65) + "@x.ru", // local part > 64
		"a@" + strings.Repeat(strings.Repeat("b", 63)+".", 4) + "ru", // domain > 253
		"a@" + strings.Repeat("b", 64) + ".ru",                       // label > 63
	}
	for _, raw := range bad {
		if got, f := input.Email(raw, 1000); f == nil || f.Reason != input.ReasonEmailInvalid {
			t.Errorf("Email(%q) = (%q, %v), want %s", raw, got, f, input.ReasonEmailInvalid)
		}
	}

	ok := []string{
		"user@example.com", "a.b.c@example.com", "a+tag@example.com", "o'brien@example.com",
		"a_b-c@example.com", "!#$%&'*+-/=?^_`{|}~@example.com", strings.Repeat("a", 64) + "@x.ru",
		"a@" + strings.Repeat("b", 63) + ".ru",
	}
	for _, raw := range ok {
		if got, f := input.Email(raw, 1000); f != nil {
			t.Errorf("Email(%q) = (%q, %v), want accepted", raw, got, f)
		}
	}
}

func TestEmail_TotalLengthIs254(t *testing.T) {
	// Each part is within its own limit (64-byte local part, 63-byte labels,
	// 195-byte domain), but the whole is 260 bytes: over RFC 5321's 254.
	label := strings.Repeat("b", 63)
	domain := label + "." + label + "." + label + "." + strings.Repeat("c", 3) // 63*3+3+3 = 195
	local := strings.Repeat("a", 64)
	addr := local + "@" + domain
	if len(addr) <= 254 {
		t.Skipf("fixture is only %d bytes", len(addr))
	}
	if _, f := input.Email(addr, 1000); f == nil || f.Reason != input.ReasonEmailInvalid {
		t.Errorf("a %d-byte address must be refused as invalid, got %v", len(addr), f)
	}
}

func TestEmail_LocalPartCaseIsKept(t *testing.T) {
	// Documented, not changed: the local part is returned as typed, so a caller
	// that needs identity compares lower-cased values itself.
	got, f := input.Email("User@Example.RU", 254)
	if f != nil || got != "User@example.ru" {
		t.Fatalf("got (%q, %v)", got, f)
	}
}

// SL-19: Body allowed bidi overrides that Line refuses, so a listing description
// could use an unclosed RLO to display a reversed phone number or link; and
// isBlank missed visible-blank runes such as U+2800, so a required name could
// be "filled" with braille blanks.
func TestBody_RefusesUnbalancedBidiControls(t *testing.T) {
	bad := map[string]string{
		"lone RLO":                     "call \u202E1234567",
		"lone LRO":                     "x\u202Dy",
		"lone LRE":                     "x\u202Ay",
		"lone RLE":                     "x\u202By",
		"stray PDF":                    "x\u202Cy",
		"lone RLI":                     "x\u2067y",
		"lone LRI":                     "x\u2066y",
		"lone FSI":                     "x\u2068y",
		"stray PDI":                    "x\u2069y",
		"RLO closed by the wrong kind": "x\u202Ey\u2069z",
		"two opens one close":          "x\u202Ey\u202Ez\u202Cw",
	}
	for name, v := range bad {
		t.Run(name, func(t *testing.T) {
			assertReason(t, "Body", bodyF(v), input.ReasonTextInvalidCharacters)
		})
	}
}

func TestBody_AcceptsBalancedBidiControls(t *testing.T) {
	ok := map[string]string{
		"embedding":        "ab \u202Bשלום\u202C cd",
		"override":         "ab \u202Dxyz\u202C cd",
		"isolate":          "ab \u2067שלום\u2069 cd",
		"nested":           "a \u2067b \u202Bc\u202C d\u2069 e",
		"direction marks":  "a\u200Eb\u200Fc",
		"no bidi controls": "just text",
	}
	for name, v := range ok {
		t.Run(name, func(t *testing.T) {
			assertAccepted(t, "Body", v, v, true)
		})
	}
}

func TestText_VisibleBlankLookalikesAreBlank(t *testing.T) {
	for name, v := range map[string]string{
		"braille blank":        "\u2800",
		"braille blanks":       "\u2800\u2800\u2800",
		"braille blank + sp":   " \u2800 ",
		"khmer inherent vowel": "\u17B4\u17B5",
		"halfwidth hangul":     "\uFFA0",
	} {
		t.Run(name, func(t *testing.T) {
			assertReason(t, "Line required", failOf(input.Line(v, 10, input.Required())), input.ReasonTextRequired)
			assertReason(t, "Body required", failOf(input.Body(v, 10, input.Required())), input.ReasonTextRequired)
		})
	}
	// A braille pattern with dots is visible text.
	assertAccepted(t, "Line", "⠁", "⠁", false)
}

// SL-20: ParsePage accepted any page up to MaxInt32, so Offset reached ~2e11:
// ?page=2000000000 made Postgres walk the whole filtered set for every request.
func TestParsePage_RefusesPagesBeyondTheDefaultBound(t *testing.T) {
	_, f := input.ParsePage("2000000000", "100", 20, 100)
	fe := f.AtPage("page", "limit")
	if fe == nil || fe.Field != "page" || fe.Reason != input.ReasonNumberOutOfRange {
		t.Fatalf("page=2e9 must be out of range, got %+v", fe)
	}
	p, f := input.ParsePage("10000", "100", 20, 100)
	if f != nil || p.Offset != 9999*100 {
		t.Fatalf("the bound itself is allowed: (%+v, %v)", p, f)
	}
	if _, f := input.ParsePage("10001", "100", 20, 100); f == nil {
		t.Fatal("one past the bound must be refused")
	}
}

func TestParsePageMax_TakesAMandatoryBound(t *testing.T) {
	p, f := input.ParsePageMax("500", "50", 20, 100, 500)
	if f != nil || p.Number != 500 || p.Offset != 499*50 {
		t.Fatalf("at the bound: (%+v, %v)", p, f)
	}
	_, f = input.ParsePageMax("501", "", 20, 100, 500)
	if fe := f.AtPage("page", "limit"); fe == nil || fe.Field != "page" || fe.Reason != input.ReasonNumberOutOfRange {
		t.Fatalf("past the bound: %+v", fe)
	}
	assertPanics(t, "maxPage 0", func() { _, _ = input.ParsePageMax("", "", 20, 100, 0) })
}
