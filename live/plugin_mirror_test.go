// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The provider mirror (#1550's second act). corpus-simpleinfra-dns does not
// use the shared plugin cache as a cache: it passes it to every init as
// -plugin-dir, a read-only filesystem MIRROR, because its two halves
// resolve hashicorp/aws from two different registries and neither half's
// lock file can satisfy the other. A mirror is only ever read, so something
// has to fill it.
//
// In the old serial gauntlet run that was an accident of ordering: an
// earlier estate's ordinary init had populated the cache before this one
// ran. One estate per job removed the accident, and run 35896010698's shard
// failed at its own stage-0 assertion on an empty runner.
// scripts/warm-plugin-cache.sh is the precondition made explicit; these
// guards hold it to the scripts that depend on it.

// mirrorConsumers are the crossing scripts that consume the shared cache as
// a -plugin-dir mirror rather than as a cache. Exactly one today. A second
// one is not forbidden - it just has to be warmed, which is what the test
// below says when it fails.
var mirrorConsumers = []string{"corpus-simpleinfra-dns"}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// shellWithoutComments drops whole-line shell comments, so a version quoted
// in a comment (this repository's scripts explain themselves at length) is
// never mistaken for a hard-coded one.
func shellWithoutComments(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// TestEveryPluginDirConsumerIsWarmed: a script that reads the mirror must be
// one the warmer knows about. A new -plugin-dir consumer that needs a
// provider or a registry the warmer does not install would fail on an empty
// runner thirty minutes into its own shard, which is where this started.
//
// Proving it red (run 2026-09-23): add `-plugin-dir="$MIRROR"` to any other
// estate script.
func TestEveryPluginDirConsumerIsWarmed(t *testing.T) {
	entries, err := os.ReadDir("e2e")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join("e2e", e.Name(), "run.sh")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(shellWithoutComments(string(b)), "-plugin-dir") {
			found = append(found, e.Name())
		}
	}
	sort.Strings(found)
	want := append([]string(nil), mirrorConsumers...)
	sort.Strings(want)
	if strings.Join(found, ",") != strings.Join(want, ",") {
		t.Errorf("estate scripts consuming the plugin cache as a -plugin-dir mirror are %v; this guard knows about %v. A mirror is read-only, so a new consumer has to be warmed: check what provider and registries it asserts, extend scripts/warm-plugin-cache.sh, and add it to mirrorConsumers.", found, want)
	}
	if len(found) == 0 {
		t.Fatal("no estate script uses -plugin-dir any more, so this guard is checking nothing")
	}
}

// TestTheWarmerInstallsWhatTheMirrorConsumerAsserts holds the warming script
// to the assertion each consumer actually makes: the same registries, the
// same provider. Both sides are read out of the files rather than retyped.
func TestTheWarmerInstallsWhatTheMirrorConsumerAsserts(t *testing.T) {
	warmer := readFileT(t, filepath.Join("..", "scripts", "warm-plugin-cache.sh"))
	warmCode := shellWithoutComments(warmer)

	// What the warmer installs: one warm_one call per registry.
	installs := map[string]bool{}
	for _, m := range regexp.MustCompile(`warm_one (\S+) (\S+) `).FindAllStringSubmatch(warmCode, -1) {
		installs[m[1]] = true
	}
	if len(installs) == 0 {
		t.Fatal("scripts/warm-plugin-cache.sh installs nothing this guard can see (it looks for `warm_one <registry> <binary>`); the guard, not the script, is what to fix if the script was reshaped")
	}

	// What each consumer asserts: `for reg in <registries>` around a
	// $MIRROR/$reg/<provider>/$VERSION existence check.
	forReg := regexp.MustCompile(`for reg in ([^;\n]+); do`)
	assertPath := regexp.MustCompile(`\[ -d "\$MIRROR/\$reg/([a-z0-9-]+/[a-z0-9-]+)/\$PROVIDER_VERSION" \]`)
	for _, name := range mirrorConsumers {
		script := readFileT(t, filepath.Join("e2e", name, "run.sh"))
		regs := forReg.FindStringSubmatch(script)
		prov := assertPath.FindStringSubmatch(script)
		if regs == nil || prov == nil {
			t.Fatalf("%s no longer asserts its mirror in the shape this guard reads (`for reg in ...` around `[ -d \"$MIRROR/$reg/<provider>/$PROVIDER_VERSION\" ]`); the assertion moved, so this guard is checking nothing", name)
		}
		for _, reg := range strings.Fields(regs[1]) {
			if !installs[reg] {
				t.Errorf("%s asserts the mirror holds %s, and scripts/warm-plugin-cache.sh installs only %v", name, reg, keysOf(installs))
			}
		}
		if prov[1] != "hashicorp/aws" {
			t.Errorf("%s asserts provider %s; the warmer installs hashicorp/aws only", name, prov[1])
		}
	}

	// And the version is read from the pin, never spelled out: a literal
	// here is a second place to bump and a silent way to warm the wrong
	// release.
	if !strings.Contains(warmCode, "gauntlet_aws_pin_version") {
		t.Error("scripts/warm-plugin-cache.sh does not read the pin through gauntlet_aws_pin_version, so it can disagree with what the crossing scripts assert")
	}
	if m := regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`).FindString(warmCode); m != "" {
		t.Errorf("scripts/warm-plugin-cache.sh carries the literal version %q outside a comment; the pin lives in live/oracle-versions.json", m)
	}
	// #1300's rule, which TestEveryScriptTakesItsPluginCacheFromTheLibrary
	// enforces for live/e2e and which this script is the first consumer of
	// outside that directory: the cache directory is chosen in one place.
	if regexp.MustCompile(`(?m)^\s*(export\s+)?TF_PLUGIN_CACHE_DIR=`).MatchString(warmCode) {
		t.Error("scripts/warm-plugin-cache.sh chooses its own TF_PLUGIN_CACHE_DIR instead of calling gauntlet_plugin_cache; the directory has one spelling (#1300)")
	}
}

// TestTheEstateJobWarmsBeforeItRuns: the workflow has to call the warmer,
// before the estate, and key its cache on the pin rather than on a literal.
//
// Proving it red (run 2026-09-23): delete the warm step, move it after
// "Run the estate", or replace the cache key's ${{ steps.oracle.outputs.aws }}
// with a version literal.
func TestTheEstateJobWarmsBeforeItRuns(t *testing.T) {
	wf := readFileT(t, filepath.Join("..", ".github", "workflows", "gauntlet.yml"))
	warm := strings.Index(wf, "bash scripts/warm-plugin-cache.sh")
	run := strings.Index(wf, "go run ./tools/gauntlet run \"${{ matrix.estate }}\"")
	if warm < 0 {
		t.Fatal("gauntlet.yml never runs scripts/warm-plugin-cache.sh; corpus-simpleinfra-dns's shard starts on an empty runner and fails at its own stage-0 mirror assertion")
	}
	if run < 0 {
		t.Fatal("gauntlet.yml has no per-estate run step; this guard is reading the wrong workflow")
	}
	if warm > run {
		t.Error("gauntlet.yml warms the provider mirror after it runs the estate")
	}
	if !strings.Contains(wf, "key: plugin-mirror-${{ runner.os }}-aws-${{ steps.oracle.outputs.aws }}") {
		t.Error("gauntlet.yml's provider-mirror cache is not keyed on the aws_provider_version the oracle step reads; a key that does not move with the pin restores the wrong release and the warm step then adds the right one to an entry nobody re-saves")
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
