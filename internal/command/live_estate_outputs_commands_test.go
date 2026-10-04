// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1859: the pre-plan identity read (GitHub issue #1575) reads
// terraform_estate_outputs through the command's holder, and live-import,
// live-ls and live-mv never opened it, so each answered "Estate outputs need
// a live block" to a configuration that has one. These tests pin, per
// command, that the holder is opened from the command's record store, and
// that the refusal still stands where it is true.

const estateOutputsNeedLiveBlockSummary = "Estate outputs need a live block"

// wireBuiltinTerraform registers the builtin terraform provider in m's
// testing overrides, built from m's own holder the way production wires it
// (Meta.internalProviders). It must be called on the Meta the command runs
// with, after the command is constructed: Meta is copied by value into the
// command struct, and the holder hangs off that copy.
func wireBuiltinTerraform(m *Meta) {
	m.testingOverrides.Providers[addrs.NewBuiltInProvider("terraform")] = m.internalProviders()["terraform"]
}

// holderReads asks the command's holder for estate network's output name
// directly, the call the builtin provider makes for a data block.
func holderReads(t *testing.T, m *Meta, name string) (map[string]cty.Value, tfdiags.Diagnostics) {
	t.Helper()
	return m.liveEstateOutputs().ReadEstateOutputs(t.Context(), "network", []string{name})
}

func hasSummary(diags tfdiags.Diagnostics, summary string) bool {
	for _, d := range diags {
		if d.Description().Summary == summary {
			return true
		}
	}
	return false
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// TestLiveImportOpensEstateOutputsFromItsRecordStore: after a live-import
// run over a configuration with a live block, the holder reads another
// estate's recorded output from the store live-import opened.
func TestLiveImportOpensEstateOutputsFromItsRecordStore(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-data",
		map[string]string{"id": "tofu-import-unit-data", "bucket": "tofu-import-unit-data"},
		map[string]string{})

	c, done := newLiveImportCommandIn(t, "live-import-estate-outputs", cloud.provider())
	wireBuiltinTerraform(&c.Meta)
	recordProducerOutput(t, mustGetwd(t), "network", "suffix", cty.StringVal("data"))

	code := c.Run([]string{"-no-color", "-state=import.tfstate", "-estate=live-unit"})
	output := done(t)
	all := output.Stdout() + output.Stderr()
	if strings.Contains(all, estateOutputsNeedLiveBlockSummary) {
		t.Errorf("live-import refused terraform_estate_outputs as needing a live block on a configuration that has one:\n%s", all)
	}

	values, diags := holderReads(t, &c.Meta, "suffix")
	if diags.HasErrors() {
		t.Fatalf("the holder did not read from live-import's record store (exit %d): %s\n%s", code, diags.Err(), all)
	}
	if got := values["suffix"]; !got.RawEquals(cty.StringVal("data")) {
		t.Errorf("suffix = %#v, want \"data\"", got)
	}
}

// TestLiveImportWithoutALiveBlockKeepsTheRefusal: live-import-basic declares
// no live block, so there is no store and the refusal is the true one.
func TestLiveImportWithoutALiveBlockKeepsTheRefusal(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-data",
		map[string]string{"id": "tofu-import-unit-data", "bucket": "tofu-import-unit-data"},
		map[string]string{})

	c, done := newLiveImportCommandIn(t, "live-import-basic", cloud.provider())
	wireBuiltinTerraform(&c.Meta)
	_ = c.Run([]string{"-no-color", "-state=import.tfstate", "-estate=live-unit"})
	_ = done(t)

	_, diags := holderReads(t, &c.Meta, "suffix")
	if !hasSummary(diags, estateOutputsNeedLiveBlockSummary) {
		t.Errorf("with no live block the holder must refuse %q, got: %v", estateOutputsNeedLiveBlockSummary, diags.Err())
	}
}

// liveMvEstateOutputsFixture is live-mv-estate-outputs over a cloud holding
// the bucket the destination's identity names once estate network's
// "suffix" output reads "data".
func liveMvEstateOutputsFixture(t *testing.T) (*mvCloud, string) {
	t.Helper()
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-mv-estate-outputs"), td)
	t.Chdir(td)

	cloud := mvNewCloud()
	cloud.put("aws_s3_bucket", "tofu-mv-unit-data",
		map[string]string{"id": "tofu-mv-unit-data", "bucket": "tofu-mv-unit-data"},
		map[string]string{"tofu-estate": "live-unit", "tofu-address": "aws_s3_bucket.data"})
	return cloud, td
}

// TestLiveMvReadsEstateOutputsForIdentity is the green arm: the destination's
// bucket name is built from another estate's recorded output, and the rename
// resolves it from the record store instead of refusing it as needing a
// live block.
func TestLiveMvReadsEstateOutputsForIdentity(t *testing.T) {
	cloud, td := liveMvEstateOutputsFixture(t)
	recordProducerOutput(t, td, "network", "suffix", cty.StringVal("data"))

	c, done := newLiveMvCommand(t, cloud)
	wireBuiltinTerraform(&c.Meta)
	code := c.Run([]string{"-no-color", "-dry-run", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	all := output.Stdout() + output.Stderr()
	if strings.Contains(all, estateOutputsNeedLiveBlockSummary) {
		t.Fatalf("live-mv refused terraform_estate_outputs as needing a live block on a configuration that has one:\n%s", all)
	}
	if code != 0 {
		t.Fatalf("exit code %d, want 0\n%s", code, all)
	}
	if !strings.Contains(output.Stdout(), "tofu-mv-unit-data") {
		t.Errorf("the dry run does not name the bucket the estate output's value resolved to:\n%s", output.Stdout())
	}
	if len(cloud.planned) != 0 || len(cloud.applied) != 0 {
		t.Errorf("-dry-run called the provider's write path: planned=%v applied=%v", cloud.planned, cloud.applied)
	}
}

// TestLiveMvUnrecordedEstateOutputRefusesByName: with nothing recorded, the
// refusal is the store's own, naming the other estate's output - proof the
// read reached the store - and not the "needs a live block" one.
func TestLiveMvUnrecordedEstateOutputRefusesByName(t *testing.T) {
	cloud, _ := liveMvEstateOutputsFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	wireBuiltinTerraform(&c.Meta)
	code := c.Run([]string{"-no-color", "-dry-run", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	all := output.Stdout() + output.Stderr()
	if code == 0 {
		t.Fatalf("a rename over an unrecorded estate output succeeded:\n%s", all)
	}
	if strings.Contains(all, estateOutputsNeedLiveBlockSummary) {
		t.Errorf("the refusal is \"needs a live block\" on a configuration that has one:\n%s", all)
	}
	if !strings.Contains(all, projection.SummaryEstateOutputNotRecorded) || !strings.Contains(all, `Estate "network" has no recorded value for its output "suffix"`) {
		t.Errorf("the refusal does not name estate network's output:\n%s", all)
	}
}

// newLiveLsCommandForEstateOutputs is a bare live-ls command whose holder is
// the one liveLsOpenEstateOutputs fills.
func newLiveLsCommandForEstateOutputs(t *testing.T) *LiveLsCommand {
	t.Helper()
	view, _ := testView(t)
	return &LiveLsCommand{Meta: Meta{WorkingDir: workdir.NewDir(t.TempDir()), View: view}}
}

// TestLiveLsOpensEstateOutputsFromDIRsRecordStore: the declared-instance
// comparison's identity read gets DIR's record store, resolved against DIR
// and not against the process's working directory.
func TestLiveLsOpensEstateOutputsFromDIRsRecordStore(t *testing.T) {
	dir := t.TempDir()
	testCopyDir(t, testFixturePath("live-estate-outputs"), dir)
	// The working directory is somewhere else entirely: a store opened
	// against "." would find nothing recorded.
	t.Chdir(t.TempDir())
	recordProducerOutput(t, dir, "network", "namespace", cty.StringVal("cluster-services"))

	c := newLiveLsCommandForEstateOutputs(t)
	config, cfgDiags := c.loadConfig(t.Context(), dir)
	if cfgDiags.HasErrors() {
		t.Fatal(cfgDiags.Err())
	}
	if diags := c.liveLsOpenEstateOutputs(t.Context(), "app", dir, config); diags.HasErrors() {
		t.Fatalf("opening DIR's record store failed: %s", diags.Err())
	}

	values, diags := holderReads(t, &c.Meta, "namespace")
	if diags.HasErrors() {
		t.Fatalf("the holder did not read from DIR's record store: %s", diags.Err())
	}
	if got := values["namespace"]; !got.RawEquals(cty.StringVal("cluster-services")) {
		t.Errorf("namespace = %#v, want \"cluster-services\"", got)
	}
}

// TestLiveLsWithoutALiveBlockKeepsTheRefusal: no live block, no store, and
// the refusal is the true one.
func TestLiveLsWithoutALiveBlockKeepsTheRefusal(t *testing.T) {
	dir := t.TempDir()
	testCopyDir(t, testFixturePath("live-ls-estate-outputs-no-live"), dir)

	c := newLiveLsCommandForEstateOutputs(t)
	config, cfgDiags := c.loadConfig(t.Context(), dir)
	if cfgDiags.HasErrors() {
		t.Fatal(cfgDiags.Err())
	}
	if diags := c.liveLsOpenEstateOutputs(t.Context(), "app", dir, config); diags.HasErrors() {
		t.Fatalf("a configuration with no live block raised an error: %s", diags.Err())
	}

	_, diags := holderReads(t, &c.Meta, "namespace")
	if !hasSummary(diags, estateOutputsNeedLiveBlockSummary) {
		t.Errorf("with no live block the holder must refuse %q, got: %v", estateOutputsNeedLiveBlockSummary, diags.Err())
	}
}

// TestLiveLsOpensNoStoreWithoutAnEstateOutputsBlock: a live block alone does
// not make live-ls touch a record store. The local backend creates its
// directory on open, so its absence is the proof.
func TestLiveLsOpensNoStoreWithoutAnEstateOutputsBlock(t *testing.T) {
	dir := t.TempDir()
	testCopyDir(t, testFixturePath("live-ls-no-estate-outputs"), dir)

	c := newLiveLsCommandForEstateOutputs(t)
	config, cfgDiags := c.loadConfig(t.Context(), dir)
	if cfgDiags.HasErrors() {
		t.Fatal(cfgDiags.Err())
	}
	if diags := c.liveLsOpenEstateOutputs(t.Context(), "app", dir, config); diags.HasErrors() {
		t.Fatalf("unexpected error: %s", diags.Err())
	}
	if c.Meta.estateOutputs != nil && c.Meta.estateOutputs.src != nil {
		t.Errorf("live-ls opened the estate-outputs reader for a configuration that declares no terraform_estate_outputs block")
	}
	if _, err := os.Stat(filepath.Join(dir, ".tofu-records")); err == nil {
		t.Errorf("live-ls created %s for a configuration that reads no estate outputs", filepath.Join(dir, ".tofu-records"))
	}
}
