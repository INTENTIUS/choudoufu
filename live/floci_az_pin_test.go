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

// The Azure emulator's pin (#1120 unit 7). live/floci-az-image holds
// lex00/floci-az by index digest, the role live/floci-image plays for AWS
// and live/kind-node-image for Kubernetes, and these guards hold it the way
// floci_literal_pin_test.go and kind_pin_test.go hold those two: the pin is
// a digest reference to the fork's repository and nothing else, the harness
// reads it rather than carrying a copy, and every full floci-az digest
// anywhere in the tree is the pin.
//
// The fork publishes ghcr.io/lex00/floci-az:latest and :<short-sha> on every
// push to its main (its .github/workflows/ghcr-publish.yml), so a tag here
// would move under a run with nothing in this repository changing. A tag
// reference is refused for that reason, wherever it appears.

// flociAzPinPattern is the whole shape of live/floci-az-image.
var flociAzPinPattern = regexp.MustCompile(`^ghcr\.io/lex00/floci-az@sha256:[0-9a-f]{64}$`)

// flociAzDigestRef is a full floci-az digest reference; flociAzTagRef is a
// tag reference to the same repository (":latest", ":836d528").
var (
	flociAzDigestRef = regexp.MustCompile(`ghcr\.io/lex00/floci-az@sha256:[0-9a-f]{64}`)
	flociAzTagRef    = regexp.MustCompile(`ghcr\.io/lex00/floci-az:[A-Za-z0-9_][A-Za-z0-9_.-]*`)
	flociAzMention   = regexp.MustCompile(`(?i)floci-az`)
)

func flociAzPinRef(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("floci-az-image")
	if err != nil {
		t.Fatalf("reading live/floci-az-image: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// TestFlociAzImagePinIsADigestReference is the shape check.
func TestFlociAzImagePinIsADigestReference(t *testing.T) {
	pin := flociAzPinRef(t)
	if !flociAzPinPattern.MatchString(pin) {
		t.Errorf("live/floci-az-image is %q; it must be exactly ghcr.io/lex00/floci-az@sha256:<64 hex>, "+
			"a digest and no tag, because the fork republishes :latest on every push to its main", pin)
	}
}

// flociAzDigestFindings reports every floci-az image literal in one file
// that is not the pin: a full digest reference that differs, any tag
// reference, and a bare 64-hex digest on a line that names floci-az. A
// bare digest that is live/floci-image's own pin, or part of a full
// reference to any image (judged above, by the AWS guard, or by its own), is left alone.
func flociAzDigestFindings(path, src, pin, flociPinDigest string) []string {
	_, pinDigest, _ := strings.Cut(pin, "@")
	var out []string
	for n, line := range strings.Split(src, "\n") {
		for _, m := range flociAzDigestRef.FindAllString(line, -1) {
			if m != pin {
				out = append(out, fmt.Sprintf("%s:%d pins %s, but live/floci-az-image pins %s.\n"+
					"Point it at the pin, or read it from live/floci-az-image.", path, n+1, m, pin))
			}
		}
		for _, m := range flociAzTagRef.FindAllString(line, -1) {
			out = append(out, fmt.Sprintf("%s:%d names the floci-az image by tag (%s); the fork republishes its tags "+
				"on every push, so name it by digest, the pin in live/floci-az-image.", path, n+1, m))
		}
		if !flociAzMention.MatchString(line) {
			continue
		}
		for _, d := range bareDigest.FindAllString(line, -1) {
			// A digest inside any full image reference (`<image>@sha256:...`)
			// is not bare: a table row naming floci-az can also carry the
			// kind node image's pin in another cell (live/SUBSTRATES.md's
			// harness row), and that reference is its own guard's to judge.
			if d == pinDigest || d == flociPinDigest || strings.Contains(line, "@"+d) {
				continue
			}
			out = append(out, fmt.Sprintf("%s:%d attributes the bare digest %s to floci-az, but live/floci-az-image pins %s.\n"+
				"A record of a past run quotes the digest truncated; a full digest beside floci-az reads as the pin.",
				path, n+1, d, pinDigest))
		}
	}
	return out
}

// flociAzLiteralExceptions lists paths allowed to carry a floci-az image
// literal that is not the pin, with why. Empty is the intended state; the
// shape is flociLiteralExceptions'.
var flociAzLiteralExceptions = map[string]string{
	"live/floci_az_pin_test.go": "this file: its red fixtures are tag references and stale digests on purpose",

	"live/gauntlet.json": "a measured artifact: each Azure row's substrate_image is the digest that run measured " +
		"against, evidence about the run, and is never hand-edited (CLAUDE.md, measured artifacts). A row whose " +
		"image is not the pin is rendered stale (tools/gauntlet's floci-az row check) until the maintainer re-runs it",

	"live/gauntlet-board.json": "the board data `go run ./tools/gauntlet render` derives from live/gauntlet.json; " +
		"it quotes each row's measured image and says Stale beside it when that is not the pin",
}

// TestFlociAzPinLiteralsMatchThePin is the tree-wide guard.
func TestFlociAzPinLiteralsMatchThePin(t *testing.T) {
	root := repoRoot(t)
	pin := flociAzPinRef(t)
	_, flociPinDigest, _ := strings.Cut(flociPinRef(t), "@")

	args := []string{"-C", root, "grep", "-Il", "-i", "-E", `floci-az`, "--", "."}
	for path := range flociAzLiteralExceptions {
		args = append(args, ":!"+path)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("git grep: %v", err)
		}
	}
	matched := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f == "" {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(root, f))
		if rerr != nil {
			t.Errorf("reading %s: %v", f, rerr)
			continue
		}
		if flociAzDigestRef.Match(b) {
			matched[f] = true
		}
		for _, finding := range flociAzDigestFindings(f, string(b), pin, flociPinDigest) {
			t.Error(finding)
		}
	}
	// Reach: the pin file always carries a full reference, so a scan that
	// does not find it has stopped reading the tree.
	if !matched["live/floci-az-image"] {
		t.Errorf("the scan did not find live/floci-az-image among the files carrying a floci-az digest; the " +
			"git grep or flociAzDigestRef has stopped recognising the pin, so every other file is falling through unread")
	}
}

// TestFlociAzLiteralExceptionsStillExist is the exceptions' reverse check.
func TestFlociAzLiteralExceptionsStillExist(t *testing.T) {
	root := repoRoot(t)
	for path, why := range flociAzLiteralExceptions {
		if _, err := os.Stat(filepath.Join(root, strings.TrimSuffix(path, "/"))); err != nil {
			t.Errorf("flociAzLiteralExceptions exempts %q (%s) but it no longer exists: %v", path, why, err)
		}
	}
}

// TestFlociAzPinHasItsReaders holds the harness to reading the pin: lib.sh
// exports FLOCI_AZ_IMAGE from live/floci-az-image, and the compose service
// takes its image from that variable and from nowhere else. A pin no
// harness reads is a file that can drift from what runs.
func TestFlociAzPinHasItsReaders(t *testing.T) {
	lib, err := os.ReadFile(filepath.Join("smoke", "lib.sh"))
	if err != nil {
		t.Fatalf("reading live/smoke/lib.sh: %v", err)
	}
	reader := regexp.MustCompile(`(?m)^export FLOCI_AZ_IMAGE="\$\{FLOCI_AZ_IMAGE:-\$\(cat "\$ROOT/live/floci-az-image"\)\}"$`)
	if !reader.Match(lib) {
		t.Errorf("live/smoke/lib.sh no longer exports FLOCI_AZ_IMAGE from live/floci-az-image")
	}
	compose, err := os.ReadFile(filepath.Join("smoke", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("reading live/smoke/docker-compose.yml: %v", err)
	}
	if !strings.Contains(string(compose), "image: ${FLOCI_AZ_IMAGE:?") {
		t.Errorf("live/smoke/docker-compose.yml's floci-az service does not take its image from FLOCI_AZ_IMAGE")
	}
}

// TestFlociAzDigestFindingsIsRed keeps the rule provably red on each form
// it exists for, and green on the pin and on the forms it must leave to
// the AWS guard.
func TestFlociAzDigestFindingsIsRed(t *testing.T) {
	pin := flociAzPinRef(t)
	_, pinDigest, _ := strings.Cut(pin, "@")
	_, flociPinDigest, _ := strings.Cut(flociPinRef(t), "@")
	const stale = "sha256:a39185cc3971d0188663d61043cb038dff1260d8a975b1aa72c4e2bb1feac3cb"
	if stale == pinDigest {
		t.Fatal("the fixture's stale digest is the current pin; pick another")
	}
	red := []string{
		"image: ghcr.io/lex00/floci-az@" + stale,
		"image: ghcr.io/lex00/floci-az:latest",
		"docker run ghcr.io/lex00/floci-az:836d528",
		"| emulator | floci-az `" + stale + "` |",
	}
	for _, line := range red {
		got := flociAzDigestFindings("fixture", line, pin, flociPinDigest)
		if len(got) != 1 {
			t.Errorf("want one finding for %q, got %v", line, got)
			continue
		}
		t.Logf("red, as it must be: %s", got[0])
	}
	green := []string{
		pin,
		"image: ${FLOCI_AZ_IMAGE:?lib.sh exports this from live/floci-az-image}",
		"| emulator | floci-az `" + pinDigest + "` |",
		"floci-az `sha256:d1efbcc9...`, truncated in prose",
		"floci and floci-az: ghcr.io/lex00/floci@" + stale, // the AWS guard's to judge
		"floci-az runs beside floci `" + flociPinDigest + "`",
		"see lex00/floci-az#2",
	}
	for _, line := range green {
		if got := flociAzDigestFindings("fixture", line, pin, flociPinDigest); len(got) != 0 {
			t.Errorf("finding for %q: %v", line, got)
		}
	}
}
