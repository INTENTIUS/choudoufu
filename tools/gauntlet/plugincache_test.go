package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #1300. HashiCorp documents the provider plugin cache as "not guaranteed to
// be concurrency safe... the provider installer's behavior in environments
// with multiple `terraform init` calls is undefined". Measured against the
// two binaries these scripts actually run:
//
//   - choudoufu/tofu serialize themselves. internal/providercache's
//     Dir.InstallPackage takes a per-(provider,version,platform) flock before
//     unpacking, so two tofu inits sharing a cache cannot interleave.
//   - real terraform (v1.15.8) takes no such lock and unpacks straight into
//     the final cache path - polling the cache during a cold init shows the
//     provider binary appear under its final name while it is still being
//     written, with no temp directory and no atomic rename. A concurrent init
//     that finds that path can link a half-written binary.
//
// Corruption was NOT reproduced (72 concurrent cold `terraform init` calls
// into one empty cache, diffed against a serially-built sha256 reference),
// so these two tests guard the STRUCTURAL property instead, which is what a
// thirteenth script would otherwise quietly break: the library is the only
// place a cache directory is chosen, and every script that shares the
// directory runs its real-terraform inits under the shared lock.

// pluginCacheAssignment matches a script choosing a cache directory for
// itself - the thing that must now come from live/e2e/lib/gauntlet.sh. It
// deliberately matches with or without `export`, and at any indentation.
var pluginCacheAssignment = regexp.MustCompile(`(^|\s)(export\s+)?TF_PLUGIN_CACHE_DIR=`)

// bareTerraformInit matches an invocation of the real `terraform` binary's
// init. It must not match `"$TOFU" init`, `$CHOU init`, a path ending in
// terraform, or an already-wrapped call, so the character class before the
// word excludes `$`, `"`, `/`, `-` and word characters.
var bareTerraformInit = regexp.MustCompile(`(^|[^-\w$"/])terraform\s+init\s`)

// sharesThePluginCache reports whether a script participates in the shared
// directory at all - either by exporting it (gauntlet_plugin_cache) or by
// reading it as a -plugin-dir mirror (gauntlet_plugin_cache_dir). A script
// that does neither gets no cache and has nothing to serialize against.
func sharesThePluginCache(body string) bool {
	return strings.Contains(body, "gauntlet_plugin_cache")
}

func e2eScripts(t *testing.T) (root string, paths []string) {
	t.Helper()
	root = testRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "live", "e2e", "*", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no live/e2e/*/run.sh scripts found - glob is broken")
	}
	return root, paths
}

// TestEveryScriptTakesItsPluginCacheFromTheLibrary: the cache directory is
// chosen in exactly one place, live/e2e/lib/gauntlet.sh, so the concurrency
// policy that comes with it cannot be opted out of by copy-paste. Before
// #1300 there were two spellings in twelve scripts and neither was reachable
// from the other.
//
// Proven red on purpose: restoring
// `export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"`
// to any one script makes this fail and name that script and line.
func TestEveryScriptTakesItsPluginCacheFromTheLibrary(t *testing.T) {
	root, scripts := e2eScripts(t)
	var violations []string
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, s)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if pluginCacheAssignment.MatchString(line) {
				violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("scripts choosing their own TF_PLUGIN_CACHE_DIR instead of calling\n"+
			"gauntlet_plugin_cache (exports it) or gauntlet_plugin_cache_dir (path only) - #1300:\n%s",
			strings.Join(violations, "\n"))
	}
}

// TestSharedPluginCacheInitsAreLocked: a script that shares the directory
// runs every real-`terraform` init through gauntlet_locked_init. tofu and
// choudoufu inits are exempt and must stay exempt - internal/providercache
// locks those already, and wrapping them would serialize work that does not
// need it.
//
// Proven red on purpose: dropping the wrapper from any one terraform init in
// any sharing script makes this fail and name that script and line.
func TestSharedPluginCacheInitsAreLocked(t *testing.T) {
	root, scripts := e2eScripts(t)
	var violations []string
	sharing := 0
	for _, s := range scripts {
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		body := string(b)
		if !sharesThePluginCache(body) {
			continue
		}
		sharing++
		rel, err := filepath.Rel(root, s)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			// Per OCCURRENCE, not per line (#1314). Several scripts carry the
			// wrapper name inside the same line's fail message
			// (`fail "plain gauntlet_locked_init terraform init failed"`), and a
			// per-line Contains check let a genuinely unwrapped init on such a
			// line pass: proven by unwrapping reference-ec2-vpc's stage-1 init
			// and watching the old check stay green.
			for _, m := range bareTerraformInit.FindAllStringIndex(line, -1) {
				tf := m[0] + strings.Index(line[m[0]:m[1]], "terraform")
				if strings.HasSuffix(line[:tf], "gauntlet_locked_init ") {
					continue
				}
				violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
				break
			}
		}
	}
	// A glob that stopped matching, or a rename of the library call, would
	// otherwise leave this test passing over nothing at all.
	if sharing == 0 {
		t.Fatal("no script calls gauntlet_plugin_cache - either the library call was renamed or the glob is broken")
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("real-terraform inits in scripts sharing the plugin cache that do not take the\n"+
			"shared lock - wrap them in gauntlet_locked_init (#1300):\n%s", strings.Join(violations, "\n"))
	}
}
