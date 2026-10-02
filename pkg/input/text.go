package input

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextOpt adjusts Line and Body.
type TextOpt func(*textOpts)

type textOpts struct {
	required bool
}

// Required makes a blank value (empty after trimming, or made only of
// whitespace, format characters, variation selectors and other
// default-ignorable code points) fail with COMMON_TEXT_REQUIRED. Without it,
// such a value is returned as "".
func Required() TextOpt {
	return func(o *textOpts) { o.required = true }
}

// Line validates a single-line text value of at most maxRunes runes and returns the
// cleaned value. See Body for the processing order; Line additionally refuses
// every control character (TAB, LF and CR included) and the bidi/separator set
// in isSingleLineDenied.
func Line(raw string, maxRunes int, opts ...TextOpt) (string, *Failure) {
	return text(raw, maxRunes, false, opts)
}

// Body validates a multi-line text value of at most maxRunes runes and returns the
// cleaned value. Processing order (identical for Line):
//
//  1. invalid UTF-8 → COMMON_TEXT_INVALID_CHARACTERS;
//  2. trim leading/trailing whitespace, Cc and Cf — the trimmed string is what
//     is checked and returned;
//  3. character rule → COMMON_TEXT_INVALID_CHARACTERS (Body allows TAB, LF, CR);
//  4. blank → COMMON_TEXT_REQUIRED with Required(), else "";
//  5. rune count > maxRunes → COMMON_TEXT_TOO_LONG, params {max}.
//
// No Unicode normalisation is applied. maxRunes <= 0 panics.
func Body(raw string, maxRunes int, opts ...TextOpt) (string, *Failure) {
	return text(raw, maxRunes, true, opts)
}

func text(raw string, maxRunes int, multiLine bool, opts []TextOpt) (string, *Failure) {
	if maxRunes <= 0 {
		panic("input: text maxRunes must be positive")
	}
	var o textOpts
	for _, opt := range opts {
		opt(&o)
	}

	if !utf8.ValidString(raw) {
		return "", fail(ReasonTextInvalidCharacters, nil)
	}

	v := trimEdges(raw)

	if hasForbiddenRune(v, multiLine) {
		return "", fail(ReasonTextInvalidCharacters, nil)
	}

	if isBlank(v) {
		if o.required {
			return "", fail(ReasonTextRequired, nil)
		}
		return "", nil
	}

	if utf8.RuneCountInString(v) > maxRunes {
		return "", fail(ReasonTextTooLong, map[string]any{"max": maxRunes})
	}
	return v, nil
}

// trimEdges removes leading and trailing runes that are whitespace
// (unicode.IsSpace), control (Cc) or format (Cf) characters — step 2 of
// Line/Body, shared with Phone and Email.
func trimEdges(s string) string {
	return strings.TrimFunc(s, isEdgeTrimmed)
}

func isEdgeTrimmed(r rune) bool {
	return unicode.IsSpace(r) || unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// hasForbiddenRune is the character rule. It mirrors sale-service
// app/Support/Validation/ControlCharacters.php (::found) exactly:
//
//   - multi-line: any \p{Cc} except TAB, LF, CR;
//   - single-line: any \p{Cc}, plus SINGLE_LINE_DENIED (ControlCharacters.php:93).
//
// Cf inside the text (ZWJ, U+200B, U+FEFF) is allowed — emoji ZWJ sequences
// need U+200D, which is why the rule never matches the whole Cf category.
func hasForbiddenRune(s string, multiLine bool) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) {
			if multiLine && (r == '\t' || r == '\n' || r == '\r') {
				continue
			}
			return true
		}
		if !multiLine && isSingleLineDenied(r) {
			return true
		}
	}
	return false
}

// isSingleLineDenied is sale-service's SINGLE_LINE_DENIED set
// (app/Support/Validation/ControlCharacters.php:93): LRM/RLM, the bidi
// embedding/override set, the bidi isolate set, and LINE/PARAGRAPH SEPARATOR.
func isSingleLineDenied(r rune) bool {
	switch {
	case r == 0x200E || r == 0x200F: // LRM, RLM
		return true
	case r >= 0x202A && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	case r == 0x2028 || r == 0x2029: // LINE SEPARATOR, PARAGRAPH SEPARATOR
		return true
	}
	return false
}

// isBlank reports whether s holds nothing visible: every rune is whitespace,
// a format character, a variation selector or another default-ignorable code
// point. The empty string is blank.
func isBlank(s string) bool {
	for _, r := range s {
		if !isInvisible(r) {
			return false
		}
	}
	return true
}

func isInvisible(r rune) bool {
	return unicode.IsSpace(r) ||
		unicode.Is(unicode.Cf, r) ||
		unicode.Is(unicode.Variation_Selector, r) ||
		unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r)
}
