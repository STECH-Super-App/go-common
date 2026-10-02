package input

// UUID accepts only the 36-character hyphenated form
// (8-4-4-4-12 hex digits, any case) and returns it lower-cased. Braces,
// "urn:uuid:", bare 32-hex and surrounding whitespace are refused with
// COMMON_UUID_INVALID. The nil UUID is accepted; a caller that forbids it
// checks that in its domain.
func UUID(raw string) (string, *Failure) {
	if len(raw) != 36 {
		return "", fail(ReasonUUIDInvalid, nil)
	}
	out := []byte(raw)
	for i := range out {
		c := raw[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", fail(ReasonUUIDInvalid, nil)
			}
		default:
			switch {
			case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			case c >= 'A' && c <= 'F':
				c += 'a' - 'A'
			default:
				return "", fail(ReasonUUIDInvalid, nil)
			}
		}
		out[i] = c
	}
	return string(out), nil
}
