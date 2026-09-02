// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/live/providerversion"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// resolvedAWSProviderVersion is the version this run's dependency lock file
// ([Meta.lockedDependencies], the same depsfile machinery "tofu init" and
// "tofu providers lock" already read and write) resolved for hashicorp/aws,
// and false if there is nothing to report: no ".terraform.lock.hcl" in the
// working directory, no entry in it for hashicorp/aws (a configuration that
// declares no aws-provider resources at all), or a dev-override/unmanaged
// run with nothing written to the lock file. Every one of those is a run
// [providerversion.Check] has nothing to compare against, not a run whose
// provider version happens to be unknown.
//
// The match is on provider namespace and type only ("hashicorp" and "aws"),
// not on the full address including registry hostname, because a lock file
// can legitimately name the provider under either
// registry.opentofu.org/hashicorp/aws (today's default host) or
// registry.terraform.io/hashicorp/aws (the transitional host some existing
// configurations still carry, live/e2e/estate/.terraform.lock.hcl among
// them) and both name the identical provider this admission table covers.
func (m *Meta) resolvedAWSProviderVersion() (string, bool) {
	locks, diags := m.lockedDependencies()
	if diags.HasErrors() || locks == nil {
		return "", false
	}
	for addr, lock := range locks.AllProviders() {
		if addr.Namespace == "hashicorp" && addr.Type == "aws" {
			return lock.Version().String(), true
		}
	}
	return "", false
}

// checkAWSProviderVersionSkew is the one call each of live-plan, live-mode
// (plain "choudoufu plan"/"choudoufu apply" under a live block), and
// live-mv makes, once per run, to raise issue #63's warning: the resolved
// hashicorp/aws provider version does not match the version live/survey.json's
// admission table was verified against. See [providerversion.Check] for the
// comparison, the diagnostic text, and the patch-version policy this
// warning follows.
//
// A run with nothing to compare (see resolvedAWSProviderVersion) returns no
// diagnostics, the same silent result an exact version match returns:
// "nothing to warn about" and "no evidence of a mismatch" are the same
// answer as far as a caller appending this to its diagnostics is concerned.
func (m *Meta) checkAWSProviderVersionSkew() tfdiags.Diagnostics {
	resolved, ok := m.resolvedAWSProviderVersion()
	if !ok {
		return nil
	}
	return providerversion.Check(resolved)
}
