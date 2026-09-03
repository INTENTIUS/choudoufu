// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The parent-list leg (issue #692) lists a bound parent's children and
// proposes removing every child the configuration does not declare. A
// DECLARED child of that shape - an inline aws_iam_role_policy whose
// identity is composed from its role - resolves parent-derived, never
// concrete, so a declared set built from concrete resolutions alone did not
// contain it. The leg then reported the role's own declared policy as an
// undeclared removal, minted an address from the policy name, and the plan
// printed "[WILL BE DESTROYED]" for a resource it was managing, under a "No
// changes." summary. Seen on every plan of a one-role estate, moved or not,
// with and without -refresh=false.

// parentListFixture is one role and its one declared inline policy, the
// smallest configuration that puts a parent-derived child in front of the
// parent-list leg. No live block: nothing this leg reads needs one.
func parentListFixture(t *testing.T, shape fixtureShape) string {
	t.Helper()
	dir := t.TempDir()
	live := ""
	if shape.recordStore {
		live = `
  live {
    estate = "` + estateName + `"
    record_store "local" {
      path = ".tofu-records"
    }
  }
`
	}
	roleRef := "aws_iam_role.r.name"
	if shape.computedParentRef {
		roleRef = "aws_iam_role.r.id"
	}
	src := `
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }` + live + `
}

resource "aws_iam_role" "r" {
  name               = "nr-role"
  assume_role_policy = "{}"
}

resource "aws_iam_role_policy" "r_inline" {
  name   = "nr-inline"
  role   = ` + roleRef + `
  policy = "{}"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fixtureShape is the two ways a declared inline policy stops resolving
// concrete: its role reference is a computed attribute (aws_iam_role.r.id,
// the shape examples/terralith-migration's fixture declares), so the child
// is parent-derived; or the live block names a record store, so an
// untaggable child is record-located and carries no import ID at all.
type fixtureShape struct {
	computedParentRef bool
	recordStore       bool
	// recorded seeds the store with the child's own located record, the
	// kind=identity write an apply leaves behind for an untaggable child,
	// so the pass sees the record the way every plan after the first does.
	recorded bool
	// providerIdentity resolves with the provider's schemas in hand, the way
	// the plan command does ([identity.ResolveWith] with Context.Schemas),
	// and gives aws_iam_role_policy the composite identity schema the AWS
	// provider serves ({role, name}). The resolver then synthesizes an
	// identity-object-only entry for the type, and the declared policy
	// resolves CONCRETE with IdentityValues and an EMPTY ImportID - the
	// shape every real plan carries, and the one the bug was in.
	providerIdentity bool
}

var fixtureShapes = map[string]fixtureShape{
	"literal-parent-ref":             {},
	"computed-parent-ref":            {computedParentRef: true},
	"record-store":                   {recordStore: true},
	"recorded":                       {recordStore: true, recorded: true},
	"all":                            {computedParentRef: true, recordStore: true, recorded: true},
	"provider-identity":              {providerIdentity: true},
	"provider-identity-computed-ref": {providerIdentity: true, computedParentRef: true},
	"provider-identity-recorded":     {providerIdentity: true, recordStore: true, recorded: true},
}

// parentListCloud is the account the fixture stands in: the role, marked as
// this estate's, plus whatever inline policies the caller hangs on it. The
// policy's list accepts "role", which is what makes it parent-list
// recoverable rather than unreadable.
func parentListCloud(shape fixtureShape, inlinePolicies ...string) *fakeCloud {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_role")
	cloud.own("aws_iam_role", "nr-role", "aws_iam_role.r")
	cloud.listableUntagged("aws_iam_role_policy")
	cloud.withListAttr("aws_iam_role_policy", "role")
	if shape.providerIdentity {
		cloud.withRequiredAttr("aws_iam_role_policy", "role")
		cloud.withRequiredAttr("aws_iam_role_policy", "name")
		cloud.withIdentitySchema("aws_iam_role_policy", "role", "name")
	}
	for _, name := range inlinePolicies {
		if shape.providerIdentity {
			cloud.objWithIdentity("aws_iam_role_policy", "nr-role:"+name, map[string]string{"role": "nr-role", "name": name})
			continue
		}
		cloud.obj("aws_iam_role_policy", "nr-role:"+name, nil)
	}
	return cloud
}

func discoverParentListFixture(t *testing.T, cloud *fakeCloud, shape fixtureShape) *Result {
	t.Helper()
	cfg := loadConfig(t, parentListFixture(t, shape))
	resolutions := resolveOrFail(t, cfg)
	if shape.providerIdentity {
		var diags tfdiags.Diagnostics
		resolutions, diags = identity.ResolveWith(t.Context(), cfg, identity.Context{Schemas: cloud.GetProviderSchema(t.Context()).ResourceTypes})
		if diags.HasErrors() {
			t.Fatalf("schema-aware identity resolution failed:\n%s", renderDiags(diags))
		}
	}
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolutions.All(),
		Provider:    cloud,
		Sweep:       true,
	}
	if shape.recorded {
		rawStore, seedStore := recordOrphanHintStore(t)
		if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, mustAddr(t, "aws_iam_role_policy.r_inline"), recordOrphanProviderAddr, projection.LocatedRecord{
			Components: map[string]string{"role": "nr-role", "name": "nr-inline"},
		}); err != nil {
			t.Fatalf("seeding the child's record: %s", err)
		}
		req.HintStore = rawStore
	}
	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)
	return res
}

// TestParentListSweepSkipsDeclaredParentDerivedChild is the bug: the one
// live inline policy is the one the configuration declares. Nothing is
// undeclared, so the leg has nothing to report and nothing to mint.
func TestParentListSweepSkipsDeclaredParentDerivedChild(t *testing.T) {
	for name, shape := range fixtureShapes {
		t.Run(name, func(t *testing.T) {
			res := discoverParentListFixture(t, parentListCloud(shape, "nr-inline"), shape)
			for _, r := range res.Resolutions {
				if r.Type() == "aws_iam_role_policy" {
					t.Logf("declared policy resolves %s at %s (ImportID %q, IdentityValues %v)", r.Class, r.Addr, r.ImportID, r.IdentityValues)
				}
			}
			if len(res.ParentReads) != 0 {
				t.Errorf("the role's own declared inline policy was reported by the parent-list leg:\n%s", res)
			}
			for _, r := range res.Resolutions {
				if r.Type() == "aws_iam_role_policy" && r.Undeclared {
					t.Errorf("an undeclared resolution was minted at %s for a declared policy:\n%s", r.Addr, res)
				}
			}
		})
	}
}

// TestParentListSweepStillFindsTheStrayChild guards the other side: a second
// inline policy nothing declares is still found through the same read and
// still proposed for removal at an address carrying its own name.
func TestParentListSweepStillFindsTheStrayChild(t *testing.T) {
	for name, shape := range fixtureShapes {
		t.Run(name, func(t *testing.T) {
			res := discoverParentListFixture(t, parentListCloud(shape, "nr-inline", "stray"), shape)
			assertOnlyTheStray(t, res)
		})
	}
}

func assertOnlyTheStray(t *testing.T, res *Result) {
	t.Helper()

	if len(res.ParentReads) != 1 {
		t.Fatalf("want exactly the stray policy reported, got %d finding(s):\n%s", len(res.ParentReads), res)
	}
	f := res.ParentReads[0]
	if f.ImportID != "nr-role:stray" || !f.Removal {
		t.Errorf("finding = ImportID %q Removal %v, want nr-role:stray removable", f.ImportID, f.Removal)
	}
	var minted bool
	for _, r := range res.Resolutions {
		if r.Type() == "aws_iam_role_policy" && r.Undeclared {
			minted = true
			if r.Addr.String() != "aws_iam_role_policy.stray" {
				t.Errorf("stray policy minted at %s, want aws_iam_role_policy.stray", r.Addr)
			}
		}
	}
	if !minted {
		t.Errorf("no undeclared resolution for the stray policy:\n%s", res)
	}
}
