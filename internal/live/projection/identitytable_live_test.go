// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestIdentityTableAgainstThePinnedProvider is where the identity table
// stops being an unfalsifiable assertion: the real provider's own resource
// identity schemas say what identifies each admitted type. Divergences are
// logged rather than failed - the table's inference layer is something no
// schema carries, so the two are allowed to describe one identity
// differently - but a table entry naming an argument or an attribute the
// real provider does not have is a bug in the table, and this is the test
// that can see it (verifyIdentityTable, schema_check.go).
//
// It asks the release the table was generated from (pins.AWSProviderVersion,
// flocitest.PinnedProviderDir), not the estate fixture's older one: the
// check lived inside TestBuildAgainstFloci and ran against the fixture's
// provider, where nineteen types the survey pin had added since read as
// breaking findings that were version skew, not table bugs (#1316). It
// needs a provider process and no cloud - the plugin is configured with
// placeholder credentials and skip_requesting_account_id, and nothing here
// leaves the machine.
func TestIdentityTableAgainstThePinnedProvider(t *testing.T) {
	flocitest.Gate(t, "identity table pin")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.PluginCacheDir(t)

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	dir := flocitest.PinnedProviderDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")

	_, schema := launchAWSProvider(t, dir, "skip_requesting_account_id = true")
	verifyIdentityTable(t, schema)
}
