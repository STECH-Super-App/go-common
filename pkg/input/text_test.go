package input_test

import (
	"strings"
	"testing"
	"unicode"

	"github.com/STECH-Super-App/go-common/pkg/input"
)

// The vectors below are a port of sale-service
// tests/Unit/Support/Validation/ControlCharactersTest.php. They prove the Go
// character rule is the same rule as ControlCharacters::found — Line is the
// single-line shape (allowContentWhitespace=false), Body the multi-line one.
// Every PHP vector places the character in the INTERIOR, where Line/Body's
// edge trim does not reach, so the comparison is like for like.

func TestControlCharacters_RefusedEverywhere(t *testing.T) {
	cases := map[string]string{
		"interior NUL":    "HEAD\x00TAIL",
		"BEL":             "a\x07b",
		"ESC":             "a\x1bb",
		"DEL (U+007F)":    "a\x7fb",
		"SOH (U+0001)":    "a\x01b",
		"US (U+001F)":     "a\x1fb",
		"C1 NEL (U+0085)": "a\u0085b",
		"C1 CSI (U+009B)": "a\u009bb",
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			assertReason(t, "Line", lineF(v), input.ReasonTextInvalidCharacters)
			assertReason(t, "Body", bodyF(v), input.ReasonTextInvalidCharacters)
		})
	}
}

func TestControlCharacters_OnlySingleLineRefusesContentWhitespace(t *testing.T) {
	cases := map[string]string{
		"LF":   "line one\nline two",
		"CR":   "line one\rline two",
		"TAB":  "column\tcolumn",
		"CRLF": "line one\r\nline two",
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			assertReason(t, "Line", lineF(v), input.ReasonTextInvalidCharacters)
			assertAccepted(t, "Body", v, v, true)
		})
	}
}

func TestControlCharacters_SingleLineRefusesBidiAndSeparators(t *testing.T) {
	cases := map[string]rune{
		"U+200E LEFT-TO-RIGHT MARK":      0x200E,
		"U+200F RIGHT-TO-LEFT MARK":      0x200F,
		"U+202A LEFT-TO-RIGHT EMBEDDING": 0x202A,
		"U+202E RIGHT-TO-LEFT OVERRIDE":  0x202E,
		"U+2066 LEFT-TO-RIGHT ISOLATE":   0x2066,
		"U+2069 POP DIRECTIONAL ISOLATE": 0x2069,
		"U+2028 LINE SEPARATOR":          0x2028,
		"U+2029 PARAGRAPH SEPARATOR":     0x2029,
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			v := "Мой фильтр" + string(r) + "2026"
			assertReason(t, "Line", lineF(v), input.ReasonTextInvalidCharacters)
			assertAccepted(t, "Body", v, v, true)
		})
	}
}

func TestControlCharacters_LegitimateTextUntouched(t *testing.T) {
	cases := map[string]string{
		"cyrillic":               "Экскаватор гусеничный CAT 320",
		"latin with punctuation": "JCB 3CX — «Сити», 2019 г.",
		"digits and separators":  "22,5 т / 1 500 000 ₽",
		"a lone emoji":           "Срочно 🔥",
		"emoji ZWJ sequence":     "\U0001F469\u200D\U0001F4BB",
		"family ZWJ sequence":    "\U0001F468\u200D\U0001F469\u200D\U0001F467",
		"a bare ZWJ":             "a\u200Db",
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			assertAccepted(t, "Line", v, v, false)
			assertAccepted(t, "Body", v, v, true)
		})
	}
}

func TestControlCharacters_MalformedUTF8RefusedByBothShapes(t *testing.T) {
	v := "\xC3\x28"
	assertReason(t, "Line", lineF(v), input.ReasonTextInvalidCharacters)
	assertReason(t, "Body", bodyF(v), input.ReasonTextInvalidCharacters)
}

// Exhaustive pin of the character rule over every code point: an interior
// rune is refused by Line iff it is Cc or in SINGLE_LINE_DENIED, and by Body
// iff it is Cc other than TAB/LF/CR. Anything wider (e.g. matching all of Cf)
// or narrower fails here.
func TestCharacterRule_ExhaustiveOverAllCodePoints(t *testing.T) {
	denied := map[rune]bool{0x200E: true, 0x200F: true, 0x2028: true, 0x2029: true}
	for r := rune(0x202A); r <= 0x202E; r++ {
		denied[r] = true
	}
	for r := rune(0x2066); r <= 0x2069; r++ {
		denied[r] = true
	}

	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogates are not encodable in UTF-8
		}
		v := "a" + string(r) + "b"
		isCc := unicode.Is(unicode.Cc, r)

		wantLine := isCc || denied[r]
		if _, f := input.Line(v, 10); (f != nil) != wantLine {
			t.Fatalf("Line U+%04X: refused=%v, want %v", r, f != nil, wantLine)
		}
		wantBody := isCc && r != '\t' && r != '\n' && r != '\r'
		if _, f := input.Body(v, 10); (f != nil) != wantBody {
			t.Fatalf("Body U+%04X: refused=%v, want %v", r, f != nil, wantBody)
		}
	}
}

func TestText_TrimsEdges(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"ascii spaces", "  hello  ", "hello"},
		{"edge NUL and controls", "\x00\x01hello\x7f\x00", "hello"},
		{"edge newlines", "\n\r\thello\n", "hello"},
		{"edge NBSP and ideographic space", "\u00A0\u3000hello\u3000", "hello"},
		{"edge BOM, ZWSP, ZWJ", "\uFEFF\u200Bhello\u200D", "hello"},
		{"edge bidi override", "\u202Ehello\u202C", "hello"},
		{"edge line separator", "\u2028hello\u2029", "hello"},
		{"interior space kept", " a b ", "a b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertAccepted(t, "Line", tc.raw, tc.want, false)
			assertAccepted(t, "Body", tc.raw, tc.want, true)
		})
	}
}

func TestText_Blank(t *testing.T) {
	blanks := map[string]string{
		"empty":                    "",
		"spaces":                   "   ",
		"NBSP only":                "\u00A0\u00A0",
		"ZWSP only":                "\u200B",
		"BOM only":                 "\uFEFF",
		"variation selector":       "\uFE0F",
		"variation selectors + sp": "\uFE0F \uFE0E",
		"hangul filler (ODI)":      "\u3164",
		"hangul choseong filler":   "\u115F\u1160",
	}
	for name, v := range blanks {
		t.Run(name, func(t *testing.T) {
			assertReason(t, "Line required", failOf(input.Line(v, 10, input.Required())), input.ReasonTextRequired)
			assertReason(t, "Body required", failOf(input.Body(v, 10, input.Required())), input.ReasonTextRequired)
			assertAccepted(t, "Line", v, "", false)
			assertAccepted(t, "Body", v, "", true)
		})
	}
}

func TestText_RequiredAcceptsVisibleValue(t *testing.T) {
	got, f := input.Line(" x ", 10, input.Required())
	if f != nil || got != "x" {
		t.Fatalf("got (%q, %v), want (\"x\", nil)", got, f)
	}
}

func TestText_LengthCountsRunesAfterTrim(t *testing.T) {
	// 5 Cyrillic letters are 10 bytes but 5 runes.
	if got, f := input.Line("  абвгд  ", 5); f != nil || got != "абвгд" {
		t.Fatalf("got (%q, %v), want (\"абвгд\", nil)", got, f)
	}
	f := failOf(input.Line("абвгде", 5))
	assertReason(t, "Line", f, input.ReasonTextTooLong)
	if f.Params["max"] != 5 {
		t.Fatalf("params: got %v, want max=5", f.Params)
	}
	assertReason(t, "Body", failOf(input.Body("a\nbcdef", 6)), input.ReasonTextTooLong)
}

func TestText_OrderInvalidCharactersBeforeRequiredAndLength(t *testing.T) {
	assertReason(t, "Line", failOf(input.Line(strings.Repeat("x", 50)+"\x00x", 5, input.Required())),
		input.ReasonTextInvalidCharacters)
}

func TestText_NonPositiveMaxPanics(t *testing.T) {
	for _, max := range []int{0, -1} {
		for name, fn := range map[string]func(string, int, ...input.TextOpt) (string, *input.Failure){
			"Line": input.Line, "Body": input.Body,
		} {
			func() {
				defer func() {
					if recover() == nil {
						t.Errorf("%s(max=%d) did not panic", name, max)
					}
				}()
				_, _ = fn("x", max)
			}()
		}
	}
}

// --- helpers ---------------------------------------------------------------

func lineF(v string) *input.Failure { return failOf(input.Line(v, 1000)) }
func bodyF(v string) *input.Failure { return failOf(input.Body(v, 1000)) }

func failOf(_ string, f *input.Failure) *input.Failure { return f }

func assertReason(t *testing.T, label string, f *input.Failure, want string) {
	t.Helper()
	if f == nil {
		t.Fatalf("%s: accepted, want %s", label, want)
	}
	if f.Reason != want {
		t.Fatalf("%s: reason %s, want %s", label, f.Reason, want)
	}
}

func assertAccepted(t *testing.T, label, raw, want string, multiLine bool) {
	t.Helper()
	var got string
	var f *input.Failure
	if multiLine {
		got, f = input.Body(raw, 1000)
	} else {
		got, f = input.Line(raw, 1000)
	}
	if f != nil {
		t.Fatalf("%s(%q): refused with %s, want accepted", label, raw, f.Reason)
	}
	if got != want {
		t.Fatalf("%s(%q): got %q, want %q", label, raw, got, want)
	}
}
