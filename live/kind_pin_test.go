// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// shWord is one shell word, and whether any part of it was quoted: a log
// line such as `log "cluster: kind create cluster --name $C"` is one quoted
// word, never the three words kind, create and cluster.
type shWord struct {
	text   string
	quoted bool
}

// shellWordsQuoted splits one line of shell into words, honouring single
// and double quotes, stopping at an unquoted `#` that starts a word, and
// emitting ; && || | & as words of their own.
func shellWordsQuoted(line string) []shWord {
	var words []shWord
	var cur strings.Builder
	inWord, quoted := false, false
	flush := func() {
		if inWord {
			words = append(words, shWord{cur.String(), quoted})
			cur.Reset()
			inWord, quoted = false, false
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			inWord = true
		case c == '\'':
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				j = len(line) - i - 1
			}
			cur.WriteString(line[i+1 : i+1+j])
			inWord, quoted = true, true
			i += j + 1
		case c == '"':
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				cur.WriteByte(line[i])
				i++
			}
			inWord, quoted = true, true
		case c == ' ' || c == '\t':
			flush()
		case c == '#' && !inWord:
			flush()
			return words
		case c == ';' || c == '|' || c == '&':
			flush()
			op := string(c)
			if i+1 < len(line) && line[i+1] == c && c != ';' {
				op += string(c)
				i++
			}
			words = append(words, shWord{op, false})
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return words
}

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

// kindCreateFindings reads one file's text and reports every real
// `kind create cluster` invocation that does not take its node image from
// the pin. A real invocation is the unquoted words kind, create and cluster
// in that order (kind's own global flags, such as -q, may sit between kind
// and create), anywhere on a logical line and in any flag order after it; a
// log line or an error message that quotes the same words is one quoted
// word and cannot match. The invocation runs to the next ; && || | or &.
//
// It must carry --image (or --image=), and an image written as a literal -
// no $ in it - must be the pin itself: `--image kindest/node:v1.25.0` is as
// unpinned as no --image at all, it only looks deliberate. An image read
// from a variable or a command substitution is accepted; where the value
// comes from is TestKindNodeImageIsReadFromThePin's business below.
func kindCreateFindings(path, src, pin string) []string {
	var out []string
	for n, line := range strings.Split(joinBackslashContinuations(src), "\n") {
		ws := shellWordsQuoted(line)
		for i := 0; i < len(ws); i++ {
			if ws[i].quoted || ws[i].text != "kind" && !strings.HasSuffix(ws[i].text, "/kind") {
				continue
			}
			j := i + 1
			for j < len(ws) && !ws[j].quoted && strings.HasPrefix(ws[j].text, "-") {
				j++
			}
			if j+1 >= len(ws) || ws[j].quoted || ws[j].text != "create" || ws[j+1].quoted || ws[j+1].text != "cluster" {
				continue
			}
			var image string
			hasImage := false
			var args []string
			for k := j + 2; k < len(ws); k++ {
				w := ws[k].text
				if !ws[k].quoted && (w == ";" || w == "&&" || w == "||" || w == "|" || w == "&") {
					break
				}
				args = append(args, w)
				switch {
				case w == "--image" && k+1 < len(ws):
					hasImage, image = true, ws[k+1].text
				case strings.HasPrefix(w, "--image="):
					hasImage, image = true, strings.TrimPrefix(w, "--image=")
				}
			}
			cmd := "kind create cluster " + strings.Join(args, " ")
			switch {
			case !hasImage:
				out = append(out, fmt.Sprintf("%s:%d creates a kind cluster with no --image, so it launches whatever node image the kind binary on PATH happens to default to (issue #1594): %s", path, n+1, cmd))
			case !strings.Contains(image, "$") && image != pin:
				out = append(out, fmt.Sprintf("%s:%d creates a kind cluster from the hard-coded image %q, not the pin live/kind-node-image (%s) (issues #1594, #1741): %s", path, n+1, image, pin, cmd))
			}
		}
	}
	return out
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
// examples/record-store-cluster/selftest.sh) both read it.
//
// Scripts and workflows are discovered by content, not a hand-maintained
// list of the call sites #1594 found. #1741 widened it: the first version
// read only .sh lines carrying --name then --kubeconfig, so `kind create
// cluster --name foo --wait 60s`, the reversed flag order, and a hard-coded
// `--image kindest/node:v1.25.0` all passed, and a workflow's own run:
// block was never read. TestKindCreateFindingsIsRedOnIssue1741sForms feeds
// each of those to the rule.
func TestNoKindClusterIsCreatedFromAnUnpinnedImage(t *testing.T) {
	const root = ".." // this package's tests run with cwd=live/; the repo root is one level up.
	pin := kindNodeImagePin(t)
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
		isWorkflow := strings.HasSuffix(filepath.ToSlash(path), ".yml") && strings.Contains(filepath.ToSlash(path), ".github/workflows/")
		if !strings.HasSuffix(path, ".sh") && !isWorkflow {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // fixed extensions, walked from a fixed root inside the checkout
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "create cluster") {
			return nil
		}
		checked = append(checked, path)
		for _, f := range kindCreateFindings(path, string(data), pin) {
			t.Error(f)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s for *.sh and workflow files: %v", root, err)
	}
	if len(checked) < 4 {
		t.Fatalf("only %d file(s) under the repository mention \"create cluster\" (%v); the four known call sites are live/smoke/lib.sh, live/e2e/lib/gauntlet.sh, examples/record-store-cluster/selftest.sh and kind-tier.yml, so this guard has stopped reading them", len(checked), checked)
	}
	t.Logf("checked %d file(s) that mention create cluster: %v", len(checked), checked)
}

// TestKindCreateFindingsIsRedOnIssue1741sForms keeps the rule provably red
// on each form #1741 proved green against the first version, and green on
// the forms this repository actually uses.
func TestKindCreateFindingsIsRedOnIssue1741sForms(t *testing.T) {
	pin := kindNodeImagePin(t)
	red := []string{
		`kind create cluster --name foo --wait 60s`,
		`kind create cluster --kubeconfig "$KUBECONFIG" --name foo`,
		`kind create cluster --image kindest/node:v1.25.0 --name x --kubeconfig "$K"`,
		`kind create cluster --image=kindest/node:v1.25.0`,
		`kind -q create cluster --name x`,
		`if true; then kind create cluster; fi`,
		"kind create cluster \\\n  --name x --kubeconfig k",
		`/usr/local/bin/kind create cluster --name x`,
	}
	for _, src := range red {
		got := kindCreateFindings("fixture.sh", src, pin)
		if len(got) == 0 {
			t.Errorf("no finding for %q", src)
			continue
		}
		t.Logf("red, as it must be: %s", got[0])
	}
	green := []string{
		`kind create cluster --image "$KIND_NODE_IMAGE" --name "$CLUSTER" --kubeconfig "$KUBECONFIG" --wait 120s`,
		`kind create cluster --name kind-tier --image "$(cat live/kind-node-image)" --kubeconfig "$KUBECONFIG" --wait 120s`,
		`kind create cluster --image ` + pin + ` --name x`,
		`log "cluster: kind create cluster --name $CLUSTER"`,
		`printf 'gauntlet_kind_up: kind create cluster %s failed:\n' "$name" >&2`,
		`# a bare kind create cluster uses whatever node image`,
		`verdict_fail "kind create cluster failed"`,
		`kind create cluster --image "$image" --name "$name" --kubeconfig "$cfg" --wait 120s >"${cfg}.kind.log" 2>&1 || { printf 'kind create cluster %s failed' "$name"; }`,
	}
	for _, src := range green {
		if got := kindCreateFindings("fixture.sh", src, pin); len(got) != 0 {
			t.Errorf("finding for a pinned or non-invocation form %q: %v", src, got)
		}
	}
}

func kindNodeImagePin(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("kind-node-image")
	if err != nil {
		t.Fatalf("reading live/kind-node-image: %v", err)
	}
	return strings.TrimSpace(string(b))
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

// kubectl's version skew policy supports a client within one minor version
// of the API server. #1741: kubectl_version was a literal v1.34.0 copied
// into four workflow steps, outside the pin files, three minors behind the
// v1.37 node live/kind-node-image pins. It now lives in live/kubectl-version
// beside the node pin, and the two tests below hold it to the node and hold
// every workflow to the file.
var kubeMinorPattern = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.[0-9]+`)

// kubectlSkewFinding is "" when kubectl (vX.Y.Z) is within one minor of the
// node image's Kubernetes version (kindest/node:vX.Y.Z@sha256:...).
func kubectlSkewFinding(kubectl, nodeImage string) string {
	k := kubeMinorPattern.FindStringSubmatch(kubectl)
	if k == nil || !kindVersionPattern.MatchString(kubectl) {
		return fmt.Sprintf("live/kubectl-version is %q, which is not a bare vX.Y.Z release", kubectl)
	}
	_, tag, _ := strings.Cut(strings.SplitN(nodeImage, "@", 2)[0], ":")
	n := kubeMinorPattern.FindStringSubmatch(tag)
	if n == nil {
		return fmt.Sprintf("live/kind-node-image is %q, whose tag carries no vX.Y.Z the skew can be read from", nodeImage)
	}
	km, _ := strconv.Atoi(k[2])
	nm, _ := strconv.Atoi(n[2])
	if k[1] != n[1] || km-nm > 1 || nm-km > 1 {
		return fmt.Sprintf("kubectl %s is out of kubectl's supported skew (+/-1 minor) against the node image's Kubernetes %s (live/kind-node-image); bump live/kubectl-version (#1741)", kubectl, tag)
	}
	return ""
}

func TestKubectlVersionPinIsWithinOneMinorOfTheNode(t *testing.T) {
	b, err := os.ReadFile("kubectl-version")
	if err != nil {
		t.Fatalf("reading live/kubectl-version: %v (#1741: kubectl's version lives beside the node pin, not in each workflow)", err)
	}
	if f := kubectlSkewFinding(strings.TrimSpace(string(b)), kindNodeImagePin(t)); f != "" {
		t.Error(f)
	}
}

// TestKubectlSkewFindingIsRedOnIssue1741sValue is #1741's own case: the
// v1.34.0 the workflows carried against a v1.37.0 node.
func TestKubectlSkewFindingIsRedOnIssue1741sValue(t *testing.T) {
	const node = "kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5"
	f := kubectlSkewFinding("v1.34.0", node)
	if f == "" {
		t.Fatal("kubectl v1.34.0 against a v1.37.0 node produced no finding")
	}
	t.Logf("red, as it must be: %s", f)
	for _, ok := range []string{"v1.36.4", "v1.37.0", "v1.38.1"} {
		if f := kubectlSkewFinding(ok, node); f != "" {
			t.Errorf("%s against v1.37.0: %s", ok, f)
		}
	}
	for _, bad := range []string{"v1.39.0", "v2.37.0", "1.37.0", "v1.37"} {
		if kubectlSkewFinding(bad, node) == "" {
			t.Errorf("%s against v1.37.0 produced no finding", bad)
		}
	}
}

// TestWorkflowsReadTheKubectlVersionPin: every helm/kind-action step takes
// kubectl_version from a step output, and that step in the same job reads
// live/kubectl-version into that output. Read as YAML, so a literal left in
// any one job, or a comment naming the file, cannot pass.
func TestWorkflowsReadTheKubectlVersionPin(t *testing.T) {
	files, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range kubectlPinFindings(filepath.Base(f), raw, &found) {
			t.Error(finding)
		}
	}
	if found == 0 {
		t.Fatal("no workflow step uses helm/kind-action - this guard would silently check nothing")
	}
	t.Logf("checked %d helm/kind-action step(s)", found)
}

var stepOutputRef = regexp.MustCompile(`^\$\{\{\s*steps\.([A-Za-z0-9_-]+)\.outputs\.([A-Za-z0-9_-]+)\s*\}\}$`)

func kubectlPinFindings(name string, raw []byte, found *int) []string {
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				ID   string `yaml:"id"`
				Uses string
				Run  string
				With map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		return []string{fmt.Sprintf("%s does not parse: %v", name, err)}
	}
	var out []string
	for jn, job := range wf.Jobs {
		for _, st := range job.Steps {
			if !strings.HasPrefix(st.Uses, "helm/kind-action@") {
				continue
			}
			*found++
			v := st.With["kubectl_version"]
			m := stepOutputRef.FindStringSubmatch(strings.TrimSpace(v))
			if m == nil {
				out = append(out, fmt.Sprintf("%s job %s: helm/kind-action's kubectl_version is %q, not a step output read from live/kubectl-version (#1741)", name, jn, v))
				continue
			}
			ok := false
			for _, src := range job.Steps {
				if src.ID == m[1] && strings.Contains(src.Run, "cat live/kubectl-version") && strings.Contains(src.Run, m[2]+"=") {
					ok = true
				}
			}
			if !ok {
				out = append(out, fmt.Sprintf("%s job %s: kubectl_version reads steps.%s.outputs.%s, but no step %q in that job writes %s= from live/kubectl-version", name, jn, m[1], m[2], m[1], m[2]))
			}
		}
	}
	sort.Strings(out)
	return out
}
