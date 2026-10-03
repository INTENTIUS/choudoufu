// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package e2etest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/intentius/choudoufu/internal/e2e"
	"github.com/intentius/choudoufu/internal/getproviders"
)

// TestLivePlanSet is GitHub issue #1752's end-to-end test of live-plan-set,
// run as the real binary against a fake provider registry that counts how
// many times the provider's package archive is downloaded. It proves:
//
//   - shared cache: three roots needing the same provider download its
//     package once, and the counter is shown able to see per-root installs
//     by the separate-runs arm below, which downloads it three times;
//   - failure isolation: a root whose init fails is reported with its
//     error and the other roots' plans are complete;
//   - exit codes 4, 2 and 0 from the real binary;
//   - equivalence: each root's resource_changes in the set's document are
//     the ones a separate init, live-plan -out and show -json of that root
//     give.
//
// The registry is plain HTTP on purpose: a "host" block in the CLI
// configuration points registry.opentofu.org's providers.v1 service at it
// without TLS, which is what lets this run on macOS, where a child process
// cannot be told to trust a test certificate. Nothing reaches the real
// registry. It has to be that hostname: it is the only one whose packages
// the installer accepts without a GPG signing key
// (getproviders.ShouldEnforceGPGValidationForProvider).
func TestLivePlanSet(t *testing.T) {
	if !canRunGoBuild {
		t.Skip("can't run without building a provider executable")
	}
	t.Parallel()

	reg := newCountingRegistry(t)
	defer reg.Close()

	tf := e2e.NewBinary(t, tofuBin, t.TempDir())
	cliConfig := tf.Path("cli.tfrc")
	writeFile(t, cliConfig, fmt.Sprintf(`
host "registry.opentofu.org" {
  services = {
    "providers.v1" = "%s/v1/providers/"
  }
}
`, reg.URL))
	tf.AddEnv("TF_CLI_CONFIG_FILE=" + cliConfig)
	// Whatever cache the developer's own environment names must not serve
	// this test, or the counter would read zero.
	tf.AddEnv("TF_PLUGIN_CACHE_DIR=")
	tf.AddEnv("TF_DATA_DIR=")

	root := func(name, estate, body string) string {
		live := ""
		if estate != "" {
			live = fmt.Sprintf("  live {\n    estate = %q\n  }\n", estate)
		}
		writeFile(t, tf.Path("roots", name, "main.tf"), fmt.Sprintf(`terraform {
%s  required_providers {
    simple6 = {
      source  = "hashicorp/simple6"
      version = "0.0.1"
    }
  }
}
%s`, live, body))
		return "roots/" + name
	}
	a := root("a", "set-a", `resource "terraform_data" "x" {
  input = "a"
}
`)
	b := root("b", "set-b", `resource "terraform_data" "x" {
  input = "b"
}
resource "terraform_data" "y" {
  input = "b2"
}
`)
	clean := root("clean", "set-clean", "")
	// Its provider does not exist in the registry, so its init fails.
	broken := "roots/broken"
	writeFile(t, tf.Path("roots", "broken", "main.tf"), `terraform {
  live {
    estate = "set-broken"
  }
  required_providers {
    missing = {
      source = "hashicorp/missing"
    }
  }
}
`)

	// ---- one set, one root broken: exit 4 ----
	doc, code, stderr := runSet(t, tf, "-parallel-estates=4", a, broken, b, clean)
	if code != 4 {
		t.Fatalf("exit %d with a broken root, want 4\nstderr:\n%s", code, stderr)
	}
	got := map[string]setRoot{}
	for _, r := range doc.Roots {
		got[r.Root] = r
	}
	if r := got[broken]; r.Status != "failed" || r.Stage != "init" || !strings.Contains(r.Error, "hashicorp/missing") || string(r.Plan) != "null" {
		t.Errorf("the broken root: status %q stage %q plan %s error %q", r.Status, r.Stage, r.Plan, r.Error)
	}
	for _, name := range []string{a, b, clean} {
		r := got[name]
		if r.Status != "planned" || r.Error != "" {
			t.Errorf("%s: status %q error %q; a broken neighbour must not stop it", name, r.Status, r.Error)
		}
		if !tf.FileExists(filepath.FromSlash(r.PlanFile)) {
			t.Errorf("%s: plan file %q not on disk", name, r.PlanFile)
		}
	}
	if !got[a].Changes || !got[b].Changes || got[clean].Changes {
		t.Errorf("changes: a=%v b=%v clean=%v, want true true false", got[a].Changes, got[b].Changes, got[clean].Changes)
	}

	// ---- installs: once for three roots ----
	if n := reg.downloads.Load(); n != 1 {
		t.Errorf("the provider package was downloaded %d times for three roots in one set; want 1", n)
	}
	for _, name := range []string{a, b, clean} {
		pkg := tf.Path(filepath.FromSlash(name), ".terraform", "providers", "registry.opentofu.org", "hashicorp", "simple6", "0.0.1", getproviders.CurrentPlatform.String())
		info, err := os.Lstat(pkg)
		if err != nil {
			t.Errorf("%s: no installed provider: %s", name, err)
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s: the provider is a copy, not a link to the shared cache", name)
		}
	}

	// ---- exit 2 and exit 0 ----
	if _, code, stderr := runSet(t, tf, a, clean); code != 2 {
		t.Errorf("exit %d with changes and no failure, want 2\nstderr:\n%s", code, stderr)
	}
	if _, code, stderr := runSet(t, tf, clean); code != 0 {
		t.Errorf("exit %d with no changes, want 0\nstderr:\n%s", code, stderr)
	}
	if n := reg.downloads.Load(); n != 1 {
		t.Errorf("two more set plans downloaded the package again: %d downloads in all, want 1", n)
	}

	// ---- separate runs: the equivalence oracle, and the counter's red arm ----
	before := reg.downloads.Load()
	for _, name := range []string{a, b, clean} {
		sep := e2e.NewBinary(t, tofuBin, t.TempDir())
		sep.AddEnv("TF_CLI_CONFIG_FILE=" + cliConfig)
		sep.AddEnv("TF_PLUGIN_CACHE_DIR=")
		sep.AddEnv("TF_DATA_DIR=")
		src, err := os.ReadFile(tf.Path(filepath.FromSlash(name), "main.tf"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, sep.Path("main.tf"), string(src))
		if _, stderr, err := sep.Run("init", "-input=false", "-no-color"); err != nil {
			t.Fatalf("%s: separate init: %s\n%s", name, err, stderr)
		}
		_, stderr, err := sep.Run("live-plan", "-input=false", "-no-color", "-detailed-exitcode", "-out=sep.tfplan")
		changes := exitCode(err) == 2
		if err != nil && !changes {
			t.Fatalf("%s: separate plan: %s\n%s", name, err, stderr)
		}
		stdout, stderr, err := sep.Run("show", "-json", "-no-color", "sep.tfplan")
		if err != nil {
			t.Fatalf("%s: separate show: %s\n%s", name, err, stderr)
		}
		want := resourceChanges(t, []byte(stdout))
		have := resourceChanges(t, got[name].Plan)
		if want != have {
			t.Errorf("%s: the set's resource_changes differ from a separate run's\nset:      %s\nseparate: %s", name, have, want)
		}
		if changes != got[name].Changes {
			t.Errorf("%s: separate plan -detailed-exitcode says changes=%v, the set says %v", name, changes, got[name].Changes)
		}
	}
	if n := reg.downloads.Load() - before; n != 3 {
		t.Errorf("three separate inits downloaded the package %d times; the counter should see one per root (3), or it cannot see what it is counting", n)
	}
}

type setRoot struct {
	Root     string          `json:"root"`
	Estate   string          `json:"estate"`
	Status   string          `json:"status"`
	Error    string          `json:"error"`
	Stage    string          `json:"stage"`
	Changes  bool            `json:"changes"`
	PlanFile string          `json:"plan_file"`
	Duration int64           `json:"duration_ms"`
	Plan     json.RawMessage `json:"plan"`
}

type setDoc struct {
	Roots    []setRoot `json:"roots"`
	ExitCode int       `json:"exit_code"`
}

func runSet(t *testing.T, tf interface {
	Run(...string) (string, string, error)
}, args ...string) (setDoc, int, string) {
	t.Helper()
	stdout, stderr, err := tf.Run(append([]string{"live-plan-set", "-json"}, args...)...)
	code := exitCode(err)
	if code < 0 {
		t.Fatalf("live-plan-set did not run: %s", err)
	}
	var doc setDoc
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		t.Fatalf("stdout is not the document: %s\nstdout:\n%s\nstderr:\n%s", jerr, stdout, stderr)
	}
	for _, r := range doc.Roots {
		t.Logf("%s: %s in %dms", r.Root, r.Status, r.Duration)
	}
	if doc.ExitCode != code {
		t.Errorf("the document says exit %d, the process exited %d", doc.ExitCode, code)
	}
	return doc, code, stderr
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// resourceChanges is a plan's resource_changes, re-encoded so two plans'
// compare as strings.
func resourceChanges(t *testing.T, plan []byte) string {
	t.Helper()
	var p struct {
		ResourceChanges json.RawMessage `json:"resource_changes"`
	}
	if err := json.Unmarshal(plan, &p); err != nil {
		t.Fatalf("not a plan: %s", err)
	}
	if len(p.ResourceChanges) == 0 {
		return "[]"
	}
	var v any
	if err := json.Unmarshal(p.ResourceChanges, &v); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// countingRegistry is a provider registry serving one provider,
// hashicorp/simple6 0.0.1, built from provider-simple-v6, and
// counting downloads of its package archive. It serves no signing keys,
// which the installer accepts with a warning.
type countingRegistry struct {
	*httptest.Server
	downloads atomic.Int64
}

func newCountingRegistry(t *testing.T) *countingRegistry {
	t.Helper()
	exe := e2e.GoBuild("github.com/intentius/choudoufu/internal/provider-simple-v6/main", filepath.Join(t.TempDir(), "terraform-provider-simple6"))
	bin, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	hdr := &zip.FileHeader{Name: "terraform-provider-simple6_v0.0.1", Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(bin); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	shasum := hex.EncodeToString(sum[:])
	platform := getproviders.CurrentPlatform
	filename := fmt.Sprintf("terraform-provider-simple6_0.0.1_%s_%s.zip", platform.OS, platform.Arch)

	reg := &countingRegistry{}
	reg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/providers/hashicorp/simple6/versions":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"versions":[{"version":"0.0.1","protocols":["6.0"],"platforms":[{"os":%q,"arch":%q}]}]}`, platform.OS, platform.Arch)
		case fmt.Sprintf("/v1/providers/hashicorp/simple6/0.0.1/download/%s/%s", platform.OS, platform.Arch):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"protocols":["6.0"],"os":%q,"arch":%q,"filename":%q,"download_url":"/files/%s","shasums_url":"/files/SHA256SUMS","shasums_signature_url":"/files/SHA256SUMS.sig","shasum":%q,"signing_keys":{"gpg_public_keys":[]}}`,
				platform.OS, platform.Arch, filename, filename, shasum)
		case "/files/" + filename:
			reg.downloads.Add(1)
			_, _ = io.Copy(w, bytes.NewReader(archive.Bytes()))
		case "/files/SHA256SUMS":
			fmt.Fprintf(w, "%s  %s\n", shasum, filename)
		case "/files/SHA256SUMS.sig":
			_, _ = w.Write([]byte("unsigned"))
		default:
			http.NotFound(w, r)
		}
	}))
	return reg
}
