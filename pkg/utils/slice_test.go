package utils //nolint:revive

import "testing"

func TestContains(t *testing.T) {
	slice := []int{1, 2, 3}
	if !Contains(slice, 2) {
		t.Error("Contains() should return true for existing element")
	}
	if Contains(slice, 4) {
		t.Error("Contains() should return false for missing element")
	}
}

func TestMap(t *testing.T) {
	slice := []int{1, 2, 3}
	mapped := Map(slice, func(i int) int { return i * 2 })
	if len(mapped) != 3 || mapped[0] != 2 || mapped[1] != 4 || mapped[2] != 6 {
		t.Errorf("Map() returned unexpected result: %v", mapped)
	}
}
