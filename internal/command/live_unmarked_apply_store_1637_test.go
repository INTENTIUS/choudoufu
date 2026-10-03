// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1637, ruled 2026-09-27: #950's unmarked-apply refusal
// ([statelessUnmarkedApplyGaps]) steps aside when the run's record store is
// writable, because the apply writes the record that finds the object
// again. It still fires when no writable store is open. The no-store arm is
// TestLivePlan_unmarkedApplyOfAMarkerOnlyResourceRefuses, unchanged: its
// fixture declares no live block, so no store is open.
//
// The fixture is claim 3's (live/smoke) at this tier: aws_iam_group_policy
// with no name, a needs-discovery type with nowhere to carry a marker,
// inside a live block with a local record store.

const unmarkedStore1637Estate = "unmarked-store-1637"

func unmarkedStore1637Setup(t *testing.T) (string, *statelessTestCloud) {
	t.Helper()
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-apply-unmarked-store-1637"), td)
	t.Chdir(td)
	cloud := newStatelessTestCloud()
	schemas := statelessTestSchemas()
	// No "tags": the real aws_iam_group_policy has nowhere to carry a marker.
	attrs := map[string]*configschema.Attribute{}
	for _, n := range []string{"id", "group", "name", "name_prefix", "policy"} {
		attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	schemas["aws_iam_group_policy"] = providers.Schema{Block: &configschema.Block{Attributes: attrs}}
	cloud.schemas = schemas
	return td, cloud
}

// unmarkedStore1637Meta is liveBlockMeta with an apply that does what AWS
// does for this type: it assigns the name the configuration left out, and
// the id built from it. The shared test cloud echoes the planned state, which
// leaves both unknown and would make the record impossible to write for a
// reason no real apply has.
func unmarkedStore1637Meta(view *views.View, cloud *statelessTestCloud) Meta {
	meta := liveBlockMeta(view, cloud)
	inst := cloud.provider().(*statelessTestProvider)
	plan := inst.MockProvider.PlanResourceChangeFn
	inst.MockProvider.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		resp := plan(req)
		if req.TypeName != "aws_iam_group_policy" || resp.PlannedState.IsNull() || !req.PriorState.IsNull() {
			return resp
		}
		// A create: the two values AWS assigns are unknown until the apply.
		vals := resp.PlannedState.AsValueMap()
		for _, n := range []string{"id", "name"} {
			if vals[n].IsNull() {
				vals[n] = cty.UnknownVal(cty.String)
			}
		}
		resp.PlannedState = cty.ObjectVal(vals)
		return resp
	}
	echo := inst.MockProvider.ApplyResourceChangeFn
	inst.MockProvider.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		resp := echo(req)
		if req.TypeName != "aws_iam_group_policy" || resp.NewState.IsNull() {
			return resp
		}
		vals := resp.NewState.AsValueMap()
		vals["name"] = cty.StringVal(unmarkedStore1637Name)
		vals["id"] = cty.StringVal(vals["group"].AsString() + ":" + unmarkedStore1637Name)
		resp.NewState = cty.ObjectVal(vals)
		return resp
	}
	meta.testingOverrides.Providers[addrs.NewDefaultProvider("aws")] = providers.FactoryFixed(inst)
	return meta
}

const unmarkedStore1637Name = "terraform-93ae464fde9adaa4139046e212"

// TestLiveApply_unmarkedApplyProceedsWithAWritableRecordStore is the
// writable arm: the apply proceeds, and afterwards the estate's record
// holds an identity for the instance, which is what makes the refusal's
// prediction ("no later run can find it") false here.
func TestLiveApply_unmarkedApplyProceedsWithAWritableRecordStore(t *testing.T) {
	td, cloud := unmarkedStore1637Setup(t)

	view, done := testView(t)
	c := &ApplyCommand{Meta: unmarkedStore1637Meta(view, cloud)}
	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	all := output.Stdout() + output.Stderr()
	if code != 0 {
		t.Fatalf("exit code %d, want 0: a writable record store is open, so the apply records aws_iam_group_policy.app and a later run finds it by that record:\n%s", code, all)
	}
	if strings.Contains(all, stamp.SummaryUnmarkedApply) {
		t.Errorf("the #950 refusal fired with a writable record store open:\n%s", all)
	}
	if !strings.Contains(output.Stdout(), "Apply complete! Resources: 1 added") {
		t.Fatalf("the apply did not create aws_iam_group_policy.app:\n%s", all)
	}

	store, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store the apply wrote into: %s", err)
	}
	rs := projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix(unmarkedStore1637Estate))
	addr, diags := addrs.ParseAbsResourceInstanceStr("aws_iam_group_policy.app")
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	rec, _, _, found, err := rs.GetIdentity(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading the record for aws_iam_group_policy.app: %s", err)
	}
	if !found || rec.Empty() {
		t.Fatalf("the apply proceeded but wrote no identity record for aws_iam_group_policy.app, so nothing can find the object again: found=%v rec=%+v", found, rec)
	}
}

// TestLiveApply_unmarkedApplyRefusesWithAReadOnlyRecordStore is the
// read-only arm: the store opens (an earlier run provisioned its sentinel,
// so #1370's reader tolerance lets this run in), but this run cannot write
// the record, so the object would be unfindable and the refusal stands.
//
// The store is made read-only with the filesystem's own permission bits,
// which is the local backend's form of a role with no s3:PutObject
// (staterecord.IsAccessDenied). A root run ignores those bits, so it
// cannot build this arm; TestRecordStore_probeWritable in
// internal/live/projection covers the same decision with a store that
// denies writes regardless of who runs it.
func TestLiveApply_unmarkedApplyRefusesWithAReadOnlyRecordStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits, so a read-only local store cannot be built; see TestRecordStore_probeWritable")
	}
	td, cloud := unmarkedStore1637Setup(t)

	// Provision the store's sentinel the ordinary way: open it once, as a
	// run with write access would.
	dir := filepath.Join(td, ".tofu-records")
	if _, err := projection.NewRecordStore(context.Background(), &configs.LiveRecordStore{Type: "local"}, nil, unmarkedStore1637Estate, td); err != nil {
		t.Fatalf("provisioning the store: %s", err)
	}
	makeTreeReadOnly(t, dir)

	view, done := testView(t)
	c := &ApplyCommand{Meta: unmarkedStore1637Meta(view, cloud)}
	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	all := output.Stdout() + output.Stderr()
	if code == 0 {
		t.Fatalf("exit 0 against a read-only record store: the apply cannot write the record, so aws_iam_group_policy.app would be created with nothing that finds it again:\n%s", all)
	}
	if !strings.Contains(all, stamp.SummaryUnmarkedApply) {
		t.Errorf("want the %q refusal; got:\n%s", stamp.SummaryUnmarkedApply, all)
	}
	if len(cloud.applied) > 0 {
		t.Errorf("the refused run still wrote to the live system: %v", cloud.applied)
	}
}

// makeTreeReadOnly strips write permission from every directory and file
// under dir, and restores it at cleanup so t.TempDir can remove the tree.
func makeTreeReadOnly(t *testing.T, dir string) {
	t.Helper()
	var paths []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %s", dir, err)
	}
	for i := len(paths) - 1; i >= 0; i-- {
		info, err := os.Stat(paths[i])
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o400)
		if info.IsDir() {
			mode = 0o500
		}
		if err := os.Chmod(paths[i], mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, p := range paths {
			_ = os.Chmod(p, 0o700)
		}
	})
}
