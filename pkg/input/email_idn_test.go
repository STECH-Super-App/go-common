package input_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/STECH-Super-App/go-common/pkg/input"
)

// Rule «B» (owner decision 14.11.2026, SL-19): the local part is ASCII only; the
// domain is ASCII or a Cyrillic internationalised domain; every label of a domain
// is single-script; the stored form is the A-label (punycode) form.

const (
	pochtaRF       = "ivan@почта.рф"
	pochtaRFStored = "ivan@xn--80a1acny.xn--p1ai"
)

func TestEmail_RuleB_Accepted(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"ascii", "ivan@mail.ru", "ivan@mail.ru"},
		{"cyrillic domain", pochtaRF, pochtaRFStored},
		{"its punycode twin is the same stored value", pochtaRFStored, pochtaRFStored},
		{"upper-case punycode", "ivan@XN--80A1ACNY.XN--P1AI", pochtaRFStored},
		{"upper-case Cyrillic", "ivan@ПОЧТА.РФ", pochtaRFStored},
		{"local part keeps its case", "Ivan@ПОЧТА.РФ", "Ivan@xn--80a1acny.xn--p1ai"},
		{"surrounding whitespace is trimmed", "  " + pochtaRF + "\n", pochtaRFStored},
		{"cyrillic label under an ascii TLD: every label is single-script", "ivan@почта.ru", "ivan@xn--80a1acny.ru"},
		{"latin label under a cyrillic TLD", "ivan@mail.рф", "ivan@mail.xn--p1ai"},
		{"digits and hyphen beside Cyrillic letters", "ivan@по-чта1.рф", "ivan@xn---1-7kc8ceu8a.xn--p1ai"},
		{"digits after Cyrillic letters", "ivan@почта1.рф", "ivan@xn--1-7sb5bdq3a.xn--p1ai"},
		{"digits-only label is ascii", "ivan@123.рф", "ivan@123.xn--p1ai"},
		{"ascii label with letters and digits next to a Cyrillic one", "ivan@a1.почта.рф", "ivan@a1.xn--80a1acny.xn--p1ai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, f := input.Email(tc.raw, 254)
			if f != nil || got != tc.want {
				t.Fatalf("Email(%q) = (%q, %v), want %q", tc.raw, got, f, tc.want)
			}
			// The stored form is a fixed point.
			again, f := input.Email(got, 254)
			if f != nil || again != got {
				t.Fatalf("not idempotent: %q -> (%q, %v)", got, again, f)
			}
		})
	}
}

func TestEmail_RuleB_DecomposedAndComposedAreOneMailbox(t *testing.T) {
	// «й» as one rune and as «и» + U+0306 COMBINING BREVE: NFC makes them one.
	composed, f1 := input.Email("ivan@пой.рф", 254)
	decomposed, f2 := input.Email("ivan@пои\u0306.рф", 254)
	if f1 != nil || f2 != nil || composed != decomposed {
		t.Fatalf("composed (%q, %v) and decomposed (%q, %v) must store the same value", composed, f1, decomposed, f2)
	}
}

func TestEmail_RuleB_Refused(t *testing.T) {
	const (
		cyrA = "а" // а
		cyrE = "е" // е
		cyrO = "о" // о
		cyrR = "р" // р
		cyrS = "с" // с
		cyrX = "х" // х
	)
	cases := []struct{ name, raw string }{
		{"cyrillic local part", "иван@почта.рф"},
		{"cyrillic local part, ascii domain", "иван@mail.ru"},
		{"one Cyrillic letter in the local part", "iv" + cyrA + "n@mail.ru"},
		{"one Cyrillic letter inside a Latin label", "ivan@m" + cyrA + "il.ru"},
		{"one Latin letter inside a Cyrillic label", "ivan@почtа.рф"},
		{"Latin letter in a Cyrillic TLD", "ivan@почта.рf"},
		{"Cyrillic letter in a Latin TLD", "ivan@mail.r\u0443"},
		{"greek", "ivan@μail.ru"},
		{"greek label", "ivan@παράδειγμα.gr"},
		{"armenian", "ivan@օրինակ.հայ"},
		{"han", "ivan@例え.jp"},
		{"latin with a diacritic", "ivan@münchen.de"},
		{"turkish dotted capital I, which lower-cases to a plain i", "ivan@mİl.ru"},
		{"punycode of a Latin non-ASCII label", "ivan@xn--mnchen-3ya.de"},
		{"punycode of a Greek label", "ivan@xn--hxajbheg2az3al.xn--qxam"},
		{"punycode garbage", "ivan@xn--garbage.ru"},
		{"punycode, nothing after the prefix", "ivan@xn--.ru"},
		{"punycode that decodes to ASCII", "ivan@xn--a-.ru"},
		{"punycode with an invalid digit", "ivan@xn--80a1ac!ny.ru"},
		{"leading hyphen, Cyrillic", "ivan@-почта.рф"},
		{"trailing hyphen, Cyrillic", "ivan@почта-.рф"},
		{"empty label in the middle", "ivan@почта..рф"},
		{"empty first label", "ivan@.почта.рф"},
		{"trailing dot, Cyrillic", "ivan@почта.рф."},
		{"trailing dot, ascii", "ivan@mail.ru."},
		{"no dot, Cyrillic", "ivan@почта"},
		{"no dot, ascii", "ivan@localhost"},
		{"underscore in a Cyrillic label", "ivan@по_чта.рф"},
		{"space in the domain", "ivan@почта .рф"},
		{"Arabic-Indic digit in a Cyrillic label", "ivan@почта٣.рф"},
		{"numeric TLD", "ivan@почта.123"},
		{"zero-width space in the domain", "ivan@почта\u200b.рф"},
		{"zero-width joiner in the domain", "ivan@почта\u200d.рф"},
		{"zero-width non-joiner in the domain", "ivan@почта\u200c.рф"},
		{"soft hyphen in the domain", "ivan@по\u00adчта.рф"},
		{"right-to-left override in the domain", "ivan@\u202eпочта.рф"},
		{"right-to-left override before the TLD", "ivan@почта.\u202eрф"},
		{"left-to-right mark inside the domain", "ivan@почта\u200e.рф"},
		{"bidi isolate in the local part", "iv\u2066an@mail.ru"},
		{"zero-width space in the local part", "iv\u200ban@mail.ru"},
		{"zero-width joiner in the local part", "iv\u200dan@mail.ru"},
		{"BOM inside the local part", "iv\ufeffan@mail.ru"},
		{"combining mark on a Latin letter", "iva\u0301n@mail.ru"},
		{"a bare combining mark starting a label", "ivan@\u0301почта.рф"},
		{"two at signs", "ivan@почта.рф@mail.ru"},
		{"invalid UTF-8", "ivan@\xffпочта.рф"},
	}
	// The six Cyrillic look-alikes (а е о р с х) inside Latin labels, at every
	// position of two real-looking domains, and the reverse inside Cyrillic ones.
	for _, base := range []string{"example", "paypal", "scope", "oxford"} {
		for i, r := range base {
			sub := map[rune]string{'a': cyrA, 'e': cyrE, 'o': cyrO, 'p': cyrR, 'c': cyrS, 'x': cyrX}[r]
			if sub == "" {
				continue
			}
			spoof := base[:i] + sub + base[i+len(string(r)):]
			cases = append(cases, struct{ name, raw string }{
				fmt.Sprintf("Cyrillic look-alike at %d of %s", i, base), "ivan@" + spoof + ".com"})
		}
	}
	for _, lat := range []struct{ latin, cyr string }{{"a", cyrA}, {"e", cyrE}, {"o", cyrO}, {"p", cyrR}, {"c", cyrS}, {"x", cyrX}} {
		cases = append(cases, struct{ name, raw string }{
			"Latin " + lat.latin + " inside a Cyrillic label", "ivan@по" + lat.latin + "чта.рф"})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, f := input.Email(tc.raw, 1000)
			if f == nil || f.Reason != input.ReasonEmailInvalid {
				t.Fatalf("Email(%q) = (%q, %v), want %s", tc.raw, got, f, input.ReasonEmailInvalid)
			}
		})
	}
}

func TestEmail_RuleB_LengthCapsAreMeasuredOnTheStoredForm(t *testing.T) {
	// 43 Cyrillic letters: far under the 63-rune label limit as typed, over the
	// 63-octet limit once encoded as an A-label.
	label := "абвгдежзийклмнопрстуфхцчшщъыьэюя" + "ёіїєґәөүқңһ"
	if utf8.RuneCountInString(label) > 63 {
		t.Fatal("test premise: the label is within 63 runes")
	}
	if got, f := input.Email("ivan@"+label+".рф", 1000); f == nil || f.Reason != input.ReasonEmailInvalid {
		t.Errorf("an A-label over 63 octets must be refused, got (%q, %v)", got, f)
	}

	// The 254-byte total is the stored form's. "почта." is 11 bytes typed and 13
	// stored, so a run of them is under 254 as typed and over as stored.
	raw := strings.Repeat("a", 64) + "@" + strings.Repeat("почта.", 16) + "рф"
	if len(raw) >= 254 {
		t.Fatalf("test premise: typed form is %d bytes", len(raw))
	}
	if got, f := input.Email(raw, 1000); f == nil || f.Reason != input.ReasonEmailInvalid {
		t.Errorf("a stored form over 254 bytes must be refused, got (%q, %v)", got, f)
	}

	// maxRunes caps the stored value, not the typed one.
	if got, f := input.Email(pochtaRF, 26); f != nil || got != pochtaRFStored {
		t.Errorf("26 = len(stored): got (%q, %v)", got, f)
	}
	f := failOfEmail(pochtaRF, 25)
	if f == nil || f.Reason != input.ReasonTextTooLong || f.Params["max"] != 25 {
		t.Errorf("stored form is 26 runes, typed 13: want COMMON_TEXT_TOO_LONG, got %+v", f)
	}
}

func failOfEmail(raw string, limit int) *input.Failure {
	_, f := input.Email(raw, limit)
	return f
}

func TestEmailDisplay(t *testing.T) {
	cases := []struct{ name, stored, want string }{
		{"A-label back to Unicode", pochtaRFStored, pochtaRF},
		{"ascii is unchanged", "ivan@mail.ru", "ivan@mail.ru"},
		{"mixed labels", "ivan@mail.xn--p1ai", "ivan@mail.рф"},
		{"local part case is kept", "Ivan@xn--80a1acny.ru", "Ivan@почта.ru"},
		{"garbage punycode stays as stored", "ivan@xn--garbage.ru", "ivan@xn--garbage.ru"},
		{"a Latin-script IDN is never shown as Unicode", "ivan@xn--mnchen-3ya.de", "ivan@xn--mnchen-3ya.de"},
		{"a Greek IDN is never shown as Unicode", "ivan@xn--hxajbheg2az3al.xn--qxam", "ivan@xn--hxajbheg2az3al.xn--qxam"},
		{"no at sign: returned as is", "plain", "plain"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := input.EmailDisplay(tc.stored); got != tc.want {
				t.Fatalf("EmailDisplay(%q) = %q, want %q", tc.stored, got, tc.want)
			}
		})
	}
	// What a person sees can be typed back: display -> Email -> the stored value.
	got, f := input.Email(input.EmailDisplay(pochtaRFStored), 254)
	if f != nil || got != pochtaRFStored {
		t.Fatalf("round trip: (%q, %v)", got, f)
	}
}

// Every address release 1 accepted is still accepted and normalises to the
// same value. The oracle is release 1's own code, copied below, run over a fixed
// list and a seeded pseudo-random corpus. The single deliberate exception is a
// label that begins "xn--": it is now decoded, so "xn--" garbage is refused
// (see TestEmail_RuleB_PunycodeLookalikeOfAsciiIsTheOnlyNarrowing).
func TestEmail_RuleB_NothingReleaseOneAcceptedChanges(t *testing.T) {
	fixed := []string{
		"user@example.com", "  User.Name@Example.COM  ", "a+tag@sub-domain.example.ru",
		"a.b.c@example.com", "o'brien@example.com", "a_b-c@example.com",
		"!#$%&'*+-/=?^_`{|}~@example.com", strings.Repeat("a", 64) + "@x.ru",
		"a@" + strings.Repeat("b", 63) + ".ru", "User@Example.RU", "a@ab--c.com", "a@a-b.c-d.ee", "a@1a.ru",
		"a@x.r2", "A@B.CO",
	}
	r := rand.New(rand.NewPCG(14, 11)) //nolint:gosec // a seeded corpus generator, not security
	const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	pick := func(set string, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = set[r.IntN(len(set))]
		}
		return string(b)
	}
	label := func() string {
		l := pick(alnum, 1+r.IntN(12))
		if len(l) > 2 && r.IntN(3) == 0 {
			k := 1 + r.IntN(len(l)-2)
			l = l[:k] + "-" + l[k+1:]
		}
		return l
	}
	for i := 0; i < 4000; i++ {
		local := pick(alnum+"._+-'", 1+r.IntN(20))
		domain := label()
		for j := 0; j < 1+r.IntN(3); j++ {
			domain += "." + label()
		}
		fixed = append(fixed, local+"@"+domain)
	}

	var accepted int
	for _, raw := range fixed {
		want, wantOK := legacyEmail(raw, 1000)
		if hasACEPrefix(raw) {
			continue
		}
		got, f := input.Email(raw, 1000)
		switch {
		case wantOK && (f != nil || got != want):
			t.Fatalf("release 1 accepted %q as %q; now (%q, %v)", raw, want, got, f)
		case !wantOK && f == nil:
			t.Fatalf("release 1 refused %q; now accepted as %q (ASCII input must not loosen)", raw, got)
		}
		if wantOK {
			accepted++
		}
	}
	if accepted < 1000 {
		t.Fatalf("corpus too thin: only %d accepted addresses", accepted)
	}
}

func hasACEPrefix(raw string) bool {
	_, domain, _ := strings.Cut(strings.ToLower(raw), "@")
	for _, l := range strings.Split(domain, ".") {
		if strings.HasPrefix(l, "xn--") {
			return true
		}
	}
	return false
}

func TestEmail_RuleB_PunycodeLookalikeOfAsciiIsTheOnlyNarrowing(t *testing.T) {
	// Release 1 stored a@xn--garbage.com as typed. The label is not a punycode
	// A-label of anything the rule accepts, so it is refused now; no real address
	// is affected (an A-label is only ever produced from a Unicode one).
	if _, ok := legacyEmail("a@xn--garbage.com", 1000); !ok {
		t.Fatal("test premise: release 1 accepted it")
	}
	if _, f := input.Email("a@xn--garbage.com", 1000); f == nil {
		t.Fatal("a@xn--garbage.com must be refused now")
	}
}

func TestEmail_RuleB_OutputIsAlwaysASCII(t *testing.T) {
	for _, raw := range []string{pochtaRF, pochtaRFStored, "ivan@ПОЧТА.РФ", "ivan@mail.ru", "ivan@почта.ru"} {
		got, f := input.Email(raw, 254)
		if f != nil {
			t.Fatalf("Email(%q): %v", raw, f)
		}
		if utf8.RuneCountInString(got) != len(got) {
			t.Fatalf("Email(%q) = %q is not ASCII", raw, got)
		}
	}
}

// --- release 1's Email, verbatim, as the oracle ---

func legacyEmail(raw string, maxRunes int) (string, bool) {
	v := strings.Trim(raw, " \t\r\n")
	local, domain, ok := strings.Cut(v, "@")
	if !ok || strings.Contains(domain, "@") || !legacyLocal(local) || !legacyDomain(domain) || len(v) > 254 {
		return "", false
	}
	if utf8.RuneCountInString(v) > maxRunes {
		return "", false
	}
	return local + "@" + strings.ToLower(domain), true
}

func legacyLocal(local string) bool {
	if local == "" || len(local) > 64 || local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	for i := 0; i < len(local); i++ {
		c := local[i]
		atext := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0
		if !atext && c != '.' {
			return false
		}
	}
	return true
}

func legacyDomain(domain string) bool {
	if len(domain) > 253 {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	last := labels[len(labels)-1]
	digits := last != ""
	for i := 0; i < len(last); i++ {
		if last[i] < '0' || last[i] > '9' {
			digits = false
		}
	}
	if digits {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}
