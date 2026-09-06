// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
)

// The record-first order (GitHub issue #875, corpus-hongbomiao-labelbox's
// day2_remove): when a whole module block goes - a taggable parent and the
// untaggable child whose identity composes from it, together - the parent
// comes back from the tag sweep as this estate's own orphan, and the child
// is reachable two ways at once. The parent-list leg can list it off the
// parent it just resolved, and the record-orphan-read leg can read it out
// of the estate's own record store. Both propose destroying the same live
// object; only one of them knows what the object is CALLED.
//
// The parent-list leg does not, and says so ([listRecoveredChildAddr]: "no
// marker and no declared block to read a name from", so the label is the
// child's own recovered name, best effort). The record store does: its key
// IS the address the deleted block declared. Measured on the estate, with
// the parent-read legs running first, the plan proposed
//
//	module.labelbox_iam_role_renamed.aws_iam_role_policy.LabelboxRoleS3Policy-hm-labelbox-v2
//
// where stock proposes
//
//	module.labelbox_iam_role_renamed.aws_iam_role_policy.labelbox_iam_role_s3_policy
//
// - the live policy's own name in place of the resource block's - and the
// record-orphan leg's correct proposal never reached the plan behind it.
//
// This is the foundation-order ruling (HANDOFF.md, "The foundation": the
// record holds the identity of every instance and a plan reads it FIRST;
// the sweep and the derivations are the recovery paths for what the record
// cannot answer), so the fix is the order: [recordOrphanReadSweep] runs
// ahead of the two parent-read legs, whose own per-value declared check
// ([declaredChildImportIDs], which reads every resolution of the type, not
// only the declared ones) then sees the record's answer and does not mint a
// second, differently-named one over the top of it.

// recordFirstFixture is the labelbox shape with nothing left declared: the
// role's whole block and its inline policy's block are both gone, so the
// configuration has no resource in it at all. The account still holds both
// objects and the estate's record store still holds the child's identity
// under the address its block used to have.
func recordFirstFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = `
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }

  live {
    estate = "` + estateName + `"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRemovedParentAndChildProposeTheRecordedChildAddress: both blocks
// deleted. The role is destroyed as a tag-sweep orphan; the inline policy
// has to be destroyed with it, at the address the estate's own record names
// - not at a label minted from the live policy's name.
func TestRemovedParentAndChildProposeTheRecordedChildAddress(t *testing.T) {
	cfg := loadConfig(t, recordFirstFixture(t))
	resolutions := resolveOrFail(t, cfg).All()

	cloud := newFakeCloud()
	cloud.listable("aws_iam_role")
	cloud.own("aws_iam_role", "nr-role", "aws_iam_role.r")
	cloud.listableUntagged("aws_iam_role_policy")
	cloud.withListAttr("aws_iam_role_policy", "role")
	cloud.withRequiredAttr("aws_iam_role_policy", "role")
	cloud.withRequiredAttr("aws_iam_role_policy", "name")
	cloud.withIdentitySchema("aws_iam_role_policy", "role", "name")
	cloud.objWithIdentity("aws_iam_role_policy", "nr-role:nr-inline", map[string]string{"role": "nr-role", "name": "nr-inline"})

	rawStore, seedStore := recordOrphanHintStore(t)
	recordedChild := mustAddr(t, "aws_iam_role_policy.r_inline")
	if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, recordedChild, recordOrphanProviderAddr, projection.LocatedRecord{
		Components: map[string]string{"role": "nr-role", "name": "nr-inline"},
	}); err != nil {
		t.Fatalf("seeding the child's record: %s", err)
	}

	res, diags := Discover(t.Context(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    cloud,
		Sweep:       true,
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)

	var roleAddrs, childAddrs []string
	for _, r := range res.Resolutions {
		switch r.Type() {
		case "aws_iam_role":
			roleAddrs = append(roleAddrs, r.Addr.String())
		case "aws_iam_role_policy":
			childAddrs = append(childAddrs, r.Addr.String())
		}
	}
	if len(roleAddrs) != 1 || roleAddrs[0] != "aws_iam_role.r" {
		t.Errorf("aws_iam_role resolutions = %v, want exactly [aws_iam_role.r]:\n%s", roleAddrs, res)
	}
	if len(childAddrs) != 1 || childAddrs[0] != recordedChild.String() {
		t.Errorf("aws_iam_role_policy resolutions = %v, want exactly [%s] - the address the estate's own record names, not a label minted from the live policy name:\n%s", childAddrs, recordedChild, res)
	}
}
