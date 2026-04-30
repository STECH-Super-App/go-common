package i18n_test

import (
	"embed"
	"errors"
	"io/fs"
	"testing"

	"golang.org/x/text/language"

	commoni18n "github.com/STECH-Super-App/go-common/pkg/i18n"
)

//go:embed testdata/*.toml
var testFS embed.FS

func subFS(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(testFS, "testdata")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	return sub
}

func TestResolveExactLocale(t *testing.T) {
	b, err := commoni18n.LoadBundle(subFS(t), language.English)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	got, err := b.Resolve("ru", "hello", map[string]any{"Name": "Мир"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "Привет, Мир" {
		t.Errorf("ru hello: want 'Привет, Мир', got %q", got)
	}
}

func TestResolveLocaleFallbackToDefault(t *testing.T) {
	b, err := commoni18n.LoadBundle(subFS(t), language.English)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	// 'transfer.expired' exists only in en.toml — ru request must fall back to en.
	got, err := b.Resolve("ru", "transfer.expired", map[string]any{"ID": "abc", "Time": "14:00"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "Transfer abc expired at 14:00" {
		t.Errorf("transfer.expired ru fallback: want 'Transfer abc expired at 14:00', got %q", got)
	}
}

func TestResolveMissingKeyReturnsErrKeyNotFound(t *testing.T) {
	b, err := commoni18n.LoadBundle(subFS(t), language.English)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	_, err = b.Resolve("en", "does.not.exist", nil)
	if !errors.Is(err, commoni18n.ErrKeyNotFound) {
		t.Errorf("missing key: want ErrKeyNotFound, got %v", err)
	}
}

func TestResolveLocaleAliasMatching(t *testing.T) {
	// kk-KZ should resolve to kk (Kazakh, ISO 639-1).
	b, err := commoni18n.LoadBundle(subFS(t), language.English)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	got, err := b.Resolve("kk-KZ", "hello", map[string]any{"Name": "Әлем"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "Сәлем, Әлем" {
		t.Errorf("kk-KZ hello: want 'Сәлем, Әлем', got %q", got)
	}
}

func TestSharedBundleHasErrorKeysInAllLocales(t *testing.T) {
	b, err := commoni18n.SharedBundle()
	if err != nil {
		t.Fatalf("SharedBundle: %v", err)
	}
	cases := []struct {
		locale, want string
	}{
		{"en", "Unauthorized"},
		{"ru", "Не авторизован"},
		{"kk", "Авторизацияланбаған"},
	}
	for _, tc := range cases {
		got, err := b.Resolve(tc.locale, "error.unauthorized", nil)
		if err != nil {
			t.Errorf("locale %s: Resolve error %v", tc.locale, err)
			continue
		}
		if got != tc.want {
			t.Errorf("locale %s: want %q, got %q", tc.locale, tc.want, got)
		}
	}
}
