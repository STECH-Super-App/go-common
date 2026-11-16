package input

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nyaruka/phonenumbers"
	"golang.org/x/net/idna"
	"golang.org/x/text/unicode/norm"
)

// Phone validates an international phone number and returns it in E.164
// ("+79991234567"). The value is trimmed as in Line/Body, must start with '+',
// and must pass libphonenumber's Parse + IsValidNumber for some region. Common
// formatting ("+7 (999) 123-45-67") is accepted and normalised away.
//
// Also refused, because E.164 cannot carry them and the stored value would
// silently differ from what was typed: invalid UTF-8, any control or format
// character inside the number, any letter (libphonenumber does not reject
// letters — it maps three or more onto their keypad digits and strips fewer),
// and an extension ("+7 999 123 45 67 ext. 5").
// Failure → COMMON_PHONE_INVALID. Stricter country rules (e.g. organisation's
// +7-only) stay in the calling domain, on top of the returned value.
func Phone(raw string) (string, *Failure) {
	if !utf8.ValidString(raw) {
		return "", fail(ReasonPhoneInvalid, nil)
	}
	v := trimEdges(raw)
	if !strings.HasPrefix(v, "+") || strings.ContainsFunc(v, isRejectedPhoneRune) {
		return "", fail(ReasonPhoneInvalid, nil)
	}
	num, err := phonenumbers.Parse(v, "")
	if err != nil || num.GetExtension() != "" || !phonenumbers.IsValidNumber(num) {
		return "", fail(ReasonPhoneInvalid, nil)
	}
	return phonenumbers.Format(num, phonenumbers.E164), nil
}

// Email validates an email address and returns its ONE canonical stored form.
// The value is trimmed as in Line/Body, must hold no whitespace, control or
// format character (so no zero-width or bidi character anywhere), is
// NFC-normalised, and must have exactly one '@' and:
//
//   - a local part of 1–64 bytes in the RFC 5322 dot-atom ASCII set — letters,
//     digits and !#$%&'*+-/=?^_`{|}~, with single dots only between atoms — so
//     a comma (a second SMTP recipient), angle brackets, quotes and every
//     non-ASCII letter (a Cyrillic «а» posing as a Latin one) are refused;
//   - a domain of at least two dot-separated labels, at most 253 bytes in all,
//     whose last label is not all digits (so a@127.0.0.1 is not an address);
//     each label is ASCII — 1–63 bytes of [A-Za-z0-9-] without a leading or
//     trailing hyphen — or Cyrillic — Cyrillic letters, ASCII digits and
//     hyphens with at least one letter, no ASCII letter and no other script, a
//     valid IDNA label (golang.org/x/net/idna, Registration profile) whose
//     A-label is at most 63 bytes. A label starting "xn--" is decoded and judged
//     as the label it stands for; one that is not the A-label of a Cyrillic
//     label is refused;
//   - at most 254 bytes in all, measured on the stored form.
//
// So "ivan@почта.рф" is accepted; "иван@почта.рф" (Cyrillic local part),
// "ivan@mаil.ru" (a Cyrillic «а» inside a Latin label), a Greek, Armenian, Han
// or accented-Latin domain, "ivan@почта.рф." and "ivan@почта" are not. A
// Cyrillic label under an ASCII TLD ("ivan@почта.ru") is accepted: every label
// is single-script, and the DNS allows it.
//
// STORED FORM. The local part is returned unchanged — CASE IS KEPT, so
// User@x.ru and user@x.ru are two values; a caller that needs identity compares
// lower-cased values itself — and the domain lower-cased with every Cyrillic
// label in its A-label (punycode) form: "ivan@почта.рф", "Ivan@ПОЧТА.РФ"'s
// domain and "ivan@XN--80A1ACNY.XN--P1AI" all store the domain
// "xn--80a1acny.xn--p1ai". The stored value is always ASCII, so uniqueness is a
// byte comparison and every SMTP library and provider takes it as is; show it
// to a person with EmailDisplay. No DNS lookup. maxRunes caps the STORED form.
//
// Failure → COMMON_EMAIL_INVALID, or COMMON_TEXT_TOO_LONG (params {max}) for an
// over-long but well-formed address. maxRunes <= 0 panics.
func Email(raw string, maxRunes int) (string, *Failure) {
	if maxRunes <= 0 {
		panic("input: Email max must be positive")
	}
	if !utf8.ValidString(raw) {
		return "", fail(ReasonEmailInvalid, nil)
	}
	v := trimEdges(raw)
	if strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || isControlOrFormat(r) }) {
		return "", fail(ReasonEmailInvalid, nil)
	}
	v = norm.NFC.String(v)

	local, domain, ok := strings.Cut(v, "@")
	if !ok || strings.Contains(domain, "@") || !validEmailLocal(local) {
		return "", fail(ReasonEmailInvalid, nil)
	}
	stored, ok := canonicalEmailDomain(domain)
	if !ok {
		return "", fail(ReasonEmailInvalid, nil)
	}
	addr := local + "@" + stored
	if len(addr) > maxEmailBytes {
		return "", fail(ReasonEmailInvalid, nil)
	}

	if utf8.RuneCountInString(addr) > maxRunes {
		return "", fail(ReasonTextTooLong, map[string]any{"max": maxRunes})
	}
	return addr, nil
}

// EmailDisplay turns a stored address (Email's output) into the form to show to
// a person: the domain's A-labels back in Unicode, so the user who typed
// "ivan@почта.рф" sees that and not "ivan@xn--80a1acny.xn--p1ai". The result is
// for display only — never store it, compare it or send mail to it; pass it
// through Email first.
//
// Only a Cyrillic label is ever shown in Unicode. A domain holding an "xn--"
// label that is not one — garbage, or the A-label of a Latin, Greek or any other
// script label, which an attacker could pick to look like another domain — is
// returned exactly as stored, A-labels visible. A value without '@' is returned
// unchanged.
func EmailDisplay(stored string) string {
	local, domain, ok := strings.Cut(stored, "@")
	if !ok {
		return stored
	}
	labels := strings.Split(domain, ".")
	for i, label := range labels {
		if !strings.HasPrefix(strings.ToLower(label), acePrefix) {
			continue
		}
		u, err := idna.Registration.ToUnicode(strings.ToLower(label))
		if err != nil || isASCII(u) || !isCyrillicLabel(u) {
			return stored
		}
		labels[i] = u
	}
	return local + "@" + strings.Join(labels, ".")
}

// Email size limits (RFC 5321 §4.5.3.1). They apply to the stored form, whose
// Cyrillic labels are A-labels.
const (
	maxEmailBytes      = 254
	maxEmailLocalBytes = 64
	maxEmailDomainLen  = 253
	maxEmailLabelBytes = 63

	acePrefix = "xn--" // IDNA ACE prefix of an A-label
)

// validEmailLocal accepts an RFC 5322 dot-atom: atext runs separated by single
// dots, no leading or trailing dot.
func validEmailLocal(local string) bool {
	if local == "" || len(local) > maxEmailLocalBytes ||
		local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, "..") {
		return false
	}
	for i := 0; i < len(local); i++ {
		if !isAtext(local[i]) && local[i] != '.' {
			return false
		}
	}
	return true
}

func isAtext(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0
}

// canonicalEmailDomain judges every label and returns the domain in stored form.
func canonicalEmailDomain(domain string) (string, bool) {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", false
	}
	for i, label := range labels {
		stored, ok := canonicalEmailLabel(label)
		if !ok {
			return "", false
		}
		labels[i] = stored
	}
	stored := strings.Join(labels, ".")
	if len(stored) > maxEmailDomainLen || isAllDigits(labels[len(labels)-1]) {
		return "", false
	}
	return stored, true
}

// canonicalEmailLabel judges one label of the domain and returns its stored
// form: an ASCII label lower-cased, a Cyrillic label as its A-label.
func canonicalEmailLabel(label string) (string, bool) {
	if label == "" {
		return "", false
	}
	if isASCII(label) {
		lower := strings.ToLower(label)
		if !validASCIILabel(lower) {
			return "", false
		}
		if !strings.HasPrefix(lower, acePrefix) {
			return lower, true
		}
		// An A-label: it must stand for a Cyrillic label, and be that label's
		// one canonical encoding.
		u, err := idna.Registration.ToUnicode(lower)
		if err != nil || isASCII(u) || !isCyrillicLabel(u) {
			return "", false
		}
		a, err := idna.Registration.ToASCII(u)
		return a, err == nil && a == lower
	}
	// A Unicode label. The script rule runs BEFORE case-folding: lower-casing
	// can turn a non-Cyrillic rune into an ASCII one («İ» into «i»), which would
	// let a foreign letter slip into the Latin alphabet unnoticed.
	if !isCyrillicLabel(label) {
		return "", false
	}
	a, err := idna.Registration.ToASCII(norm.NFC.String(strings.ToLower(label)))
	if err != nil || len(a) > maxEmailLabelBytes {
		return "", false
	}
	return a, true
}

// validASCIILabel is release 1's rule for an ASCII label: 1–63 bytes of
// [A-Za-z0-9-] without a leading or trailing hyphen.
func validASCIILabel(label string) bool {
	if label == "" || len(label) > maxEmailLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		if !isDomainByte(label[i]) {
			return false
		}
	}
	return true
}

// isCyrillicLabel reports a label of Cyrillic letters, ASCII digits and hyphens
// holding at least one letter. An ASCII letter, a letter of any other script, a
// combining mark and every other rune make it false — so a label is never a
// mix of alphabets, and the only non-ASCII alphabet admitted is Cyrillic.
func isCyrillicLabel(s string) bool {
	letters := 0
	for _, r := range s {
		switch {
		case r == '-' || r >= '0' && r <= '9':
		case unicode.Is(unicode.Cyrillic, r) && unicode.IsLetter(r):
			letters++
		default:
			return false
		}
	}
	return letters > 0
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func isControlOrFormat(r rune) bool {
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// isRejectedPhoneRune reports the runes Phone refuses before handing the value
// to libphonenumber. Letters are among them because libphonenumber's normalize
// mutates them instead of rejecting them: three or more letters are mapped onto
// their keypad digits ("+7916ABC4567" -> "+79162224567") and fewer are stripped
// ("+79161234567A" -> "+79161234567"), either way storing a number other than
// the one typed.
func isRejectedPhoneRune(r rune) bool {
	return isControlOrFormat(r) || unicode.IsLetter(r)
}

func isDomainByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}
