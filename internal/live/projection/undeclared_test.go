// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// A projection entry for a resource this estate owns and the configuration no
// longer declares. It is what makes the plan destroy it: the plan engine's
// ordinary rule is that a prior-state instance with no configuration is an
// orphan to be destroyed, and this is how such an instance gets into the
// prior state without a configuration behind it.

// TestUndeclaredInstanceIsMaterialized: a concrete resolution flagged
// Undeclared is read and written into the state exactly like any other, with
// no resource block anywhere in the configuration.
func TestUndeclaredInstanceIsMaterialized(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/deleted", map[string]string{
		"id": "/stateless-e2e/deleted", "name": "/stateless-e2e/deleted",
	})

	addr := mustAddr(t, `aws_cloudwatch_log_group.deleted`)
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{{
		Addr:       addr,
		Class:      identity.ClassConcrete,
		ImportID:   "/stateless-e2e/deleted",
		Undeclared: true,
	}}, cloud.providers(t), Options{UndeclaredProvider: awsProvider})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{`aws_cloudwatch_log_group.deleted`})

	// It has to be a real state entry, filed under the provider the run
	// supplied, or the plan cannot destroy it: the destroy node reads its
	// provider out of the state.
	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}
	rs := mod.Resource(addr.Resource.Resource)
	if rs == nil {
		t.Fatalf("the undeclared instance is not in the state:\n%s", res)
	}
	if rs.ProviderConfig.String() != awsProvider.String() {
		t.Errorf("the undeclared instance is filed under provider %s, want %s", rs.ProviderConfig, awsProvider)
	}
}

// TestUndeclaredInstanceUsesItsOwnAttributedProvider is issue #69's case: an
// estate whose managed resources span more than one provider configuration
// attributes each undeclared instance to the specific provider
// configuration that found it (Options.UndeclaredProviders, keyed by
// address), rather than reading every undeclared instance through one
// UndeclaredProvider - the map takes precedence over the scalar fallback
// per instance, and a caller who never populates the map (every
// single-provider caller, unchanged) still gets exactly
// TestUndeclaredInstanceIsMaterialized's behavior.
func TestUndeclaredInstanceUsesItsOwnAttributedProvider(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/deleted", map[string]string{
		"id": "/stateless-e2e/deleted", "name": "/stateless-e2e/deleted",
	})

	west := addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
		Alias:    "west",
	}
	addr := mustAddr(t, `aws_cloudwatch_log_group.deleted`)

	// A two-provider estate has more than one provider configuration to
	// serve, unlike cloud.providers(t)'s SingleProvider - so this hands out
	// the same mock for either address, which is enough to prove
	// providerFor asked for the right one; the mock does not care which
	// alias "configured" it.
	underlying := cloud.provider(t)
	multi := ProviderFunc(func(_ context.Context, want addrs.AbsProviderConfig) (providers.Interface, error) {
		if want.String() != awsProvider.String() && want.String() != west.String() {
			return nil, fmt.Errorf("no provider configured for %s", want)
		}
		return underlying, nil
	})

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{{
		Addr:       addr,
		Class:      identity.ClassConcrete,
		ImportID:   "/stateless-e2e/deleted",
		Undeclared: true,
	}}, multi, Options{
		// The scalar fallback deliberately names the *other* provider, so
		// this only passes if the map's entry actually wins rather than
		// merely happening to agree with it.
		UndeclaredProvider: awsProvider,
		UndeclaredProviders: map[string]addrs.AbsProviderConfig{
			addr.String(): west,
		},
	})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`aws_cloudwatch_log_group.deleted`})

	mod := res.State.Module(addrs.RootModuleInstance)
	if mod == nil {
		t.Fatal("the projection has no root module")
	}
	rs := mod.Resource(addr.Resource.Resource)
	if rs == nil {
		t.Fatalf("the undeclared instance is not in the state:\n%s", res)
	}
	if rs.ProviderConfig.String() != west.String() {
		t.Errorf("the undeclared instance is filed under provider %s, want the attributed %s (not the scalar fallback %s)", rs.ProviderConfig, west, awsProvider)
	}
}

// TestUndeclaredInstanceFallsBackToTheImpliedProvider: a run that supplies no
// provider still resolves one from the resource type, which is the rule a
// resource block with no provider argument follows anyway.
func TestUndeclaredInstanceFallsBackToTheImpliedProvider(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/deleted", map[string]string{
		"id": "/stateless-e2e/deleted", "name": "/stateless-e2e/deleted",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{{
		Addr:       mustAddr(t, `aws_cloudwatch_log_group.deleted`),
		Class:      identity.ClassConcrete,
		ImportID:   "/stateless-e2e/deleted",
		Undeclared: true,
	}}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{`aws_cloudwatch_log_group.deleted`})
}

// TestUndeclaredInstanceThatIsAlreadyGone: the resource was deleted out of
// band between the sweep and the read. Absence is an ordinary omission, not
// an error, and the plan proposes nothing for it - which is right, because
// there is nothing left to destroy.
func TestUndeclaredInstanceThatIsAlreadyGone(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{{
		Addr:       mustAddr(t, `aws_cloudwatch_log_group.deleted`),
		Class:      identity.ClassConcrete,
		ImportID:   "/stateless-e2e/deleted",
		Undeclared: true,
	}}, newFakeCloud().providers(t), Options{UndeclaredProvider: awsProvider})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, nil)
	assertOmitted(t, res, map[string]Reason{
		`aws_cloudwatch_log_group.deleted`: ReasonAbsent,
	})
}

// TestUndeclaredIsNotAWayAroundTheConfigCheck: an instance whose block is
// missing and which is *not* flagged is still the hard error it always was.
// The flag is how discovery says "I meant this"; without it, a resolution
// naming a block that does not exist is a bug in whatever assembled the list,
// and treating it as a removal would turn that bug into a destroy.
func TestUndeclaredIsNotAWayAroundTheConfigCheck(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/deleted", map[string]string{
		"id": "/stateless-e2e/deleted", "name": "/stateless-e2e/deleted",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{{
		Addr:     mustAddr(t, `aws_cloudwatch_log_group.deleted`),
		Class:    identity.ClassConcrete,
		ImportID: "/stateless-e2e/deleted",
	}}, cloud.providers(t))

	if !diags.HasErrors() {
		t.Fatalf("an unflagged instance with no resource block was accepted:\n%s", res)
	}
	if !strings.Contains(renderDiags(diags), "not in the configuration") {
		t.Errorf("the error does not say the block is missing:\n%s", renderDiags(diags))
	}
	assertMaterialized(t, res, nil)
}

// TestUndeclaredConcreteSupersededByRelocatedDeclaredInstance is GitHub
// issue #404: renaming a resource whose own live-discovered attribute feeds
// a sibling's data-source computation - the estate's own module rename,
// terraform-aws-modules/terraform-aws-s3-bucket's aws_s3_bucket_policy
// reading its bucket's arn - showed a wider defect, found reading the
// live-plan trace directly rather than guessed at: an untaggable,
// single-parent-component type's identity record, written under its OLD,
// now-undeclared address by an earlier apply (GitHub issue #364 ruling item
// 1's recordOrphanReadSweep, internal/live/discovery), materializes as an
// ordinary undeclared instance in the SAME pass a currently-declared block
// re-resolves the exact same live object under its NEW address by ordinary
// parent-derived discovery - needing no `moved` statement of its own, the
// same property day2_rename's own e2e header documents for a renamed
// module's untaggable children. Without a check, the OLD address plans
// destroying the very object the NEW address is about to keep managing:
// one live cloud object, materialized twice, with two different verdicts in
// the SAME plan.
//
// aws_s3_bucket_policy.data (live/e2e/estate/storage.tf) is this shape
// exactly - untaggable, its identity is its parent bucket's own name - so
// this reuses the estate fixture's own declared block rather than adding a
// synthetic one: the DECLARED resolution below is what a real parent-derived
// second pass would render for it, and the UNDECLARED one at a different,
// undeclared address stands in for recordOrphanReadSweep's own output for
// the SAME live object, found under its pre-rename address.
func TestUndeclaredConcreteSupersededByRelocatedDeclaredInstance(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_s3_bucket_policy", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
		"policy": `{"Version":"2012-10-17","Statement":[{"Sid":"AllowAppRoleReadWrite"}]}`,
	})

	declaredAddr := mustAddr(t, `aws_s3_bucket_policy.data`)
	oldAddr := mustAddr(t, `aws_s3_bucket_policy.old`)

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{
			Addr:     declaredAddr,
			Class:    identity.ClassConcrete,
			ImportID: "tofu-stateless-e2e-data",
		},
		{
			Addr:       oldAddr,
			Class:      identity.ClassConcrete,
			ImportID:   "tofu-stateless-e2e-data",
			Undeclared: true,
		},
	}, cloud.providers(t), Options{UndeclaredProvider: awsProvider})
	assertNoErrors(t, diags)

	// Only the currently-declared address ends up in prior state. If the
	// old address materialized too, the plan engine would see a
	// prior-state entry with no configuration behind it and destroy the
	// live object the declared address is managing.
	assertMaterialized(t, res, []string{`aws_s3_bucket_policy.data`})
	assertOmitted(t, res, map[string]Reason{
		`aws_s3_bucket_policy.old`: ReasonSuperseded,
	})

	if res.Has(oldAddr) {
		t.Fatalf("the old, undeclared address is in prior state - it would plan a destroy of the live object %s still manages:\n%s", declaredAddr, res)
	}
	if !res.Has(declaredAddr) {
		t.Fatalf("the currently-declared address is missing from prior state:\n%s", res)
	}
}

// TestUndeclaredConcreteWithDifferentIdentityStillDestroyed is the mutation
// check for the fix above: the dedup key is the resolved import identity,
// not "any undeclared entry of a type something else in this plan also
// uses." An undeclared instance naming a live object NO declared instance
// claims - a genuine removal, recordOrphanReadSweep's own original case
// (GitHub issue #364's harbor unit) - still materializes and is still
// destined for the ordinary undeclared-instance destroy. Deleting the
// materializedIdentity check in build.go (or comparing only by type, not by
// type+importID) makes this test fail exactly the way it would have caught
// a version of the fix that suppressed every undeclared entry outright.
func TestUndeclaredConcreteWithDifferentIdentityStillDestroyed(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	cloud := newFakeCloud()
	cloud.put("aws_s3_bucket_policy", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
		"policy": `{"Version":"2012-10-17","Statement":[{"Sid":"AllowAppRoleReadWrite"}]}`,
	})
	cloud.put("aws_s3_bucket_policy", "tofu-stateless-e2e-orphan", map[string]string{
		"id": "tofu-stateless-e2e-orphan", "bucket": "tofu-stateless-e2e-orphan",
		"policy": `{"Version":"2012-10-17","Statement":[]}`,
	})

	declaredAddr := mustAddr(t, `aws_s3_bucket_policy.data`)
	orphanAddr := mustAddr(t, `aws_s3_bucket_policy.old`)

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{
			Addr:     declaredAddr,
			Class:    identity.ClassConcrete,
			ImportID: "tofu-stateless-e2e-data",
		},
		{
			Addr:       orphanAddr,
			Class:      identity.ClassConcrete,
			ImportID:   "tofu-stateless-e2e-orphan",
			Undeclared: true,
		},
	}, cloud.providers(t), Options{UndeclaredProvider: awsProvider})
	assertNoErrors(t, diags)

	// A genuinely different live object: both addresses materialize, and
	// the orphan is not omitted at all - it is a real undeclared instance
	// destined for the ordinary "not in configuration" destroy.
	assertMaterialized(t, res, []string{`aws_s3_bucket_policy.data`, `aws_s3_bucket_policy.old`})
	assertOmitted(t, res, nil)

	if !res.Has(orphanAddr) {
		t.Fatalf("a genuine, unclaimed orphan was suppressed - it should still be in prior state, destined for a destroy:\n%s", res)
	}
}
