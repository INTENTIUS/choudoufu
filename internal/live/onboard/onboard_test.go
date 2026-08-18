// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/check"
)

// write lays out one throwaway module directory.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const oneBucket = `
resource "aws_s3_bucket" "b" {
  bucket = "example"
}
`

// loadWith loads dir through the plan's overlay and returns the root module.
func loadWith(t *testing.T, dir string, overlay map[string][]byte) *configs.Module {
	t.Helper()
	res := check.LoadOverlay(context.Background(), dir, overlay)
	if res.Config == nil {
		t.Fatalf("load produced no configuration: %s", res.Diags.Error())
	}
	return res.Config.Module
}

// TestComputeEditsAStateBackedModule is the whole edit on the ordinary shape:
// a terraform block with a backend, and no live configuration anywhere.
func TestComputeEditsAStateBackedModule(t *testing.T) {
	dir := write(t, map[string]string{
		"versions.tf": `
terraform {
  required_version = ">= 1.0"
  backend "s3" {
    bucket = "tfstate"
    key    = "prod/terraform.tfstate"
  }
}
`,
		"main.tf": oneBucket,
	})

	p := Compute(dir)
	if p.Status != StatusEdited {
		t.Fatalf("status = %q (%s), want %q", p.Status, p.Reason, StatusEdited)
	}
	if got, want := p.Added, []string{configs.LiveSidecarFilename}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("added = %v, want %v", got, want)
	}
	if got, want := p.Rewritten, []string{"versions.tf"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("rewritten = %v, want %v", got, want)
	}
	if len(p.Removed) != 1 || !strings.Contains(p.Removed[0], `backend "s3"`) {
		t.Errorf("removed = %v, want one backend \"s3\"", p.Removed)
	}

	// The rewritten file must keep everything else. required_version is the
	// canary: an edit that dropped it would change what the loader accepts.
	rewritten := string(p.Overlay[filepath.Join(dir, "versions.tf")])
	if strings.Contains(rewritten, "backend") {
		t.Errorf("backend survived the rewrite:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, `required_version = ">= 1.0"`) {
		t.Errorf("required_version did not survive the rewrite:\n%s", rewritten)
	}

	mod := loadWith(t, dir, p.Overlay)
	if mod.Live == nil {
		t.Fatal("loaded module has no live configuration")
	}
	if !mod.Live.Sidecar {
		t.Error("live configuration is not the sidecar form")
	}
	if mod.Live.RecordStore == nil {
		t.Fatal("loaded live configuration declares no record_store")
	}
	if got := mod.Live.RecordStore.Type; got != "local" {
		t.Errorf("record store type = %q, want \"local\"", got)
	}
	if mod.Live.Estate != p.Estate || p.Estate == "" {
		t.Errorf("estate = %q in the module, %q in the plan", mod.Live.Estate, p.Estate)
	}
	if mod.Backend != nil {
		t.Error("the loaded module still has a backend")
	}
}

// TestRemovingTheBackendIsLoadBearing is the mutation the edit's second step
// exists for. Adding the sidecar WITHOUT removing the backend does not
// produce a module that loads: configs.Module.appendFile refuses both at
// once. A version of this package that only added the sidecar would report
// every backend-carrying estate as unreadable and no reviewer reading a
// blocked count would see why.
func TestRemovingTheBackendIsLoadBearing(t *testing.T) {
	dir := write(t, map[string]string{
		"versions.tf": "terraform {\n  backend \"s3\" {\n    bucket = \"tfstate\"\n  }\n}\n",
		"main.tf":     oneBucket,
	})

	sidecarOnly := map[string][]byte{
		filepath.Join(dir, configs.LiveSidecarFilename): []byte(sidecar("example")),
	}
	res := check.LoadOverlay(context.Background(), dir, sidecarOnly)
	if res.Config != nil {
		t.Fatal("a module declaring both a backend and a live configuration loaded; the edit's backend removal would then be untested")
	}
	if !strings.Contains(res.Diags.Error(), "backend") {
		t.Errorf("diagnostics do not mention the backend: %s", res.Diags.Error())
	}

	// And the computed plan does load.
	if mod := loadWith(t, dir, Compute(dir).Overlay); mod.Live == nil || mod.Live.RecordStore == nil {
		t.Error("the computed plan did not produce a live configuration with a record store")
	}
}

// TestComputeRemovesACloudBlock covers the other half of the "one or the
// other, never both" wall. A cloud block is refused beside a live
// configuration by its own branch of configs.Module.appendFile, so an edit
// that only knew about "backend" would leave every Terraform Cloud estate
// unreadable.
func TestComputeRemovesACloudBlock(t *testing.T) {
	dir := write(t, map[string]string{
		"main.tf": `
terraform {
  cloud {
    organization = "acme"
    workspaces { name = "prod" }
  }
}
` + oneBucket,
	})
	p := Compute(dir)
	if p.Status != StatusEdited {
		t.Fatalf("status = %q (%s)", p.Status, p.Reason)
	}
	if len(p.Removed) != 1 || !strings.Contains(p.Removed[0], "cloud") {
		t.Errorf("removed = %v, want one cloud block", p.Removed)
	}
	if mod := loadWith(t, dir, p.Overlay); mod.CloudConfig != nil {
		t.Error("the loaded module still has a cloud block")
	}
}

// TestComputeRemovesEveryBackendItFinds: a backend can be in an override file
// or in a second terraform block, and one left behind is enough to refuse the
// module. The file selection therefore has to be the loader's own, which is
// what configs.Parser.ConfigFiles is for.
func TestComputeRemovesEveryBackendItFinds(t *testing.T) {
	dir := write(t, map[string]string{
		"main.tf":       "terraform {\n  backend \"local\" {}\n}\n" + oneBucket,
		"override.tf":   "terraform {\n  backend \"s3\" {\n    bucket = \"b\"\n  }\n}\n",
		"extra.tofu":    "terraform {\n  backend \"http\" {}\n}\n",
		"notconfig.txt": "terraform { backend \"s3\" {} }\n",
		"sub/nested.tf": "terraform {\n  backend \"s3\" {}\n}\n",
	})
	p := Compute(dir)
	if p.Status != StatusEdited {
		t.Fatalf("status = %q (%s)", p.Status, p.Reason)
	}
	if len(p.Removed) != 3 {
		t.Errorf("removed %d block(s), want 3 (main.tf, override.tf, extra.tofu): %v", len(p.Removed), p.Removed)
	}
	for _, r := range p.Removed {
		if strings.Contains(r, "sub/") || strings.Contains(r, "notconfig") {
			t.Errorf("edited a file the loader does not read: %s", r)
		}
	}
	if mod := loadWith(t, dir, p.Overlay); mod.Backend != nil {
		t.Error("the loaded module still has a backend")
	}
}

// TestComputeLeavesAnOnboardedModuleAlone. A module that already declares a
// live configuration with a record_store IS its own onboarded form, and an
// edit here would be measuring something the author did not write.
func TestComputeLeavesAnOnboardedModuleAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"sidecar", map[string]string{
			configs.LiveSidecarFilename: "estate = \"e\"\nrecord_store \"local\" {}\n",
			"main.tf":                   oneBucket,
		}},
		{"block", map[string]string{
			"main.tf": "terraform {\n  live {\n    estate = \"e\"\n    record_store \"local\" {}\n  }\n}\n" + oneBucket,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Compute(write(t, tc.files))
			if p.Status != StatusAlreadyOnboarded {
				t.Fatalf("status = %q (%s), want %q", p.Status, p.Reason, StatusAlreadyOnboarded)
			}
			if len(p.Overlay) != 0 {
				t.Errorf("overlay is not empty: %v", p.Overlay)
			}
		})
	}
}

// TestComputeAddsAStoreToAnExistingLiveConfiguration. A live block without a
// record_store is not onboarded for the purpose this measures: the store is
// what admits the record-backed types. The edit adds one in place rather than
// writing a second live configuration, which would be refused outright.
func TestComputeAddsAStoreToAnExistingLiveConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
	}{
		{"sidecar", map[string]string{
			configs.LiveSidecarFilename: "estate = \"e\"\n",
			"main.tf":                   oneBucket,
		}},
		{"block", map[string]string{
			"main.tf": "terraform {\n  live {\n    estate = \"e\"\n  }\n}\n" + oneBucket,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := write(t, tc.files)
			p := Compute(dir)
			if p.Status != StatusEdited {
				t.Fatalf("status = %q (%s)", p.Status, p.Reason)
			}
			if len(p.Added) != 0 {
				t.Errorf("added %v; the live configuration already existed, so nothing should be created", p.Added)
			}
			mod := loadWith(t, dir, p.Overlay)
			if mod.Live == nil || mod.Live.RecordStore == nil {
				t.Fatal("no record store after the edit")
			}
			if mod.Live.Estate != "e" {
				t.Errorf("estate = %q, want the author's own \"e\"", mod.Live.Estate)
			}
		})
	}
}

// TestComputeRefusesJSONSyntax. Rewriting a .tf.json backend means
// re-marshalling somebody's file, which is not an edit an operator would
// commit. The refusal is what makes the count of unmeasurable estates a
// finding rather than a silent zero.
func TestComputeRefusesJSONSyntax(t *testing.T) {
	dir := write(t, map[string]string{
		"main.tf.json": `{"terraform": {"backend": {"s3": {"bucket": "b"}}}}`,
	})
	p := Compute(dir)
	if p.Status != StatusUnmeasurable || p.Reason != UnmeasurableJSONBackend {
		t.Fatalf("status = %q reason = %q, want unmeasurable/%s", p.Status, p.Reason, UnmeasurableJSONBackend)
	}
	if len(p.Overlay) != 0 {
		t.Error("an unmeasurable plan produced an overlay")
	}
}

// TestComputeRefusesAJSONFileItCannotSee is the shape that would make the
// refusal above useless: a backend in a JSON file the scan skipped. The
// terraform key can hold an ARRAY of blocks in HCL's JSON syntax, and a
// walker that only handled the object form would edit the module, load it,
// and report a verdict about a configuration that does not exist.
func TestComputeRefusesAJSONFileItCannotSee(t *testing.T) {
	dir := write(t, map[string]string{
		"main.tf.json": `{"terraform": [{"required_version": ">= 1.0"}, {"backend": {"s3": {}}}]}`,
	})
	if p := Compute(dir); p.Reason != UnmeasurableJSONBackend {
		t.Fatalf("reason = %q, want %s", p.Reason, UnmeasurableJSONBackend)
	}
}

// TestComputeIgnoresJSONWithoutABackend: JSON in the module is not itself a
// reason to refuse. Only a backend this package cannot remove is.
func TestComputeIgnoresJSONWithoutABackend(t *testing.T) {
	dir := write(t, map[string]string{
		"main.tf.json": `{"resource": {"aws_s3_bucket": {"b": {"bucket": "x"}}}}`,
	})
	if p := Compute(dir); p.Status != StatusEdited {
		t.Fatalf("status = %q (%s), want edited", p.Status, p.Reason)
	}
}

func TestEstateName(t *testing.T) {
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{".corpus/vpc/examples/simple", "corpus-vpc-examples-simple"},
		{"live/e2e/estates/storage", "live-e2e-estates-storage"},
		{".corpus/govuk-aws/terraform/projects/app_ci", "corpus-govuk-aws-terraform-projects-app-ci"},
		{"./a", "a"},
		{"9/8/7", ""},
		{"", ""},
		{".", ""},
	} {
		got, ok := EstateName(tc.dir)
		if tc.want == "" {
			if ok {
				t.Errorf("EstateName(%q) = %q, true; want a refusal", tc.dir, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("EstateName(%q) = %q, %v; want %q, true", tc.dir, got, ok, tc.want)
		}
	}
}

// TestEstateNameStaysWithinTheGrammar over a long path: the name is truncated
// from the front so the specific tail survives, and it must still start with
// a letter afterwards.
func TestEstateNameStaysWithinTheGrammar(t *testing.T) {
	long := strings.Repeat("averylongsegment/", 20) + "9tail"
	got, ok := EstateName(long)
	if !ok {
		t.Fatal("no name derived from a long path")
	}
	if len(got) > 128 {
		t.Errorf("name is %d characters: %q", len(got), got)
	}
	if got[0] < 'a' || got[0] > 'z' {
		t.Errorf("name does not start with a letter: %q", got)
	}
}

// TestEstateNameCannotBiasTheMeasurement. The estate name is the one value in
// the edit this package invents, so the claim that it cannot affect a number
// has to be measured rather than argued. Two names, same findings.
func TestEstateNameCannotBiasTheMeasurement(t *testing.T) {
	dir := write(t, map[string]string{
		"main.tf": oneBucket + "\nresource \"null_resource\" \"n\" {}\n",
	})
	ctx := context.Background()

	analyze := func(estate string) string {
		overlay := map[string][]byte{
			filepath.Join(dir, configs.LiveSidecarFilename): []byte(sidecar(estate)),
		}
		res := check.LoadOverlay(ctx, dir, overlay)
		rep := check.Analyze(ctx, res.Config, check.Context{})
		var b strings.Builder
		for _, f := range rep.Findings {
			b.WriteString(f.ID)
			b.WriteString(":")
			for _, s := range f.Sites {
				b.WriteString(s.Address)
				b.WriteString(",")
			}
			b.WriteString(";")
		}
		return b.String()
	}

	a, b := analyze("alpha"), analyze("zulu-something-entirely-different")
	if a != b {
		t.Errorf("the estate name changed the findings:\n %q\n %q", a, b)
	}
}

// TestComputeWritesNothing. The corpus is a checkout shared by every
// concurrent worktree, and a stray write into it contaminates every later
// measurement silently. The overlay design is what prevents that; this pins
// it against the directory's own modification time and content.
func TestComputeWritesNothing(t *testing.T) {
	dir := write(t, map[string]string{
		"versions.tf": "terraform {\n  backend \"s3\" {}\n}\n",
		"main.tf":     oneBucket,
	})
	before := snapshot(t, dir)
	p := Compute(dir)
	if p.Status != StatusEdited {
		t.Fatalf("status = %q", p.Status)
	}
	loadWith(t, dir, p.Overlay)
	if after := snapshot(t, dir); after != before {
		t.Errorf("the directory changed:\nbefore %s\nafter  %s", before, after)
	}
}

func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.WriteString(path)
		b.WriteString("=")
		b.WriteString(string(src))
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestRecordStoreIsWhatMovesTheVerdict is the measurement's own premise,
// checked rather than assumed: a null_resource is refused without a record
// store and admitted with one. If this ever stopped being true the onboarded
// column would be measuring nothing and every figure built on it would read
// as "onboarding changes nothing".
func TestRecordStoreIsWhatMovesTheVerdict(t *testing.T) {
	dir := write(t, map[string]string{"main.tf": "resource \"null_resource\" \"n\" {}\n"})
	ctx := context.Background()

	published := check.Analyze(ctx, check.Load(ctx, dir).Config, check.Context{})
	if !published.Blocked() {
		t.Fatal("a null_resource with no record store was not blocked")
	}

	p := Compute(dir)
	onboarded := check.Analyze(ctx, check.LoadOverlay(ctx, dir, p.Overlay).Config, check.Context{})
	if onboarded.Blocked() {
		var ids []string
		for _, f := range onboarded.Findings {
			ids = append(ids, f.ID)
		}
		t.Fatalf("a null_resource with a record store is still blocked: %v", ids)
	}
}
