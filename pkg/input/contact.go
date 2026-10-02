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
// character inside the number, and an extension ("+7 999 123 45 67 ext. 5").
// Failure → COMMON_PHONE_INVALID. Stricter country rules (e.g. organisation's
// +7-only) stay in the calling domain, on top of the returned value.
func Phone(raw string) (string, *Failure) {
	if !utf8.ValidString(raw) {
		return "", fail(ReasonPhoneInvalid, nil)
	}
	v := trimEdges(raw)
	if !strings.HasPrefix(v, "+") || strings.ContainsFunc(v, isControlOrFormat) {
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
// exactly one '@', a non-empty local part, and a domain of at least two
// dot-separated labels, each made of [A-Za-z0-9-] without a leading or
// trailing hyphen. The local part is returned unchanged and the domain
// lower-cased. No DNS lookup, no IDN. Failure → COMMON_EMAIL_INVALID, or
// COMMON_TEXT_TOO_LONG (params {max}) for an over-long but well-formed address.
// maxRunes <= 0 panics.
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
	if !ok || local == "" || strings.Contains(domain, "@") || !validEmailDomain(domain) {
		return "", fail(ReasonEmailInvalid, nil)
	}

	if utf8.RuneCountInString(v) > maxRunes {
		return "", fail(ReasonTextTooLong, map[string]any{"max": maxRunes})
	}
	return local + "@" + strings.ToLower(domain), nil
}

func validEmailDomain(domain string) bool {
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
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

func isControlOrFormat(r rune) bool {
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

func isDomainByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}
