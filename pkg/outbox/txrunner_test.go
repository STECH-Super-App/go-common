package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

func TestNewTxRunner_withNilPool_callsFnWithNilTx(t *testing.T) {
	r := outbox.NewTxRunner(nil)

	called := false
	err := r.RunInTx(context.Background(), func(tx outbox.Tx) error {
		called = true
		if tx != nil {
			t.Errorf("tx = %v, want nil in nil-pool path", tx)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTx err = %v, want nil", err)
	}
	if !called {
		t.Error("fn not called")
	}
}

func TestNewTxRunner_propagatesFnError(t *testing.T) {
	r := outbox.NewTxRunner(nil)
	sentinel := errors.New("handler exploded")

	got := r.RunInTx(context.Background(), func(_ outbox.Tx) error {
		return sentinel
	})
	if !errors.Is(got, sentinel) {
		t.Errorf("err = %v, want %v", got, sentinel)
	}
}

// Compile-time check that the constructor returns an outbox.TxRunner.
var _ = outbox.NewTxRunner(nil)
