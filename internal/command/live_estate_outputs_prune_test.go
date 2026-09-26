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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1371, second PR: a producer's recorded outputs are deleted
// when it is destroyed, and a removed `output` block's record is deleted on
// the next apply, so a consumer that names either gets "not recorded"
// rather than a value nothing stands behind any more.

// producerApply runs plain "choudoufu apply -auto-approve" plus extra args in
// the current directory, with the builtin terraform provider wired the way
// production wires it.
func producerApply(t *testing.T, extra ...string) {
	t.Helper()
	view, done := testView(t)
	meta := Meta{
		WorkingDir:       workdir.NewDir("."),
		View:             view,
		testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{}},
	}
	meta.testingOverrides.Providers[addrs.NewBuiltInProvider("terraform")] = meta.internalProviders()["terraform"]
	c := &ApplyCommand{Meta: meta}
	code := c.Run(append([]string{"-no-color", "-auto-approve"}, extra...))
	out := done(t)
	if code != 0 {
		t.Fatalf("apply %v: exit %d\nstdout:\n%s\nstderr:\n%s", extra, code, out.Stdout(), out.Stderr())
	}
}

// outputRecorded reports whether estate's record for output name exists in
// the local store under td.
func outputRecorded(t *testing.T, td, estate, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(td, ".tofu-records", filepath.FromSlash(projection.RootOutputKey(estate, name))))
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return false
}

func setupProducer(t *testing.T) string {
	t.Helper()
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-estate-outputs-producer"), td)
	t.Chdir(td)
	t.Setenv("TF_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv(EnvStateCache, "")
	producerApply(t)
	for _, name := range []string{"namespace", "zone"} {
		if !outputRecorded(t, td, "network", name) {
			t.Fatalf("the producer's first apply did not record %q, so this test says nothing about deleting it", name)
		}
	}
	return td
}

// TestProducerDestroyDeletesItsRecordedOutputs: after "apply -destroy" every
// output the producer recorded is gone, and a consumer that reads one is
// told it is not recorded.
func TestProducerDestroyDeletesItsRecordedOutputs(t *testing.T) {
	td := setupProducer(t)

	producerApply(t, "-destroy")
	for _, name := range []string{"namespace", "zone"} {
		if outputRecorded(t, td, "network", name) {
			t.Errorf("estate network was destroyed and its output %q is still recorded, so another estate can still read it", name)
		}
	}

	// The consumer, over the same store.
	consumer, err := os.ReadFile(filepath.Join(testFixturePath("live-estate-outputs"), "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(td, "main.tf"), consumer, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := estateOutputsPlan(t, td)
	if code == 0 {
		t.Fatalf("a consumer read a destroyed estate's output and planned cleanly:\n%s", stdout)
	}
	if !strings.Contains(stderr, projection.SummaryEstateOutputNotRecorded) {
		t.Errorf("the consumer's refusal is not %q:\n%s", projection.SummaryEstateOutputNotRecorded, stderr)
	}
}

// TestRemovedOutputBlockDeletesItsRecord: an apply after an `output` block
// is removed deletes that output's record and keeps the others.
func TestRemovedOutputBlockDeletesItsRecord(t *testing.T) {
	td := setupProducer(t)

	mainTF := filepath.Join(td, "main.tf")
	raw, err := os.ReadFile(mainTF)
	if err != nil {
		t.Fatal(err)
	}
	const block = "output \"zone\" {\n  value = \"internal.example\"\n}\n"
	if !strings.Contains(string(raw), block) {
		t.Fatalf("the fixture no longer has the block this test removes: %q", block)
	}
	if err := os.WriteFile(mainTF, []byte(strings.Replace(string(raw), block, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	producerApply(t)
	if outputRecorded(t, td, "network", "zone") {
		t.Error(`the output block "zone" was removed and its record is still there, so another estate can still read it`)
	}
	if !outputRecorded(t, td, "network", "namespace") {
		t.Error(`the apply deleted the record of "namespace", which is still declared`)
	}
}

// TestTargetedDestroyKeepsRecordedOutputs is the case the delete must be told
// apart from: a -target destroy removes one resource, the estate and its
// outputs remain, and the records stay.
func TestTargetedDestroyKeepsRecordedOutputs(t *testing.T) {
	td := setupProducer(t)

	producerApply(t, "-destroy", "-target=terraform_data.anchor")
	for _, name := range []string{"namespace", "zone"} {
		if !outputRecorded(t, td, "network", name) {
			t.Errorf("a -target destroy of one resource deleted the record of the output %q", name)
		}
	}
}
