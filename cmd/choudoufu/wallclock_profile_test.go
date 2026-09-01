package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProfileRateFallsBackRatherThanDisabling pins the one decision in
// profileRate that can fail silently. SetBlockProfileRate(0) disables the
// profile entirely, so parsing a bad value to 0 would produce an empty profile
// that is indistinguishable from a run with nothing to report - the exact
// confusion these knobs exist to remove.
func TestProfileRateFallsBackRatherThanDisabling(t *testing.T) {
	const env = "CHOUDOUFU_TEST_RATE"
	for _, tc := range []struct {
		name string
		set  bool
		val  string
		want int
	}{
		{name: "unset", set: false, want: 1},
		{name: "empty", set: true, val: "", want: 1},
		{name: "valid", set: true, val: "7", want: 7},
		{name: "unparseable", set: true, val: "banana", want: 1},
		{name: "zero disables, so must fall back", set: true, val: "0", want: 1},
		{name: "negative", set: true, val: "-5", want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Unsetenv(env)
			if tc.set {
				t.Setenv(env, tc.val)
			}
			if got := profileRate(env, 1); got != tc.want {
				t.Errorf("profileRate(%q=%q) = %d, want %d", env, tc.val, got, tc.want)
			}
		})
	}
}

// TestWriteProfileToWritesSomething proves the writer actually produces a
// non-empty profile and surfaces failures. A profile that silently fails to
// write looks exactly like one with no samples, which is the confusion these
// knobs exist to remove.
func TestWriteProfileToWritesSomething(t *testing.T) {
	dir := t.TempDir()

	// "goroutine" is always registered and always contains at least the
	// goroutine running this test, so an empty result means the writer is
	// broken rather than that the program was idle.
	path := filepath.Join(dir, "g.out")
	if err := writeProfileTo("goroutine", path); err != nil {
		t.Fatalf("writeProfileTo(goroutine): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("no profile written: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("profile written but empty; a silent empty profile is the failure this guards")
	}

	// An unregistered name must error and must not leave a file behind.
	missing := filepath.Join(dir, "nope.out")
	if err := writeProfileTo("no-such-profile-exists", missing); err == nil {
		t.Error("unregistered profile name returned no error")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("wrote a file for an unregistered profile name")
	}

	// An unwritable path must error rather than be swallowed.
	if err := writeProfileTo("goroutine", filepath.Join(dir, "no-such-dir", "x.out")); err == nil {
		t.Error("unwritable path returned no error")
	}
}
