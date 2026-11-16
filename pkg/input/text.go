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
// (unicode.IsSpace), control (Cc) or one of the invisible format characters
// named in isInvisibleFormat — step 2 of Line/Body, shared with Phone and Email.
func trimEdges(s string) string {
	return strings.TrimFunc(s, isEdgeTrimmed)
}

func isEdgeTrimmed(r rune) bool {
	return unicode.IsSpace(r) || unicode.Is(unicode.Cc, r) || isInvisibleFormat(r)
}

// isInvisibleFormat is the named list of format (Cf) runes that draw nothing and
// only ever appear at the edge of a text by accident or to disguise it: BOM,
// the zero-width spaces and joiners, direction marks and the bidi embedding /
// override / isolate controls, word joiner and the invisible operators, soft
// hyphen, Arabic letter mark, Mongolian vowel separator and the deprecated
// format controls.
//
// It is NOT the whole Cf category, which also holds runes that carry meaning at
// an edge: the emoji tag characters U+E0020–U+E007F that END a flag sequence
// (a text ending in the flag of Scotland must keep it) and the Arabic number
// signs U+0600–U+0605 / U+06DD that START a number (SL-17).
func isInvisibleFormat(r rune) bool {
	switch {
	case r == 0x00AD, r == 0x061C, r == 0x180E, r == 0xFEFF:
		return true // soft hyphen, ALM, Mongolian vowel separator, BOM
	case r >= 0x200B && r <= 0x200F: // ZWSP, ZWNJ, ZWJ, LRM, RLM
		return true
	case r >= 0x202A && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2060 && r <= 0x206F: // WJ, invisible operators, isolates, deprecated
		return true
	}
	return false
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
	// Body takes bidi controls only in matched pairs (SL-19): an unclosed
	// override such as RLO reverses everything after it, which lets a
	// description display a phone number or a link backwards. Line refuses them
	// outright above.
	return multiLine && hasUnbalancedBidi(s)
}

// hasUnbalancedBidi reports a bidi embedding/override (LRE, RLE, LRO, RLO) that
// is never closed by PDF, a PDF with nothing to close, or the same for the
// isolates (LRI, RLI, FSI closed by PDI). The two families are counted apart,
// so an RLO closed by an isolate's PDI is still unbalanced. LRM and RLM are
// marks, not openers, and are always allowed.
func hasUnbalancedBidi(s string) bool {
	var embeds, isolates int
	for _, r := range s {
		switch {
		case r >= 0x202A && r <= 0x202B, r == 0x202D, r == 0x202E:
			embeds++
		case r == 0x202C:
			if embeds == 0 {
				return true
			}
			embeds--
		case r >= 0x2066 && r <= 0x2068:
			isolates++
		case r == 0x2069:
			if isolates == 0 {
				return true
			}
			isolates--
		}
	}
	return embeds != 0 || isolates != 0
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
		unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		isBlankLookalike(r)
}

// isBlankLookalike lists runes that are neither space nor default-ignorable but
// draw nothing, so a required field could be "filled" with them (SL-19). U+2800
// BRAILLE PATTERN BLANK is the known one; the Hangul and Khmer fillers are
// default-ignorable already and are listed so the intent survives a Unicode
// table change.
func isBlankLookalike(r rune) bool {
	switch r {
	case 0x2800, // BRAILLE PATTERN BLANK
		0x115F, 0x1160, 0x3164, 0xFFA0, // Hangul fillers
		0x17B4, 0x17B5: // Khmer inherent vowels
		return true
	}
	return false
}
