package money

import (
	"math"
	"testing"
)

func TestParseCode(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"RUB", true}, {"USD", true},
		{"rub", false}, {"RU", false}, {"RUBL", false}, {"R1B", false}, {"", false},
	} {
		_, err := ParseCode(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseCode(%q): err=%v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

func TestAdd(t *testing.T) {
	rub := Code("RUB")
	a, _ := New(150_00, rub)
	b, _ := New(50_00, rub)
	sum, err := a.Add(b)
	if err != nil || sum.AmountMinor() != 200_00 {
		t.Fatalf("sum=%v err=%v", sum, err)
	}
}

func TestAdd_CurrencyMismatch(t *testing.T) {
	a, _ := New(1, Code("RUB"))
	b, _ := New(1, Code("USD"))
	if _, err := a.Add(b); err == nil {
		t.Fatal("expected currency-mismatch error")
	}
}

func TestAdd_Overflow(t *testing.T) {
	a, _ := New(int64(1)<<62, Code("RUB"))
	if _, err := a.Add(a); err == nil {
		t.Fatal("expected overflow error")
	}
	neg, _ := New(-(int64(1) << 62), Code("RUB"))
	negOne, _ := New(-4, Code("RUB"))
	if _, err := neg.Add(neg); err == nil {
		t.Fatal("expected negative overflow error")
	}
	_ = negOne
}

func TestMul(t *testing.T) {
	a, _ := New(150_00, Code("RUB"))
	got, err := a.Mul(3)
	if err != nil || got.AmountMinor() != 450_00 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if _, err := a.Mul(0); err != nil {
		t.Fatal("mul by zero must be legal (=0)")
	}
}

func TestMul_Overflow(t *testing.T) {
	a, _ := New(int64(1)<<40, Code("RUB"))
	if _, err := a.Mul(1 << 30); err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestMul_MinInt64TimesMinusOne(t *testing.T) {
	m, _ := New(math.MinInt64, Code("RUB"))
	if _, err := m.Mul(-1); err == nil {
		t.Fatal("expected overflow error for MinInt64 × -1")
	}
}

func TestCmp(t *testing.T) {
	a, _ := New(100, Code("RUB"))
	b, _ := New(200, Code("RUB"))
	if c, err := a.Cmp(b); err != nil || c != -1 {
		t.Fatalf("cmp=%d err=%v", c, err)
	}
	usd, _ := New(100, Code("USD"))
	if _, err := a.Cmp(usd); err == nil {
		t.Fatal("expected cross-currency comparison error")
	}
}
