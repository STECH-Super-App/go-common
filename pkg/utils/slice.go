package utils //nolint:revive

// Contains checks if a slice contains a specific element.
func Contains[T comparable](slice []T, item T) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// Map applies a function to each element of a slice and returns a new slice with the results.
func Map[T any, R any](slice []T, f func(T) R) []R {
	result := make([]R, len(slice))
	for i, s := range slice {
		result[i] = f(s)
	}
	return result
}

// Filter returns a new slice containing only elements that satisfy the predicate.
func Filter[T any](slice []T, f func(T) bool) []T {
	var result []T
	for _, s := range slice {
		if f(s) {
			result = append(result, s)
		}
	}
	return result
}
