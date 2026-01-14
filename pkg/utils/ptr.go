// Package utils provides utility functions.
package utils //nolint:revive

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}

// ToVal returns the value of the pointer or the zero value if the pointer is nil.
func ToVal[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// ToValDefault returns the value of the pointer or the default value if the pointer is nil.
func ToValDefault[T any](p *T, defaultVal T) T {
	if p == nil {
		return defaultVal
	}
	return *p
}
