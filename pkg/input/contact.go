package input

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nyaruka/phonenumbers"
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

// Email validates an email address of at most maxRunes runes. The value is trimmed
// as in Line/Body, then must hold no whitespace, control or format character,
// exactly one '@', and:
//
//   - a local part of 1–64 bytes in the RFC 5322 dot-atom ASCII set — letters,
//     digits and !#$%&'*+-/=?^_`{|}~, with single dots only between atoms — so
//     a comma (a second SMTP recipient), angle brackets, quotes and every
//     non-ASCII letter (a Cyrillic «а» posing as a Latin one) are refused;
//   - a domain of at least two dot-separated labels, each 1–63 bytes of
//     [A-Za-z0-9-] without a leading or trailing hyphen, at most 253 bytes in
//     all, whose last label is not all digits (so a@127.0.0.1 is not an address);
//   - at most 254 bytes in all.
//
// The local part is returned unchanged — CASE IS KEPT, so User@x.ru and
// user@x.ru are two values; a caller that needs identity compares lower-cased
// values itself — and the domain lower-cased. No DNS lookup, no IDN. Failure →
// COMMON_EMAIL_INVALID, or COMMON_TEXT_TOO_LONG (params {max}) for an over-long
// but well-formed address. maxRunes <= 0 panics.
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

	local, domain, ok := strings.Cut(v, "@")
	if !ok || strings.Contains(domain, "@") || !validEmailLocal(local) || !validEmailDomain(domain) ||
		len(v) > maxEmailBytes {
		return "", fail(ReasonEmailInvalid, nil)
	}

	if utf8.RuneCountInString(v) > maxRunes {
		return "", fail(ReasonTextTooLong, map[string]any{"max": maxRunes})
	}
	return local + "@" + strings.ToLower(domain), nil
}

// Email size limits (RFC 5321 §4.5.3.1).
const (
	maxEmailBytes      = 254
	maxEmailLocalBytes = 64
	maxEmailDomainLen  = 253
	maxEmailLabelBytes = 63
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

func validEmailDomain(domain string) bool {
	if len(domain) > maxEmailDomainLen {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 || isAllDigits(labels[len(labels)-1]) {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > maxEmailLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !isDomainByte(c) {
				return false
			}
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
