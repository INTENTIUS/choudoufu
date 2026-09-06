// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// TestUntaggableUnlistableSensitivePopulation is issue #855's measurement.
//
// PR #851 (issue #746) split writeBack's once-per-type "unrecordable" log
// into a structural line and a deliberate sensitive-identity refusal line
// ([SensitiveIdentityAttr]), and left both TF_LOG-gated. Its own
// re-measurement against a real hashicorp/aws 6.59.0 GetProviderSchema
// response found 90 resource types recordable as a wire-identity composite
// ([LocatedIdentityPlanFor] -> [LocatedIdentityPlan.Composite]), of which 73
// are markerless ([markers.Taggable] false) and 27 of THOSE are also
// unlistable: no native list resource, no Cloud Control enumeration source
// ([registry.Roster.EnumerationSource]), no content-match binding
// ([ContentMatchTypes]).
//
// For that 27, the record store is the only surviving identity carrier -
// there is no marker to fall back on and nothing to list the object by -
// so #855 asks how many of the 27 ALSO carry a sensitive identity attribute
// ([SensitiveIdentityAttr]'s own definition): the case where the deliberate
// refusal costs the most, because refusing to record the identity there
// means the instance cannot be re-bound after the cache is lost.
//
// The whole funnel is recomputed here rather than starting from PR #851's
// pasted 27, because a check written from a number someone else already
// computed proves nothing about whether that number still holds
// (.claude/skills/measuring-choudoufu: numbers drift silently and the only
// defence is an independent re-derivation). Every step is logged and the
// two populations that matter - the unlistable 27 and the sensitive
// intersection within it - are pinned by value: an added or removed member
// of either fails this test with the name, the same discipline
// TestIdentityGolden and TestLocatedTypePopulation already apply.
//
// It names no resource type in its own logic, only in the pinned
// expectations, which are evidence of a measurement rather than a rule any
// code branches on.
func TestUntaggableUnlistableSensitivePopulation(t *testing.T) {
	if os.Getenv("CHOUDOUFU_LIVE_SCHEMAS") == "" {
		t.Skip("set CHOUDOUFU_LIVE_SCHEMAS=1 to install hashicorp/aws and measure this population against it")
	}

	dir := t.TempDir()
	resp, err := pluginschema.Acquire(context.Background(), pluginschema.Request{
		InitBin:  "terraform",
		WorkDir:  dir,
		Source:   "hashicorp/aws",
		Version:  "6.59.0",
		Provider: addrs.NewDefaultProvider("aws"),
	})
	if err != nil {
		t.Fatalf("acquiring hashicorp/aws schemas: %s", err)
	}
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("loading the embedded registry: %s", err)
	}

	var composite, markerless, unlistable, sensitive []string
	for name, schema := range resp.ResourceTypes {
		if schema.Block == nil {
			continue
		}
		plan, recordable := LocatedIdentityPlanFor(name, schema)
		if !recordable || !plan.Composite() {
			continue
		}
		composite = append(composite, name)

		if markers.Taggable(schema.Block) {
			continue
		}
		markerless = append(markerless, name)

		if _, ok := resp.ListResourceTypes[name]; ok {
			continue
		}
		if _, ok := ContentMatchTypes[name]; ok {
			continue
		}
		if _, ok := roster.EnumerationSource(name); ok {
			continue
		}
		unlistable = append(unlistable, name)

		if SensitiveIdentityAttr(name, schema) != "" {
			sensitive = append(sensitive, name)
		}
	}
	sort.Strings(composite)
	sort.Strings(markerless)
	sort.Strings(unlistable)
	sort.Strings(sensitive)

	t.Logf("resource types=%d composite=%d markerless=%d unlistable=%d sensitive=%d",
		len(resp.ResourceTypes), len(composite), len(markerless), len(unlistable), len(sensitive))
	t.Logf("unlistable population (%d): %v", len(unlistable), unlistable)
	t.Logf("sensitive intersection (%d): %v", len(sensitive), sensitive)

	// PR #851's own re-measurement, commit d3e08d1df2 on main, hashicorp/aws
	// 6.59.0. If this list moves, the funnel above has diverged from PR
	// #851's - re-read both before assuming either is wrong.
	wantUnlistable := []string{
		"aws_alb_target_group_attachment",
		"aws_autoscaling_policy",
		"aws_autoscaling_schedule",
		"aws_cloudfrontkeyvaluestore_key",
		"aws_cloudwatch_log_account_policy",
		"aws_datazone_asset_type",
		"aws_datazone_environment",
		"aws_datazone_environment_blueprint_configuration",
		"aws_datazone_environment_profile",
		"aws_datazone_form_type",
		"aws_datazone_glossary",
		"aws_datazone_glossary_term",
		"aws_datazone_project",
		"aws_datazone_user_profile",
		"aws_networkmanager_prefix_list_association",
		"aws_opensearchserverless_access_policy",
		"aws_opensearchserverless_lifecycle_policy",
		"aws_opensearchserverless_security_config",
		"aws_opensearchserverless_security_policy",
		"aws_organizations_policy_attachment",
		"aws_outposts_capacity_task",
		"aws_redshift_namespace_registration",
		"aws_securityhub_standards_control_association",
		"aws_ssm_maintenance_window_target",
		"aws_ssm_maintenance_window_task",
		"aws_ssoadmin_customer_managed_policy_attachments_exclusive",
		"aws_ssoadmin_managed_policy_attachments_exclusive",
	}
	if added, removed := diffPinned(wantUnlistable, unlistable); added != "" || removed != "" {
		t.Errorf("the untaggable+unlistable composite-identity population moved since PR #851's measurement "+
			"(commit d3e08d1df2, hashicorp/aws 6.59.0): added=[%s] removed=[%s] - re-measure issue #855's "+
			"sensitive intersection against the new population before trusting it", added, removed)
	}

	// Issue #855's own answer, measured 2026-09-05 against hashicorp/aws
	// 6.59.0: empty. See writeback.go's comment at the promotion this
	// measurement settles.
	wantSensitive := []string{}
	if added, removed := diffPinned(wantSensitive, sensitive); added != "" || removed != "" {
		t.Errorf("issue #855's untaggable+unlistable+sensitive intersection moved: added=[%s] removed=[%s] - "+
			"a non-empty result here means the deliberate sensitive-identity refusal now has at least one "+
			"instance whose record is its only identity carrier, which is the open question writeback.go's "+
			"comment names", added, removed)
	}
}

// diffPinned compares a pinned expectation against a freshly measured
// population and names exactly what moved, the same shape
// TestIdentityGolden's own diff takes: an added member and a removed member
// are different findings and a test that only reports "mismatch" makes a
// reader recompute the diff by hand before they can act on it.
func diffPinned(want, got []string) (added, removed string) {
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, g := range got {
		gotSet[g] = true
	}
	var addedList, removedList []string
	for _, g := range got {
		if !wantSet[g] {
			addedList = append(addedList, g)
		}
	}
	for _, w := range want {
		if !gotSet[w] {
			removedList = append(removedList, w)
		}
	}
	sort.Strings(addedList)
	sort.Strings(removedList)
	return strings.Join(addedList, ", "), strings.Join(removedList, ", ")
}
