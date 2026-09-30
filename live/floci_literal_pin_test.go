// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #1595. flociimage_test.go's two guards above cover the artifacts
// under live/'s own top level that record which image they were measured
// against. Nothing covered a plain operational literal: a workflow or
// compose file that names ghcr.io/lex00/floci@sha256:... directly, to pull
// and run the image, rather than to record a measurement.
//
// Two of those had drifted from live/floci-image with nothing failing:
// .github/workflows/ci-pipelines-smoke.yml and
// examples/ci-pipelines/e2e/gitlab/docker-compose.yml both still named the
// pin two repins back, and examples/live-mv-workbench/compose/docker-compose.yml's
// FLOCI_IMAGE default was a third, older digest still. The workflow file
// even carries its own runtime check ("the emulator is the pin" step) that
// would have failed on its next workflow_dispatch - this guard catches the
// same drift on every push instead, without dispatching anything.
//
// flociDigestRef is deliberately exactly 64 hex characters after sha256:,
// a full digest and nothing shorter. Prose in this repository routinely
// quotes a digest truncated ("sha256:1362e856...") or a deliberately fake
// one in a test fixture ("sha256:aaa", "sha256:deadbeef") to illustrate a
// point without claiming to be a live pin; neither can match this pattern,
// so neither needs an entry in flociLiteralExceptions.
var flociDigestRef = regexp.MustCompile(`ghcr\.io/lex00/floci@sha256:[0-9a-f]{64}`)

// bareDigest is a full sha256 digest with no repository in front of it.
// #1741: examples/live-mv-workbench/README.md named
// `sha256:a39185cc...` as "the digest live/floci-image pins" while the pin
// had moved on to 6c3d5c2d, and flociDigestRef, which needs the
// ghcr.io/lex00/floci@ prefix, never saw it. A bare digest on a line that
// names floci is attributed to floci, and is held to the pin the same way;
// the same 64-hex rule keeps truncated prose digests out of it.
var bareDigest = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// flociMention is how a line attributes a digest to the emulator.
var flociMention = regexp.MustCompile(`(?i)floci`)

// flociDigestFindings reports every floci digest literal in one file that
// is not the pin: a full ghcr.io/lex00/floci@sha256 reference anywhere, and
// a bare sha256 digest on any line that names floci.
func flociDigestFindings(path, src, pin string) []string {
	_, pinDigest, _ := strings.Cut(pin, "@")
	var out []string
	for n, line := range strings.Split(src, "\n") {
		for _, m := range flociDigestRef.FindAllString(line, -1) {
			if m != pin {
				out = append(out, fmt.Sprintf("%s:%d pins %s, but live/floci-image pins %s.\n"+
					"Point it at the pin (or generate it from live/floci-image), or add its path "+
					"to flociLiteralExceptions with why it legitimately differs.", path, n+1, m, pin))
			}
		}
		if !flociMention.MatchString(line) {
			continue
		}
		for _, d := range bareDigest.FindAllString(line, -1) {
			if d == pinDigest || strings.Contains(line, "floci@"+d) {
				continue // the pin, or a full reference already judged above
			}
			out = append(out, fmt.Sprintf("%s:%d attributes the bare digest %s to floci, but live/floci-image pins %s (#1741).\n"+
				"A record of what a past run measured says so and quotes the digest truncated, the way this "+
				"repository's prose does; a full digest beside floci reads as the pin.", path, n+1, d, pinDigest))
		}
	}
	return out
}

// flociLiteralExceptions lists paths, relative to the repo root, allowed to
// carry a full floci digest literal that differs from live/floci-image,
// with why. A path ending in "/" is a git pathspec directory exclusion
// (everything under it is exempt); anything else is a single file.
//
// The live/ artifacts flociImageFields and multiRefArtifacts already name
// (flociimage_test.go, above) are exempted here too, dynamically, rather
// than repeated in this map: they are governed by the two guards above,
// and duplicating the judgment here would just be a second list to keep in
// sync with the first.
var flociLiteralExceptions = map[string]string{
	"CHANGELOG.md": "a changelog entry documents what a past repin moved from and to; " +
		"rewriting the old value to the current pin would make the entry describe a repin that never happened",

	"live/history/": "frozen release snapshots; each records the pin current at that release " +
		"and is never rewritten (cmdSnapshot byte-copies deliberately, per this repository's own " +
		"decision-authority guard on the same directory)",

	"live/e2e/terralith-scale/scale-history.json": "a frozen per-release measurement of the scale ladder, " +
		"same reason as live/history/",

	"live/e2e/estates/": "each estate README's docker run reproduction command is pinned to the digest " +
		"a specific finding was reproduced against, not to whatever the pin has since moved to",

	"live/e2e/corpus-sumaform-aws/run.sh": "comments cite the digest that exposed or fixed a specific " +
		"emulator behavior in a past run; the digest is part of the finding, not a pin to keep current",

	"live/e2e/corpus-vpc-complete/run.sh": "same reason as corpus-sumaform-aws/run.sh: comments cite the " +
		"digest a past finding was measured or fixed against",

	"site/data/": "published copies of the live/ measurement artifacts, written by `go run ./tools/gauntlet " +
		"render`; the artifacts themselves are what TestFlociMeasurementsMatchThePinOrSayWhyNot governs, " +
		"and this is their generated mirror",

	"tools/floci-capability-gen/digest_test.go": "a test fixture value for resolveDigest's already-pinned " +
		"fast path, unrelated to which image is actually pinned",

	"tools/gauntlet/scalerecord_test.go": "test fixture data copied verbatim from a past live/gauntlet.json " +
		"row, not a claim about the current pin",
}

// TestFlociPinLiteralsMatchTheFloatingPin is the guard: every full floci
// digest literal in the tree is either live/floci-image's own pin, or its
// path is listed above (or in flociImageFields/multiRefArtifacts) with why.
func TestFlociPinLiteralsMatchTheFloatingPin(t *testing.T) {
	root := repoRoot(t)
	pin := flociPinRef(t)

	args := []string{"-C", root, "grep", "-Il", "-E", bareDigest.String(), "--", "."}
	for path := range flociLiteralExceptions {
		args = append(args, ":!"+path)
	}
	for name := range flociImageFields {
		args = append(args, ":!live/"+name)
	}
	for name := range multiRefArtifacts {
		args = append(args, ":!live/"+name)
	}

	out, err := exec.Command("git", args...).Output()
	matched := map[string]bool{}
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("git grep: %v", err)
		}
		// git grep exits 1 when nothing matches; matched stays empty and
		// the reach check below catches that as the failure it is.
	} else {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f == "" {
				continue
			}
			b, rerr := os.ReadFile(filepath.Join(root, f))
			if rerr != nil {
				t.Errorf("reading %s: %v", f, rerr)
				continue
			}
			if flociDigestRef.Match(b) {
				matched[f] = true
			}
			for _, finding := range flociDigestFindings(f, string(b), pin) {
				t.Error(finding)
			}
		}
	}

	// The scan's own reach: live/floci-image carries the pin itself and is
	// never excluded above, so it must always come back matched. A pattern
	// or pathspec bug that stops finding real refs would otherwise pass
	// this test having read nothing, the same silent-green shape
	// TestEveryFlociImageRefIsAccountedFor guards against for the JSON
	// artifacts.
	if !matched["live/floci-image"] {
		t.Errorf("the scan did not find live/floci-image among the matches; flociDigestRef or the git grep " +
			"pathspec has stopped recognizing real floci digest literals, so every other file in the tree " +
			"is now falling through this guard unread")
	}
}

// TestFlociLiteralExceptionsStillExist is flociLiteralExceptions' own
// reverse check: an exception names a path so it can be skipped above, and
// a path that no longer exists excuses nothing while silently standing
// ready to excuse whatever new file takes its name.
func TestFlociLiteralExceptionsStillExist(t *testing.T) {
	root := repoRoot(t)
	for path, why := range flociLiteralExceptions {
		p := filepath.Join(root, strings.TrimSuffix(path, "/"))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("flociLiteralExceptions exempts %q (%s) but it no longer exists: %v\n"+
				"Drop the entry.", path, why, err)
		}
	}
}

// TestFlociDigestFindingsIsRedOnABareDigest keeps #1741's case provably red:
// the README line as it stood, a bare digest attributed to floci that is
// not the pin.
func TestFlociDigestFindingsIsRedOnABareDigest(t *testing.T) {
	pin := flociPinRef(t)
	_, pinDigest, _ := strings.Cut(pin, "@")
	const stale = "sha256:a39185cc3971d0188663d61043cb038dff1260d8a975b1aa72c4e2bb1feac3cb"
	if stale == pinDigest {
		t.Fatal("the fixture's stale digest is the current pin; pick another")
	}
	line := "| emulator | floci `" + stale + "`, the digest `live/floci-image` pins |"
	got := flociDigestFindings("examples/live-mv-workbench/README.md", line, pin)
	if len(got) != 1 {
		t.Fatalf("want one finding for %q, got %v", line, got)
	}
	t.Logf("red, as it must be: %s", got[0])

	for _, ok := range []string{
		"| emulator | floci `" + pinDigest + "`, the digest `live/floci-image` pins |",
		"| emulator | floci `sha256:a39185cc3971...`, the digest `live/floci-image` pinned at `60d0cdf63f` |",
		"image: " + pin,
		"kindest/node:v1.37.0@" + stale, // a digest on a line that never names floci
	} {
		if got := flociDigestFindings("x", ok, pin); len(got) != 0 {
			t.Errorf("finding for %q: %v", ok, got)
		}
	}
	if got := flociDigestFindings("x", "ghcr.io/lex00/floci@"+stale, pin); len(got) != 1 {
		t.Errorf("a full stale reference should be exactly one finding, not also a bare one: %v", got)
	}
}
