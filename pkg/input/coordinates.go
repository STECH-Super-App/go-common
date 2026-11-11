package input

import "math"

// Coordinate bounds in decimal degrees (WGS 84), both ends included.
const (
	maxLatitude  = 90.0
	maxLongitude = 180.0
)

// Coordinates checks a latitude/longitude pair in decimal degrees and returns
// the cleaned pair. Three rules, checked in this order:
//
//   - NaN or ±Inf in either component → COMMON_COORDINATES_INVALID;
//   - lat outside [-90, 90] or lon outside [-180, 180] (both ends included)
//     → COMMON_COORDINATES_INVALID;
//   - exactly (0, 0), any sign of zero → COMMON_COORDINATES_UNSET. That is the
//     value a client sends when it has no point (an unset map picker, a
//     zero-valued struct), and no real address in this market lies at null
//     island. A single zero axis — (0, x) or (x, 0) with x ≠ 0 — is a real
//     place on the equator or the prime meridian and stays valid.
//
// Neither Reason carries params: the bounds are fixed, not chosen per call.
// The cleaned pair equals the input except that a -0 component comes back as
// +0: they are the same point, but encoding/json writes -0 as "-0", which
// would put a sign a client never meant into every stored and echoed copy.
// The pair is a fixed point: validating the cleaned values again returns them
// unchanged. The caller attaches one field name with Failure.At (the point's
// field, e.g. "location"), since the rules judge the pair, not one axis.
func Coordinates(lat, lon float64) (float64, float64, *Failure) {
	if !isFinite(lat) || !isFinite(lon) {
		return 0, 0, fail(ReasonCoordinatesInvalid, nil)
	}
	if lat < -maxLatitude || lat > maxLatitude || lon < -maxLongitude || lon > maxLongitude {
		return 0, 0, fail(ReasonCoordinatesInvalid, nil)
	}
	if lat == 0 && lon == 0 { // -0 == 0 in IEEE 754, so (-0, -0) is caught too
		return 0, 0, fail(ReasonCoordinatesUnset, nil)
	}
	return positiveZero(lat), positiveZero(lon), nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// positiveZero maps -0 to +0 and returns every other value unchanged.
func positiveZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}
