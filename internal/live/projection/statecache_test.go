package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
)

// oneResourceState builds a snapshot holding a single managed resource, so a
// round trip has something to lose.
func oneResourceState(t *testing.T) *states.State {
	t.Helper()
	s := states.NewState()
	s.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_iam_role",
			Name: "example",
		}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"role-1"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{
			Provider: addrs.NewDefaultProvider("aws"),
			Module:   addrs.RootModule,
		},
		addrs.NoKey,
	)
	return s
}

// TestStateCacheOffByDefault pins that a MANAGER nobody enabled the cache on
// writes nothing at all - the zero value stays inert. The command layer is
// what enables it, by default since v0.6.0 (the #685 ruling), through
// stateCachePath; a Manager constructed anywhere else must not grow a
// side effect nobody asked for.
func TestStateCacheOffByDefault(t *testing.T) {
	dir := t.TempDir()
	m := NewManager()
	if err := m.WriteState(oneResourceState(t)); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("cache disabled but %d file(s) appeared: %v", len(entries), entries)
	}
}

// TestStateCacheWritesAndRoundTrips is the load-bearing guard. The defect this
// exists to prevent is a cache that is configured, reports success, and writes
// nothing - which is indistinguishable from a working cache in every log line,
// and is precisely how a plan came to rebuild prior state from live reads on
// every run while the docs said a state file "becomes a cache".
func TestStateCacheWritesAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "terraform.tfstate")
	m := NewManager()
	m.EnableStateCache(path)

	want := oneResourceState(t)
	if err := m.WriteState(want); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %v", err)
	}
	if diags := m.CacheWarning(); diags.HasErrors() || len(diags) != 0 {
		t.Fatalf("cache write warned: %s", diags.Err())
	}

	// It must be a real statefile, readable by the ordinary reader - not a
	// file of the right name with the wrong contents.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	defer f.Close()
	sf, err := statefile.Read(f, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("cache is not a readable statefile: %v", err)
	}
	if sf.State == nil {
		t.Fatal("cache holds no state")
	}
	got := sf.State.RootModule().Resources
	if len(got) != 1 {
		t.Fatalf("cache holds %d managed resource(s), want 1", len(got))
	}
	if _, ok := got["aws_iam_role.example"]; !ok {
		t.Errorf("cache lost the resource; holds %v", got)
	}
}

// TestStateCacheRewriteIsAtomic proves a second write replaces the first
// cleanly and leaves no temporary file behind. An interrupted write must leave
// the previous cache intact rather than a truncated one, which is why the
// writer renames rather than truncating in place.
func TestStateCacheRewriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfstate")
	m := NewManager()
	m.EnableStateCache(path)

	for i := 0; i < 3; i++ {
		if err := m.WriteState(oneResourceState(t)); err != nil {
			t.Fatalf("WriteState: %v", err)
		}
		if err := m.PersistState(context.Background(), nil); err != nil {
			t.Fatalf("PersistState: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("after 3 writes the directory holds %d entries %v, want exactly the cache file", len(entries), names)
	}
}

// TestStateCacheFailureIsAWarningNotAnError holds PersistState's contract. A
// cache that could fail an apply would not be a cache, so an unwritable path
// must warn and return nil.
func TestStateCacheFailureIsAWarningNotAnError(t *testing.T) {
	// A path whose parent is a FILE, so MkdirAll cannot create it.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	m := NewManager()
	m.EnableStateCache(filepath.Join(blocker, "nested", "terraform.tfstate"))

	if err := m.WriteState(oneResourceState(t)); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState returned an error for a cache failure: %v", err)
	}
	if len(m.CacheWarning()) == 0 {
		t.Error("an unwritable cache path produced no warning, so the failure is invisible")
	}
}
