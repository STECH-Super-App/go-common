package input_test

import (
	"errors"
	"math"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonErrors "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/input"
)

func TestUUID(t *testing.T) {
	ok := map[string]string{
		"550e8400-e29b-41d4-a716-446655440000": "550e8400-e29b-41d4-a716-446655440000",
		"550E8400-E29B-41D4-A716-446655440000": "550e8400-e29b-41d4-a716-446655440000",
		"00000000-0000-0000-0000-000000000000": "00000000-0000-0000-0000-000000000000",
	}
	for raw, want := range ok {
		got, f := input.UUID(raw)
		if f != nil || got != want {
			t.Errorf("UUID(%q) = (%q, %v), want %q", raw, got, f, want)
		}
	}
	bad := []string{
		"",
		"not-a-uuid",
		"{550e8400-e29b-41d4-a716-446655440000}",
		"urn:uuid:550e8400-e29b-41d4-a716-446655440000",
		"550e8400e29b41d4a716446655440000",
		" 550e8400-e29b-41d4-a716-446655440000",
		"550e8400-e29b-41d4-a716-44665544000g",
		"550e8400-e29b-41d4-a716_446655440000",
		"550e8400-e29b-41d4-a716-4466554400000",
		"550e8400-e29b-41d4-a716-44665544000\x00",
	}
	for _, raw := range bad {
		if _, f := input.UUID(raw); f == nil || f.Reason != input.ReasonUUIDInvalid {
			t.Errorf("UUID(%q): want %s, got %v", raw, input.ReasonUUIDInvalid, f)
		}
	}
}

func TestInt32AndInt64(t *testing.T) {
	if v, f := input.Int32(5, 1, 10); f != nil || v != 5 {
		t.Fatalf("Int32 in range: (%d, %v)", v, f)
	}
	f := failOf2(input.Int32(int64(math.MaxInt32)+1, 0, math.MaxInt32))
	if f == nil || f.Reason != input.ReasonNumberOutOfRange || f.Params["min"] != int32(0) || f.Params["max"] != int32(math.MaxInt32) {
		t.Fatalf("Int32 int32+1: got %+v", f)
	}
	if _, f := input.Int32(0, 1, 10); f == nil {
		t.Fatal("Int32 below min accepted")
	}
	if _, f := input.Int64(11, 1, 10); f == nil || f.Reason != input.ReasonNumberOutOfRange {
		t.Fatalf("Int64 above max: %v", f)
	}
	if v, f := input.Int64(math.MinInt64, math.MinInt64, math.MaxInt64); f != nil || v != math.MinInt64 {
		t.Fatalf("Int64 full range: (%d, %v)", v, f)
	}
	assertPanics(t, "Int32 inverted", func() { _, _ = input.Int32(0, 2, 1) })
	assertPanics(t, "Int64 inverted", func() { _, _ = input.Int64(0, 2, 1) })
}

func TestParseInt(t *testing.T) {
	cases := []struct {
		raw    string
		want   int64
		reason string
	}{
		{"0", 0, ""},
		{"42", 42, ""},
		{"-7", -7, ""},
		{"007", 7, ""},
		{"2147483647", math.MaxInt32, ""},
		{"2147483648", 0, input.ReasonNumberOutOfRange},
		{"-2147483649", 0, input.ReasonNumberOutOfRange},
		{"99999999999999999999999", 0, input.ReasonNumberOutOfRange},
		{"", 0, input.ReasonNumberInvalid},
		{"-", 0, input.ReasonNumberInvalid},
		{"+5", 0, input.ReasonNumberInvalid},
		{" 5", 0, input.ReasonNumberInvalid},
		{"5 ", 0, input.ReasonNumberInvalid},
		{"1e3", 0, input.ReasonNumberInvalid},
		{"1_000", 0, input.ReasonNumberInvalid},
		{"0x10", 0, input.ReasonNumberInvalid},
		{"1.0", 0, input.ReasonNumberInvalid},
		{"--1", 0, input.ReasonNumberInvalid},
		{"٣", 0, input.ReasonNumberInvalid}, // Arabic-Indic digit
	}
	for _, tc := range cases {
		got, f := input.ParseInt32(tc.raw, math.MinInt32, math.MaxInt32)
		checkParse(t, "ParseInt32", tc.raw, int64(got), f, tc.want, tc.reason)
	}

	got, f := input.ParseInt64("9223372036854775807", math.MinInt64, math.MaxInt64)
	checkParse(t, "ParseInt64", "max", got, f, math.MaxInt64, "")
	_, f = input.ParseInt64("9223372036854775808", 0, math.MaxInt64)
	checkParse(t, "ParseInt64", "max+1", 0, f, 0, input.ReasonNumberOutOfRange)
	_, f = input.ParseInt64("11", 1, 10)
	checkParse(t, "ParseInt64", "11 in [1,10]", 0, f, 0, input.ReasonNumberOutOfRange)
}

func TestParsePage(t *testing.T) {
	p, f := input.ParsePage("", "", 20, 100)
	if f != nil || p != (input.Page{Number: 1, Size: 20, Offset: 0}) {
		t.Fatalf("defaults: (%+v, %v)", p, f)
	}
	p, f = input.ParsePage("3", "50", 20, 100)
	if f != nil || p != (input.Page{Number: 3, Size: 50, Offset: 100}) {
		t.Fatalf("explicit: (%+v, %v)", p, f)
	}
	p, f = input.ParsePage("2147483647", "100", 20, 100)
	if f != nil || p.Offset != int64(math.MaxInt32-1)*100 {
		t.Fatalf("max page: (%+v, %v)", p, f)
	}

	bad := []struct {
		number, size, field, reason string
	}{
		{"0", "", "page", input.ReasonNumberOutOfRange},
		{"-1", "", "page", input.ReasonNumberOutOfRange},
		{"2147483648", "", "page", input.ReasonNumberOutOfRange},
		{"x", "", "page", input.ReasonNumberInvalid},
		{"", "0", "limit", input.ReasonNumberOutOfRange},
		{"", "101", "limit", input.ReasonNumberOutOfRange},
		{"", "ten", "limit", input.ReasonNumberInvalid},
	}
	for _, tc := range bad {
		_, f := input.ParsePage(tc.number, tc.size, 20, 100)
		fe := f.AtPage("page", "limit")
		if fe == nil || fe.Field != tc.field || fe.Reason != tc.reason {
			t.Errorf("ParsePage(%q,%q): got %+v, want field=%s reason=%s", tc.number, tc.size, fe, tc.field, tc.reason)
		}
	}
	assertPanics(t, "default > max", func() { _, _ = input.ParsePage("", "", 101, 100) })
	assertPanics(t, "max 0", func() { _, _ = input.ParsePage("", "", 0, 0) })
}

func TestMaxItems(t *testing.T) {
	if f := input.MaxItems(3, 3); f != nil {
		t.Fatalf("at cap: %v", f)
	}
	f := input.MaxItems(4, 3)
	if f == nil || f.Reason != input.ReasonTooManyItems || f.Params["max"] != 3 {
		t.Fatalf("over cap: %+v", f)
	}
	assertPanics(t, "max 0", func() { _ = input.MaxItems(0, 0) })
}

func TestPhone(t *testing.T) {
	ok := map[string]string{
		"+79991234567":          "+79991234567",
		"  +7 (999) 123-45-67 ": "+79991234567",
		"+1 650-253-0000":       "+16502530000",
		"+44 20 7031 3000":      "+442070313000",
		"\u200B+79991234567\n":  "+79991234567",
	}
	for raw, want := range ok {
		got, f := input.Phone(raw)
		if f != nil || got != want {
			t.Errorf("Phone(%q) = (%q, %v), want %q", raw, got, f, want)
		}
	}
	bad := []string{
		"",
		"89991234567",              // no '+'
		"+7999",                    // too short
		"+79991234567 ext. 5",      // extension cannot be stored in E.164
		"+7999\x001234567",         // interior NUL
		"+7999\u200B1234567",       // interior Cf
		"+0000000000",              // no such country code
		"\xff+79991234567",         // invalid UTF-8
		"+7 999 123 45 67 abc def", // trailing junk
	}
	for _, raw := range bad {
		if got, f := input.Phone(raw); f == nil || f.Reason != input.ReasonPhoneInvalid {
			t.Errorf("Phone(%q) = (%q, %v), want %s", raw, got, f, input.ReasonPhoneInvalid)
		}
	}
}

func TestEmail(t *testing.T) {
	ok := map[string]string{
		"user@example.com":            "user@example.com",
		"  User.Name@Example.COM  ":   "User.Name@example.com",
		"a+tag@sub-domain.example.ru": "a+tag@sub-domain.example.ru",
		"имя@example.com":             "имя@example.com",
	}
	for raw, want := range ok {
		got, f := input.Email(raw, 254)
		if f != nil || got != want {
			t.Errorf("Email(%q) = (%q, %v), want %q", raw, got, f, want)
		}
	}
	bad := []string{
		"",
		"plain",
		"@example.com",
		"user@",
		"user@localhost",
		"a@b@example.com",
		"user@-example.com",
		"user@example-.com",
		"user@exa_mple.com",
		"user@example..com",
		"user@.example.com",
		"user@example.com.",
		"us er@example.com",
		"user@exam ple.com",
		"us\x00er@example.com",
		"us\u200Ber@example.com",
		"user@пример.рф",
		"\xffuser@example.com",
	}
	for _, raw := range bad {
		if got, f := input.Email(raw, 254); f == nil || f.Reason != input.ReasonEmailInvalid {
			t.Errorf("Email(%q) = (%q, %v), want %s", raw, got, f, input.ReasonEmailInvalid)
		}
	}
	f := failOf(input.Email("abcdef@example.com", 10))
	if f == nil || f.Reason != input.ReasonTextTooLong || f.Params["max"] != 10 {
		t.Fatalf("over-long email: %+v", f)
	}
	assertPanics(t, "Email max 0", func() { _, _ = input.Email("a@b.c", 0) })
}

func TestFailure_ErrorAtCollect(t *testing.T) {
	_, f := input.Line("", 10, input.Required())
	var err error = f
	var got *input.Failure
	if !errors.As(err, &got) || got.Reason != input.ReasonTextRequired {
		t.Fatalf("errors.As: %v", err)
	}
	if err.Error() != "COMMON_TEXT_REQUIRED: value is required" {
		t.Fatalf("Error(): %q", err.Error())
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("validator failure must have no cause")
	}

	var none *input.Failure
	_, tooLong := input.Line("abcdef", 3)
	details := input.Collect(none.At("a"), f.At("name"), nil, tooLong.At("title"))
	want := []commonErrors.FieldError{
		{Field: "name", Reason: input.ReasonTextRequired, Message: "value is required"},
		{Field: "title", Reason: input.ReasonTextTooLong, Message: "value is too long", Params: map[string]any{"max": 3}},
	}
	if len(details) != len(want) {
		t.Fatalf("Collect: %+v", details)
	}
	for i := range want {
		if details[i].Field != want[i].Field || details[i].Reason != want[i].Reason || details[i].Message != want[i].Message {
			t.Errorf("Collect[%d] = %+v, want %+v", i, details[i], want[i])
		}
	}
	if input.Collect(none.At("x"), nil) != nil {
		t.Fatal("Collect of nils must be nil")
	}
}

func TestRejectedByStorage(t *testing.T) {
	cause := errors.New("sqlstate 22021")
	f := input.RejectedByStorage(cause)
	if f.Reason != input.ReasonInputRejectedByStorage || !errors.Is(f, cause) {
		t.Fatalf("got %+v", f)
	}
	if fe := f.At("x"); fe.Message != "value was rejected by storage" || fe.Params != nil {
		t.Fatalf("At leaks cause or wrong message: %+v", fe)
	}
}

func TestMessageForEveryReason(t *testing.T) {
	for _, r := range []string{
		input.ReasonTextRequired, input.ReasonTextTooLong, input.ReasonTextInvalidCharacters,
		input.ReasonUUIDInvalid, input.ReasonNumberInvalid, input.ReasonNumberOutOfRange,
		input.ReasonTooManyItems, input.ReasonPhoneInvalid, input.ReasonEmailInvalid,
		input.ReasonInputRejectedByStorage, input.ReasonRequestEncodingInvalid,
	} {
		if m := input.MessageFor(r); m == "" || m == "invalid input" {
			t.Errorf("%s has no English message", r)
		}
	}
}

func TestGRPC(t *testing.T) {
	if input.GRPC() != nil {
		t.Fatal("GRPC() with no fields must be nil")
	}
	_, f1 := input.UUID("nope")
	_, f2 := input.Line("", 5, input.Required())
	err := input.GRPC(input.Collect(f1.At("tenant_id"), f2.At("name"))...)
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument || st.Message() != input.ReasonUUIDInvalid {
		t.Fatalf("status: %v", err)
	}
	if len(st.Details()) != 1 {
		t.Fatalf("details: %v", st.Details())
	}
	br, ok := st.Details()[0].(*errdetails.BadRequest)
	if !ok || len(br.GetFieldViolations()) != 2 {
		t.Fatalf("BadRequest: %v", st.Details()[0])
	}
	v := br.GetFieldViolations()[1]
	if v.GetField() != "name" || v.GetDescription() != input.ReasonTextRequired || v.GetReason() != input.ReasonTextRequired {
		t.Fatalf("violation: %v", v)
	}
}

func failOf2(_ int32, f *input.Failure) *input.Failure { return f }

func checkParse(t *testing.T, fn, raw string, got int64, f *input.Failure, want int64, reason string) {
	t.Helper()
	if reason == "" {
		if f != nil || got != want {
			t.Errorf("%s(%q) = (%d, %v), want %d", fn, raw, got, f, want)
		}
		return
	}
	if f == nil || f.Reason != reason {
		t.Errorf("%s(%q): got %v, want %s", fn, raw, f, reason)
	}
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: did not panic", name)
		}
	}()
	fn()
}
