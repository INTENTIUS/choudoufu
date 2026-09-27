// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
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

	args := []string{"-C", root, "grep", "-Il", "-E", flociDigestRef.String(), "--", "."}
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
			matched[f] = true
			b, rerr := os.ReadFile(filepath.Join(root, f))
			if rerr != nil {
				t.Errorf("reading %s: %v", f, rerr)
				continue
			}
			for _, m := range flociDigestRef.FindAllString(string(b), -1) {
				if m != pin {
					t.Errorf("%s pins %s, but live/floci-image pins %s.\n"+
						"Point it at the pin (or generate it from live/floci-image), or add its path "+
						"to flociLiteralExceptions with why it legitimately differs.", f, m, pin)
				}
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
