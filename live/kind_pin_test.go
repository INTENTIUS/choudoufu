// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// kindCreateClusterInvocation matches a REAL `kind create cluster`
// invocation, as opposed to a log line or error message that merely quotes
// the same words (every script that creates a cluster also prints a line
// like "cluster: kind create cluster --name $CLUSTER" before doing it, and
// a diagnostic like "kind create cluster %s failed" after). Every real
// call in this repository names both the cluster and its kubeconfig on the
// same command, which no such string does - the log/error lines above are
// missing --kubeconfig entirely.
var kindCreateClusterInvocation = regexp.MustCompile(`kind create cluster\b[^\n]*--name\b[^\n]*--kubeconfig\b`)

// joinBackslashContinuations collapses a shell line continuation
// ("...\\\n    ...") into a single logical line, so a multi-line
// invocation such as live/smoke/lib.sh's
//
//	logged kind cluster "kind create cluster failed" \
//	  -- kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG" --wait 120s
//
// is matched as the one command it actually is, instead of being split
// across two lines neither of which contains the whole invocation.
func joinBackslashContinuations(src string) string {
	return strings.ReplaceAll(src, "\\\n", " ")
}

// TestNoKindClusterIsCreatedFromAnUnpinnedImage is issue #1594's guard. A
// bare `kind create cluster` launches whatever node image the kind binary
// on PATH happens to default to, and that default moves across kind
// releases - #1594's own evidence is the same commit measuring kind
// v1.34.0 for three Kubernetes estates and v1.37.0 for cert-manager, purely
// because of which kind happened to run each script. Every real invocation
// must instead pass --image, sourced from the single pin file
// live/kind-node-image: gauntlet_kind_node_image (live/e2e/lib/gauntlet.sh)
// and the KIND_NODE_IMAGE variable (live/smoke/lib.sh and
// examples/record-store-cluster/selftest.sh) both read it, and this test
// does not care how a script gets the value to the command line - only
// that --image is on the same invocation.
//
// Scripts are discovered by content, not a hand-maintained list of the
// three call sites #1594 found - the same reason
// gauntletCrossingScriptsThatDeclareAWS (pins_drift_test.go) scans
// e2e/*/run.sh by content rather than by name. A fourth script that starts
// creating a kind cluster without --image must fail here on its own,
// never depend on someone remembering to add it to a list.
//
// Proving it red: revert the --image argument from any of the three known
// call sites (or run this against the pre-#1594 tree) and it fails, naming
// the file and the unpinned invocation.
func TestNoKindClusterIsCreatedFromAnUnpinnedImage(t *testing.T) {
	const root = ".." // this package's tests run with cwd=live/; the repo root is one level up.
	var checked []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".corpus", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".sh") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // fixed extension, walked from a fixed root inside the checkout
		if err != nil {
			return err
		}
		src := string(data)
		if !strings.Contains(src, "kind create cluster") {
			return nil
		}
		checked = append(checked, path)
		for _, line := range strings.Split(joinBackslashContinuations(src), "\n") {
			if !kindCreateClusterInvocation.MatchString(line) {
				continue
			}
			if !strings.Contains(line, "--image") {
				t.Errorf("%s creates a kind cluster with no --image, so it launches whatever node image the kind binary on PATH happens to default to (issue #1594): %s",
					path, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s for *.sh files: %v", root, err)
	}
	if len(checked) == 0 {
		t.Fatal("no .sh file under the repository mentions \"kind create cluster\" - this guard would silently check nothing")
	}
	t.Logf("checked %d script(s) that mention kind create cluster: %v", len(checked), checked)
}

// TestKindNodeImagePinLooksLikeADigestReference is a shape check on
// live/kind-node-image (#1594), the same role emulatorPin's own file check
// plays for live/floci-image: kind's own release notes are explicit that
// an unpinned node image tag is not reproducible ("you must use the
// @sha256 digest to guarantee an image built for this release"), so a
// bare tag here would silently reopen the defect this pin exists to close.
func TestKindNodeImagePinLooksLikeADigestReference(t *testing.T) {
	b, err := os.ReadFile("kind-node-image")
	if err != nil {
		t.Fatalf("reading live/kind-node-image: %v", err)
	}
	pin := strings.TrimSpace(string(b))
	if pin == "" {
		t.Fatal("live/kind-node-image is empty")
	}
	if !strings.Contains(pin, "@sha256:") {
		t.Errorf("live/kind-node-image is %q, which carries no @sha256 digest - kind's own release notes require one to guarantee a reproducible image", pin)
	}
}

// kindVersionPattern is the shape a kind release tag takes (v0.33.0), the
// same style check oracle-versions.json's own aws_provider_version pin
// gets in TestGauntletCrossingScriptsPinOneAWSProvider.
var kindVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// TestKindVersionPinLooksLikeARelease guards live/kind-version, the single
// place #1594 asks every workflow that installs kind via helm/kind-action
// to read its `version:` input from, instead of the three copies of the
// literal `v0.33.0` that used to disagree only by luck.
func TestKindVersionPinLooksLikeARelease(t *testing.T) {
	b, err := os.ReadFile("kind-version")
	if err != nil {
		t.Fatalf("reading live/kind-version: %v", err)
	}
	pin := strings.TrimSpace(string(b))
	if !kindVersionPattern.MatchString(pin) {
		t.Errorf("live/kind-version is %q, which is not a bare vX.Y.Z release", pin)
	}
}

// TestWorkflowsReadTheKindVersionPin guards the other half of #1594's "one
// place for the kind binary version" scope: no workflow may carry its own
// copy of the release string helm/kind-action installs. Every job reads
// live/kind-version into a step output first (the same shape ci.yml,
// gauntlet.yml and k8s-smoke.yml already use for
// live/oracle-versions.json's terraform_version) and passes that output to
// kind-action's `version:` input, so a bump only ever touches the pin
// file.
func TestWorkflowsReadTheKindVersionPin(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil {
		t.Fatalf("globbing ../.github/workflows/*.yml: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no workflow files found - this guard would silently check nothing")
	}
	kindActionUse := regexp.MustCompile(`uses:\s*helm/kind-action@`)
	// Anchored to "version:" as the whole (trimmed) key, so it never
	// matches the same job's unrelated kubectl_version: v1.34.0 line.
	literalVersion := regexp.MustCompile(`^version:\s*v[0-9]+\.[0-9]+\.[0-9]+\s*$`)
	found := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		src := string(data)
		if !kindActionUse.MatchString(src) {
			continue
		}
		found++
		if !strings.Contains(src, "cat live/kind-version") {
			t.Errorf("%s uses helm/kind-action but never reads live/kind-version - it must read the one pinned place, not a literal", f)
		}
		for _, line := range strings.Split(src, "\n") {
			if literalVersion.MatchString(strings.TrimSpace(line)) {
				t.Errorf("%s: %q looks like a hardcoded kind version literal rather than a read of live/kind-version", f, strings.TrimSpace(line))
			}
		}
	}
	if found == 0 {
		t.Fatal("no workflow uses helm/kind-action - this guard would silently check nothing")
	}
	t.Logf("checked %d workflow file(s) using helm/kind-action", found)
}
