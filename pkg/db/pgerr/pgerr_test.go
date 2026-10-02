package pgerr

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/STECH-Super-App/go-common/pkg/input"
	"github.com/STECH-Super-App/go-common/pkg/metrics"
)

func TestInvalidInput_RecognisedSQLStates(t *testing.T) {
	for _, code := range []string{"22021", "22P05", "22001", "22P02"} {
		t.Run(code, func(t *testing.T) {
			before := testutil.ToFloat64(backstopTotal.WithLabelValues(code))
			pgErr := &pgconn.PgError{Code: code, TableName: "listings", ColumnName: "title", Message: "boom"}
			wrapped := fmt.Errorf("repo: %w", pgErr)

			f, ok := InvalidInput(wrapped)
			if !ok || f == nil {
				t.Fatalf("InvalidInput(%s) = (%v, %v), want hit", code, f, ok)
			}
			if f.Reason != input.ReasonInputRejectedByStorage {
				t.Fatalf("reason %s", f.Reason)
			}
			var got *pgconn.PgError
			if !errors.As(f, &got) || got != pgErr {
				t.Fatal("cause does not unwrap to the PgError")
			}
			if msg := f.Error(); !strings.Contains(msg, `table="listings"`) || !strings.Contains(msg, `column="title"`) || !strings.Contains(msg, code) {
				t.Fatalf("Error() does not name sqlstate/table/column: %q", msg)
			}
			if fe := f.At("title"); fe.Params != nil || strings.Contains(fe.Message, "listings") {
				t.Fatalf("FieldError leaks storage detail: %+v", fe)
			}
			if after := testutil.ToFloat64(backstopTotal.WithLabelValues(code)); after != before+1 {
				t.Fatalf("counter %s: %v -> %v", code, before, after)
			}
		})
	}
}

func TestInvalidInput_IgnoresOtherErrors(t *testing.T) {
	for name, err := range map[string]error{
		"nil":                    nil,
		"unique violation":       &pgconn.PgError{Code: "23505"},
		"fk violation":           &pgconn.PgError{Code: "23503"},
		"numeric overflow":       &pgconn.PgError{Code: "22003"}, // server-side arithmetic, not an input encode
		"no rows":                pgx.ErrNoRows,
		"plain":                  errors.New("connection refused"),
		"scan range (no encode)": errors.New("can't scan into dest[0]: 2147483648 is greater than maximum value for int32"),
	} {
		t.Run(name, func(t *testing.T) {
			if f, ok := InvalidInput(err); ok || f != nil {
				t.Fatalf("InvalidInput(%v) = (%v, %v), want miss", err, f, ok)
			}
		})
	}
}

// A real pgx v5 encode error, produced by the same code path Exec/Query use to
// bind arguments (ExtendedQueryBuilder.Build over a pgtype.Map), with no
// database. If a pgx bump rewords the message, this test fails and
// isIntEncodeError must be updated.
func TestInvalidInput_RealPgxEncodeError(t *testing.T) {
	m := pgtype.NewMap()
	cases := map[string]struct {
		oid uint32
		arg any
	}{
		"int4 overflow":  {pgtype.Int4OID, int64(math.MaxInt32) + 1},
		"int4 underflow": {pgtype.Int4OID, int64(math.MinInt32) - 1},
		"int2 overflow":  {pgtype.Int2OID, int64(math.MaxInt16) + 1},
		"int8 from uint": {pgtype.Int8OID, uint64(math.MaxInt64) + 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var eqb pgx.ExtendedQueryBuilder
			sd := &pgconn.StatementDescription{ParamOIDs: []uint32{tc.oid}}
			err := eqb.Build(m, sd, []any{tc.arg})
			if err == nil {
				t.Fatal("pgx encoded an out-of-range value; the pinned error premise is gone")
			}
			t.Logf("pgx error: %v", err)

			before := testutil.ToFloat64(backstopTotal.WithLabelValues(LabelEncode))
			f, ok := InvalidInput(err)
			if !ok || f.Reason != input.ReasonInputRejectedByStorage {
				t.Fatalf("InvalidInput missed real encode error %q", err)
			}
			if !errors.Is(f, err) {
				t.Fatal("cause does not unwrap to the pgx error")
			}
			if after := testutil.ToFloat64(backstopTotal.WithLabelValues(LabelEncode)); after != before+1 {
				t.Fatalf("encode counter: %v -> %v", before, after)
			}
		})
	}
}

func TestInvalidInput_ExactPgxMessagePinned(t *testing.T) {
	var eqb pgx.ExtendedQueryBuilder
	err := eqb.Build(pgtype.NewMap(), &pgconn.StatementDescription{ParamOIDs: []uint32{pgtype.Int4OID}},
		[]any{int64(math.MaxInt32) + 1})
	const want = "failed to encode args[0]: unable to encode 2147483648 into binary format for int4 (OID 23): 2147483648 is greater than maximum value for int4"
	if err == nil || err.Error() != want {
		t.Fatalf("pgx v5 encode message changed:\n got: %v\nwant: %s", err, want)
	}
}

func TestBackstopSeriesPreCreatedOnRegistry(t *testing.T) {
	families, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, mf := range families {
		if mf.GetName() != "stech_input_backstop_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "sqlstate" {
					labels[lp.GetValue()] = true
				}
			}
		}
	}
	for _, want := range []string{"22021", "22P05", "22001", "22P02", "encode"} {
		if !labels[want] {
			t.Errorf("series sqlstate=%s missing from metrics.Registry", want)
		}
	}
}
