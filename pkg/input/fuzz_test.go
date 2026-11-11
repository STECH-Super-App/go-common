package input_test

import (
	"math"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/STECH-Super-App/go-common/pkg/input"
)

func addTextSeeds(f *testing.F) {
	for _, s := range []string{
		"", " ", "hello", "  hi  ", "a\x00b", "\x00", "line\nline", "\t\r\n",
		"\u202Eevil", "a\u2028b", "\U0001F469\u200D\U0001F4BB", "\uFEFF\u200B",
		"\uFE0F", "\u3164", "\xc3\x28", "абвгдеж", strings.Repeat("я", 40),
	} {
		f.Add(s, 8)
	}
}

func checkTextInvariants(t *testing.T, label, raw, out string, f *input.Failure, maxRunes int, multiLine bool) {
	if f != nil {
		if out != "" {
			t.Fatalf("%s(%q): failure %s with non-empty output %q", label, raw, f.Reason, out)
		}
		return
	}
	if !utf8.ValidString(out) {
		t.Fatalf("%s(%q): output not valid UTF-8: %q", label, raw, out)
	}
	if n := utf8.RuneCountInString(out); n > maxRunes {
		t.Fatalf("%s(%q): output %d runes > maxRunes %d", label, raw, n, maxRunes)
	}
	for _, r := range out {
		contentWhitespace := multiLine && (r == '\t' || r == '\n' || r == '\r')
		if unicode.Is(unicode.Cc, r) && !contentWhitespace {
			t.Fatalf("%s(%q): output contains forbidden control U+%04X", label, raw, r)
		}
	}
	if out == "" {
		return
	}
	first, _ := utf8.DecodeRuneInString(out)
	last, _ := utf8.DecodeLastRuneInString(out)
	for _, r := range []rune{first, last} {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("%s(%q): output has edge whitespace/Cc/Cf U+%04X: %q", label, raw, r, out)
		}
	}
	// The cleaned value is a fixed point: validating it again changes nothing.
	var again string
	var f2 *input.Failure
	if multiLine {
		again, f2 = input.Body(out, maxRunes)
	} else {
		again, f2 = input.Line(out, maxRunes)
	}
	if f2 != nil || again != out {
		t.Fatalf("%s(%q): not idempotent: %q -> (%q, %v)", label, raw, out, again, f2)
	}
}

func clampMax(maxRunes int) int {
	if maxRunes < 0 {
		maxRunes = -maxRunes
	}
	return maxRunes%64 + 1
}

func FuzzLine(f *testing.F) {
	addTextSeeds(f)
	f.Fuzz(func(t *testing.T, raw string, maxRunes int) {
		maxRunes = clampMax(maxRunes)
		out, fail := input.Line(raw, maxRunes)
		checkTextInvariants(t, "Line", raw, out, fail, maxRunes, false)
		for _, r := range out {
			if r == '\t' || r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029 || r == 0x202E {
				t.Fatalf("Line(%q): single-line output holds U+%04X", raw, r)
			}
		}
	})
}

func FuzzBody(f *testing.F) {
	addTextSeeds(f)
	f.Fuzz(func(t *testing.T, raw string, maxRunes int) {
		maxRunes = clampMax(maxRunes)
		out, fail := input.Body(raw, maxRunes)
		checkTextInvariants(t, "Body", raw, out, fail, maxRunes, true)
	})
}

var canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func FuzzUUID(f *testing.F) {
	for _, s := range []string{
		"550e8400-e29b-41d4-a716-446655440000", "550E8400-E29B-41D4-A716-446655440000",
		"{550e8400-e29b-41d4-a716-446655440000}", "550e8400e29b41d4a716446655440000", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		out, fail := input.UUID(raw)
		if fail != nil {
			return
		}
		if !canonicalUUID.MatchString(out) {
			t.Fatalf("UUID(%q) = %q, not lower-case canonical", raw, out)
		}
		if !strings.EqualFold(out, raw) {
			t.Fatalf("UUID(%q) = %q changed more than case", raw, out)
		}
	})
}

func FuzzParsePage(f *testing.F) {
	for _, s := range [][2]string{{"", ""}, {"1", "20"}, {"2147483647", "100"}, {"-1", "0"}, {"0", "-5"}, {"+1", "1e2"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, number, size string) {
		p, fail := input.ParsePage(number, size, 20, 100)
		if fail != nil {
			return
		}
		if p.Offset < 0 || p.Number < 1 || p.Size < 1 || p.Size > 100 {
			t.Fatalf("ParsePage(%q,%q) = %+v", number, size, p)
		}
		if p.Offset != int64(p.Number-1)*int64(p.Size) {
			t.Fatalf("ParsePage(%q,%q): offset mismatch %+v", number, size, p)
		}
	})
}

func FuzzEmail(f *testing.F) {
	for _, s := range []string{"user@example.com", " A@B.CO ", "a@b@c.d", "x@-y.z", "\x00@a.b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		out, fail := input.Email(raw, 254)
		if fail != nil {
			return
		}
		if !utf8.ValidString(out) || strings.Count(out, "@") != 1 || utf8.RuneCountInString(out) > 254 {
			t.Fatalf("Email(%q) = %q", raw, out)
		}
		if strings.ContainsFunc(out, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
		}) {
			t.Fatalf("Email(%q) = %q holds space/control/format", raw, out)
		}
		if again, f2 := input.Email(out, 254); f2 != nil || again != out {
			t.Fatalf("Email(%q): not idempotent %q -> (%q, %v)", raw, out, again, f2)
		}
	})
}

func FuzzPhone(f *testing.F) {
	for _, s := range []string{"+79991234567", "+7 (999) 123-45-67", "89991234567", "+1 650 253 0000 ext 1", "+"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		out, fail := input.Phone(raw)
		if fail != nil {
			return
		}
		if !strings.HasPrefix(out, "+") || strings.TrimLeft(out[1:], "0123456789") != "" {
			t.Fatalf("Phone(%q) = %q, not E.164", raw, out)
		}
		if again, f2 := input.Phone(out); f2 != nil || again != out {
			t.Fatalf("Phone(%q): not idempotent %q -> (%q, %v)", raw, out, again, f2)
		}
	})
}

func FuzzCoordinates(f *testing.F) {
	for _, s := range [][2]float64{
		{0, 0}, {math.Copysign(0, -1), 0}, {55.7558, 37.6173}, {-90, 180}, {90, -180},
		{0, 37.6}, {55.7, 0}, {90.0000001, 0}, {math.NaN(), 1}, {1, math.Inf(-1)},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, lat, lon float64) {
		outLat, outLon, fail := input.Coordinates(lat, lon)
		// An independent statement of the three rules: the validator must
		// accept exactly the pairs it describes.
		valid := !math.IsNaN(lat) && !math.IsNaN(lon) &&
			lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 &&
			(lat != 0 || lon != 0)
		if fail != nil {
			if valid {
				t.Fatalf("Coordinates(%v, %v) refused a valid pair: %s", lat, lon, fail.Reason)
			}
			if outLat != 0 || outLon != 0 {
				t.Fatalf("Coordinates(%v, %v): failure %s with output (%v, %v)", lat, lon, fail.Reason, outLat, outLon)
			}
			return
		}
		if !valid {
			t.Fatalf("Coordinates(%v, %v) accepted an invalid pair", lat, lon)
		}
		if outLat != lat || outLon != lon || math.Signbit(outLat) && outLat == 0 || math.Signbit(outLon) && outLon == 0 {
			t.Fatalf("Coordinates(%v, %v) = (%v, %v): changed more than the sign of zero", lat, lon, outLat, outLon)
		}
		if againLat, againLon, f2 := input.Coordinates(outLat, outLon); f2 != nil || againLat != outLat || againLon != outLon {
			t.Fatalf("Coordinates(%v, %v): not idempotent (%v, %v) -> (%v, %v, %v)", lat, lon, outLat, outLon, againLat, againLon, f2)
		}
	})
}
