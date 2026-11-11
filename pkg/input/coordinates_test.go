package input_test

import (
	"math"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/input"
)

func TestCoordinates(t *testing.T) {
	negZero := math.Copysign(0, -1)
	nan, posInf, negInf := math.NaN(), math.Inf(1), math.Inf(-1)
	below := func(v float64) float64 { return math.Nextafter(v, math.Inf(-1)) }
	above := func(v float64) float64 { return math.Nextafter(v, math.Inf(1)) }

	cases := []struct {
		name     string
		lat, lon float64
		reason   string // "" means accepted
	}{
		// Rule 1: NaN or ±Inf in either component.
		{"lat NaN", nan, 37.6, input.ReasonCoordinatesInvalid},
		{"lon NaN", 55.7, nan, input.ReasonCoordinatesInvalid},
		{"lat +Inf", posInf, 37.6, input.ReasonCoordinatesInvalid},
		{"lat -Inf", negInf, 37.6, input.ReasonCoordinatesInvalid},
		{"lon +Inf", 55.7, posInf, input.ReasonCoordinatesInvalid},
		{"lon -Inf", 55.7, negInf, input.ReasonCoordinatesInvalid},
		{"both NaN", nan, nan, input.ReasonCoordinatesInvalid},
		{"NaN beside zero", nan, 0, input.ReasonCoordinatesInvalid},

		// Rule 2: the four boundaries are included; just past them is refused.
		{"lat -90", -90, 37.6, ""},
		{"lat 90", 90, 37.6, ""},
		{"lon -180", 55.7, -180, ""},
		{"lon 180", 55.7, 180, ""},
		{"lat below -90", below(-90), 37.6, input.ReasonCoordinatesInvalid},
		{"lat above 90", above(90), 37.6, input.ReasonCoordinatesInvalid},
		{"lon below -180", 55.7, below(-180), input.ReasonCoordinatesInvalid},
		{"lon above 180", 55.7, above(180), input.ReasonCoordinatesInvalid},
		{"lat 91", 91, 37.6, input.ReasonCoordinatesInvalid},
		{"lon -181", 55.7, -181, input.ReasonCoordinatesInvalid},
		{"swapped pair", 37.6, 181, input.ReasonCoordinatesInvalid},
		// Out of range beside a zero axis is invalid, not unset.
		{"lat 0, lon out of range", 0, 200, input.ReasonCoordinatesInvalid},

		// Rule 3: exactly (0, 0), whatever the sign of zero.
		{"(0, 0)", 0, 0, input.ReasonCoordinatesUnset},
		{"(-0, 0)", negZero, 0, input.ReasonCoordinatesUnset},
		{"(0, -0)", 0, negZero, input.ReasonCoordinatesUnset},
		{"(-0, -0)", negZero, negZero, input.ReasonCoordinatesUnset},

		// One zero axis is a real place: the equator or the prime meridian.
		{"equator", 0, 37.6, ""},
		{"prime meridian", 55.7, 0, ""},
		{"equator at the antimeridian", 0, 180, ""},
		{"pole on the prime meridian", -90, 0, ""},
		{"smallest non-zero lat", math.SmallestNonzeroFloat64, 0, ""},
		{"Moscow", 55.7558, 37.6173, ""},
		{"western and southern", -33.8688, -70.6693, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lat, lon, f := input.Coordinates(tc.lat, tc.lon)
			if tc.reason != "" {
				if f == nil || f.Reason != tc.reason {
					t.Fatalf("Coordinates(%v, %v) = (%v, %v, %v), want %s", tc.lat, tc.lon, lat, lon, f, tc.reason)
				}
				if f.Params != nil {
					t.Fatalf("Coordinates(%v, %v): want no params, got %v", tc.lat, tc.lon, f.Params)
				}
				if lat != 0 || lon != 0 {
					t.Fatalf("Coordinates(%v, %v): failure with non-zero output (%v, %v)", tc.lat, tc.lon, lat, lon)
				}
				return
			}
			if f != nil {
				t.Fatalf("Coordinates(%v, %v): unexpected failure %v", tc.lat, tc.lon, f)
			}
			if lat != tc.lat || lon != tc.lon {
				t.Fatalf("Coordinates(%v, %v) changed the pair to (%v, %v)", tc.lat, tc.lon, lat, lon)
			}
		})
	}
}

func TestCoordinatesNormalisesNegativeZero(t *testing.T) {
	negZero := math.Copysign(0, -1)
	for _, tc := range []struct{ lat, lon float64 }{{negZero, 37.6}, {55.7, negZero}} {
		lat, lon, f := input.Coordinates(tc.lat, tc.lon)
		if f != nil {
			t.Fatalf("Coordinates(%v, %v): unexpected failure %v", tc.lat, tc.lon, f)
		}
		if math.Signbit(lat) && lat == 0 || math.Signbit(lon) && lon == 0 {
			t.Fatalf("Coordinates(%v, %v) = (%v, %v): -0 survived", tc.lat, tc.lon, lat, lon)
		}
	}
}

func TestCoordinatesFailureAt(t *testing.T) {
	_, _, f := input.Coordinates(0, 0)
	fe := f.At("location")
	if fe == nil || fe.Field != "location" || fe.Reason != input.ReasonCoordinatesUnset ||
		fe.Message != input.MessageFor(input.ReasonCoordinatesUnset) {
		t.Fatalf("At: %+v", fe)
	}
}
