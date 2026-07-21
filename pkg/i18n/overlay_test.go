package i18n_test

import (
	"context"
	"sync"
	"testing"
	"testing/fstest"

	i18n "github.com/STECH-Super-App/go-common/pkg/i18n"
)

var baseline = map[string]string{
	"greet.title": "Hello {{.name}}",
	"only.code":   "code-only",
}

func dir(files map[string]string) fstest.MapFS {
	fs := fstest.MapFS{}
	for name, data := range files {
		fs[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fs
}

func TestBaselinesOnlyWhenDirNil(t *testing.T) {
	b := i18n.NewOverlayBundle(baseline, nil)
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
	b := i18n.NewOverlayBundle(baseline, dir(map[string]string{
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
	b := i18n.NewOverlayBundle(baseline, dir(map[string]string{
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
	b := i18n.NewOverlayBundle(baseline, dir(map[string]string{
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
	b := i18n.NewOverlayBundle(baseline, dir(map[string]string{
		"en.json": `{"greet":{"title":"Hello {{.name}}"}}`,
	}))
	sum := b.Load(context.Background())
	if len(sum.Missing) != 1 || sum.Missing[0] != "only.code" {
		t.Fatalf("missing = %v", sum.Missing)
	}
}

func TestInvalidLocaleFileSkippedNotFatal(t *testing.T) { // F13
	b := i18n.NewOverlayBundle(baseline, dir(map[string]string{
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
	fsys := dir(map[string]string{"ru.json": `{"greet":{"title":"v1 {{.name}}"}}`})
	b := i18n.NewOverlayBundle(baseline, fsys)
	b.Load(context.Background())
	snap := b.Snapshot()
	fsys["ru.json"] = &fstest.MapFile{Data: []byte(`{"greet":{"title":"v2 {{.name}}"}}`)}
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
	fsys := dir(map[string]string{"ru.json": `{"greet":{"title":"v {{.name}}"}}`})
	b := i18n.NewOverlayBundle(baseline, fsys)
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
	b := i18n.NewOverlayBundle(baseline, nil)
	b.Load(context.Background())
	if _, err := b.Snapshot().Resolve("en", "nope", nil); err == nil {
		t.Fatal("want ErrKeyNotFound")
	}
}
