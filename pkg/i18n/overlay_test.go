package i18n_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	i18n "github.com/STECH-Super-App/go-common/pkg/i18n"
)

var baseline = map[string]string{
	"greet.title": "Hello {{.name}}",
	"only.code":   "code-only",
}

// writeDir materializes the given <name> -> <contents> map as real files in a
// fresh temp dir and returns its path. The overlay engine binds a directory
// path (not an fs.FS), so fixtures live on disk and Reload re-reads them.
func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(data), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return d
}

func TestBaselinesOnlyWhenPathEmpty(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, "")
	sum := b.Load(context.Background())
	if sum.Source != "baselines-only" {
		t.Fatalf("source = %q", sum.Source)
	}
	got, err := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "Ann"})
	if err != nil || got != "Hello Ann" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestOverlayWinsAndLocaleMatches(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"en.json": `{"greet":{"title":"Hi {{.name}}"}}`,
		"ru.json": `{"greet":{"title":"Привет {{.name}}"}}`,
	}))
	b.Load(context.Background())
	snap := b.Snapshot()
	if got, _ := snap.Resolve("ru-RU", "greet.title", map[string]any{"name": "Ann"}); got != "Привет Ann" {
		t.Fatalf("BCP47 match failed: %q", got) // F6: ru-RU → ru
	}
	if got, _ := snap.Resolve("kk", "greet.title", map[string]any{"name": "Ann"}); got != "Hi Ann" {
		t.Fatalf("en fallback failed: %q", got)
	}
	if got, _ := snap.Resolve("ru", "only.code", nil); got != "code-only" {
		t.Fatalf("baseline fallback failed: %q", got)
	}
}

func TestRejectUndeclaredPlaceholder(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"ru.json": `{"greet":{"title":"Привет {{.hacker}}"}}`,
	}), i18n.WithAllowedParams(func(_ string) ([]string, bool) {
		return []string{"name"}, true
	}))
	sum := b.Load(context.Background())
	if len(sum.Rejected) != 1 || sum.Rejected[0] != "ru/greet.title" {
		t.Fatalf("rejected = %v", sum.Rejected)
	}
	if got, _ := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "A"}); got != "Hello A" {
		t.Fatalf("rejected key must fall back to baseline, got %q", got)
	}
}

func TestShadowWarningOnEnOverride(t *testing.T) { // F3
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"en.json": `{"greet":{"title":"DIFFERENT {{.name}}"}}`,
	}))
	sum := b.Load(context.Background())
	if len(sum.Shadowed) != 1 || sum.Shadowed[0] != "greet.title" {
		t.Fatalf("shadowed = %v", sum.Shadowed)
	}
	// F3: the overlay en value wins at Resolve() time — the truth repo is
	// authoritative over the compiled-in baseline. Shadowed is a visibility
	// warning, not a rejection.
	got, err := b.Snapshot().Resolve("en", "greet.title", map[string]any{"name": "Ann"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := "DIFFERENT Ann"; got != want {
		t.Fatalf("resolve = %q, want %q (overlay must win over baseline)", got, want)
	}
}

func TestMissingKeysReported(t *testing.T) { // D5
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"en.json": `{"greet":{"title":"Hello {{.name}}"}}`,
	}))
	sum := b.Load(context.Background())
	if len(sum.Missing) != 1 || sum.Missing[0] != "only.code" {
		t.Fatalf("missing = %v", sum.Missing)
	}
}

func TestInvalidLocaleFileSkippedNotFatal(t *testing.T) { // F13
	b := i18n.NewOverlayBundle(baseline, writeDir(t, map[string]string{
		"ru.json":         `{"greet":{"title":"Привет {{.name}}"}}`,
		"not-a-tag!.json": `{}`,
		"broken.json":     `{{{`,
	}))
	sum := b.Load(context.Background())
	if sum.Source != "overlay" {
		t.Fatalf("invalid files must not abort the load: %v", sum)
	}
}

func TestReloadSwapsAtomically(t *testing.T) { // F4 substrate
	d := writeDir(t, map[string]string{"ru.json": `{"greet":{"title":"v1 {{.name}}"}}`})
	b := i18n.NewOverlayBundle(baseline, d)
	b.Load(context.Background())
	snap := b.Snapshot()
	if err := os.WriteFile(filepath.Join(d, "ru.json"), []byte(`{"greet":{"title":"v2 {{.name}}"}}`), 0o600); err != nil {
		t.Fatalf("rewrite ru.json: %v", err)
	}
	b.Reload(context.Background())
	// old snapshot still consistent:
	if got, _ := snap.Resolve("ru", "greet.title", map[string]any{"name": "x"}); got != "v1 x" {
		t.Fatalf("held snapshot changed: %q", got)
	}
	if got, _ := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "x"}); got != "v2 x" {
		t.Fatalf("new snapshot stale: %q", got)
	}
}

// TestSnapshotResolveRaceWithReload runs N reader goroutines resolving through
// b.Snapshot() while the main goroutine hammers Reload. The atomic Snapshot swap
// (spec F4) must let readers keep resolving without error or data race; run under
// -race to catch a torn read of the current pointer or its maps.
func TestSnapshotResolveRaceWithReload(t *testing.T) {
	d := writeDir(t, map[string]string{"ru.json": `{"greet":{"title":"v {{.name}}"}}`})
	b := i18n.NewOverlayBundle(baseline, d)
	b.Load(context.Background())

	const readers = 8
	stop := make(chan struct{})
	errCh := make(chan error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "x"}); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	for i := 0; i < 500; i++ {
		b.Reload(context.Background())
	}
	close(stop)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent resolve returned unexpected error: %v", err)
	}
}

func TestUnknownKeyTyped(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, "")
	b.Load(context.Background())
	if _, err := b.Snapshot().Resolve("en", "nope", nil); err == nil {
		t.Fatal("want ErrKeyNotFound")
	}
}

// TestReloadRecoversLateMount is the regression lock for the 2026-07 dev
// incident: a ConfigMap overlay mount that appears AFTER pod boot must be picked
// up by the next Reload with no restart. Construct against a not-yet-existing
// dir (mount not applied) -> Load degrades to baselines-only; then create the
// dir + write locale files (mount lands) -> Reload flips Source to "overlay" and
// keys resolve from the overlay. Before the late-bind fix the bundle froze a nil
// fs.FS at construction and could never recover.
func TestReloadRecoversLateMount(t *testing.T) {
	parent := t.TempDir()
	dirPath := filepath.Join(parent, "i18n") // deliberately not created yet
	b := i18n.NewOverlayBundle(baseline, dirPath)

	sum := b.Load(context.Background())
	if sum.Source != "baselines-only" {
		t.Fatalf("pre-mount Source = %q, want baselines-only", sum.Source)
	}
	if got, _ := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "Ann"}); got != "Hello Ann" {
		t.Fatalf("pre-mount resolve = %q, want baseline", got)
	}

	// Mount appears after boot.
	if err := os.Mkdir(dirPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "en.json"), []byte(`{"greet":{"title":"Hi {{.name}}"}}`), 0o600); err != nil {
		t.Fatalf("write en.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "ru.json"), []byte(`{"greet":{"title":"Привет {{.name}}"}}`), 0o600); err != nil {
		t.Fatalf("write ru.json: %v", err)
	}

	sum2 := b.Reload(context.Background())
	if sum2.Source != "overlay" {
		t.Fatalf("post-mount Source = %q, want overlay", sum2.Source)
	}
	if got, _ := b.Snapshot().Resolve("ru", "greet.title", map[string]any{"name": "Ann"}); got != "Привет Ann" {
		t.Fatalf("post-mount ru resolve = %q, want overlay value", got)
	}
	if got, _ := b.Snapshot().Resolve("en", "greet.title", map[string]any{"name": "Ann"}); got != "Hi Ann" {
		t.Fatalf("post-mount en resolve = %q, want overlay value", got)
	}
}
