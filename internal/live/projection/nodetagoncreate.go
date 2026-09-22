// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #1084: create, then tag in the same apply.
//
// live/registry.json records CloudFormation's tagging.tagOnCreate per type,
// and ten taggable types read false at the pinned bundle -
// AWS::Route53::HostedZone among them, which four corpus estates and the
// reference estate declare. Such a type can hold the marker (tag_updatable
// true) but cannot be handed it in the call that creates it: CreateHostedZone
// takes no Tags parameter, so hashicorp/aws creates the zone and then calls
// ChangeTagsForResource itself when tags are set. Before this file the
// marker still reached the zone, but only through that follow-up call,
// which this fork did not make and could not report on: with it refused,
// the apply error was the provider's own text, and the zone stood in the
// account unmarked, belonging to nobody as far as the next sweep could
// tell.
//
// The maintainer's ruling (2026-09-21): for a type whose registry
// tag_on_create is false, the node writer withholds tags from the create
// call and the live path issues the tag write immediately after, before the
// apply reports the instance complete. A failed tag write is an apply error
// naming the unmarked object, by identity, with the tag command. Keyed on
// the registry flag, no type name in control flow.
//
// How wide the unmarked window is, measured rather than assumed (#1535,
// claim 42's scenario at 950e66c900, the pinned emulator): the tag WRITE is
// one round trip, and the WINDOW is not. WriteAppliedMarkers runs once
// ApplyResourceChange RETURNS, which is when the provider's whole create
// step finishes - for aws_route53_zone that was 15.0s after the zone was
// visible in the account, the same 15s the apply's own "Creation complete"
// line reports. An apply killed in there leaves an object nothing can bind
// to, deliberately reachable rather than a lucky hit. The width on real AWS
// is whatever that provider's create step costs there, and is unmeasured.
//
// The two halves:
//
//   - [NodeResolver.AdjustCreateConfigValue] (tofu.CreateConfigValueAdjuster)
//     is [NodeResolver.AdjustConfigValue] for an instance being created.
//     For a type [NodeResolver.tagsAfterCreate] answers true for, it runs
//     the same conflict checks and then returns the configuration UNSTAMPED:
//     the provider's create call carries the operator's own tags and none
//     of this fork's. Every other type, and every update, is stamped
//     exactly as before.
//   - [NodeResolver.WriteAppliedMarkers] (tofu.AppliedMarkerWriter) runs
//     after ApplyResourceChange has returned the created object and before
//     the PostApply hook prints "Creation complete". For the same types it
//     writes the withheld markers onto the object through the Resource
//     Groups Tagging API's TagResources ([MarkerTagger]), addressed by the
//     arn attribute the provider returned. That write is generic in both
//     directions: one operation for every type on the list, and the same
//     store the discovery sweep reads (internal/live/discovery's
//     markerIndex, GetResources), so the marker written here is what the
//     next plan binds on.
//
// The object the provider returns carries the zone's tags WITHOUT the
// markers, because this run never sent them through the provider. So a
// successful write returns that object with the markers it wrote merged
// into tags and tags_all ([withWrittenMarkers]), and core stores that: the
// state, the state cache written at run end, and the apply's -json stream
// then describe the object as it stands. Until #1316 they carried the
// provider's pre-marker copy, and a -refresh=false plan, which reads the
// cache instead of the cloud, proposed writing the markers again onto a
// zone that already had them. The cache is still never consulted for
// ownership (live/stale_state_ruling_test.go) - the next plan's identity
// comes from the sweep - and no record store write is involved: a
// tag-governed type never had a record.
//
// Why Cloud Control's UpdateResource was not chosen: it would need a per-type
// patch path (HostedZoneTags here, Tags elsewhere), and the pinned emulator
// answers UnsupportedOperation for it. TagResources needs only an ARN.

// SummaryMarkerNotWritten is the one diagnostic this file raises.
const SummaryMarkerNotWritten = "Created object is not marked"

// MarkerTagger is the one write [NodeResolver.WriteAppliedMarkers] makes:
// the Resource Groups Tagging API's TagResources, an upsert of tags onto
// every ARN listed. *internal/live/cloudcontrol.Client implements it; the
// unit tests fake it.
type MarkerTagger interface {
	TagResources(ctx context.Context, arns []string, tags map[string]string) error
}

// The two seams are reached by type assertions on the value
// internal/tofu.EvalContext.ConfigValueAdjuster returns, so a drifted
// signature would silently stop being called rather than fail to compile;
// same reason nodeverify.go asserts its own.
var (
	_ tofu.CreateConfigValueAdjuster = (*NodeResolver)(nil)
	_ tofu.AppliedMarkerWriter       = (*NodeResolver)(nil)
)

// AdjustCreateConfigValue implements internal/tofu.CreateConfigValueAdjuster.
func (n *NodeResolver) AdjustCreateConfigValue(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	return n.adjustConfigValue(ctx, addr, config, schema, true)
}

// tagsAfterCreate reports whether addr's type is one whose create call
// cannot carry tags: live/registry.json's tagging.tag_on_create, read
// through the roster for the Terraform type's CloudFormation counterpart.
// False for a run with no roster, a type the mapping never joined, and a
// type the registry cannot vouch for - all of which take the ordinary path.
func (n *NodeResolver) tagsAfterCreate(addr addrs.AbsResourceInstance) bool {
	cfnType, ok := n.Roster.CloudControlTypeOrService(addr.Resource.Resource.Type)
	if !ok {
		return false
	}
	return n.Roster.TagsAfterCreate(cfnType)
}

// WriteAppliedMarkers implements internal/tofu.AppliedMarkerWriter.
func (n *NodeResolver) WriteAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, action plans.Action, applied cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if action != plans.Create || n.Estate == "" || schema.Block == nil {
		return applied, diags
	}
	if !n.tagsAfterCreate(addr) {
		return applied, diags
	}
	if _, taggable := markers.TagSurface(schema.Block); !taggable {
		return applied, diags
	}
	if n.recordSelected(addr, schema) {
		// strict { markers "record" }: the create was left unstamped for
		// the same reason AdjustConfigValue leaves it, and there is
		// nothing to write now either.
		return applied, diags
	}

	want := n.withheldMarkers(addr)
	if len(want) == 0 {
		return applied, diags
	}

	arn := appliedString(applied, "arn")
	id := appliedString(applied, "id")
	cfnType, _ := n.Roster.CloudControlTypeOrService(addr.Resource.Resource.Type)

	var err error
	switch {
	case arn == "":
		err = errors.New("the object the provider returned carries no arn attribute to address the write to")
	case n.Tagger == nil:
		err = errors.New("this run has no tagging client")
	default:
		tagger := n.Tagger(provider)
		if tagger == nil {
			err = fmt.Errorf("this run has no tagging client for provider configuration %s", provider)
		} else {
			err = tagger.TagResources(ctx, []string{arn}, want)
		}
	}
	if err == nil {
		log.Printf("[DEBUG] stateless/projection: marked %s (%s) after its create: %s", addr, arn, markerTagsArgument(want))
		return withWrittenMarkers(applied, want), diags
	}

	object := arn
	if id != "" && id != arn {
		object = fmt.Sprintf("%s [id=%s]", arn, id)
	}
	if arn == "" && id != "" {
		object = fmt.Sprintf("[id=%s]", id)
	}
	if object == "" {
		object = "an object with no arn and no id in what the provider returned"
	}

	var fix string
	if arn != "" {
		fix = fmt.Sprintf("Mark it, then plan again:\n\n  aws resourcegroupstaggingapi tag-resources --resource-arn-list %s --tags %s", arn, markerTagsArgument(want))
	} else {
		fix = fmt.Sprintf("Mark it by hand with the tag write %s takes, with the tags %s, then plan again.", cfnType, markerTagsArgument(want))
	}

	diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerNotWritten,
		fmt.Sprintf(
			"%s was created as %s and could not be marked: %s.\n\n%s does not take tags in its create call (live/registry.json: tag_on_create false), so this run withheld the ownership markers from the create and wrote them afterwards, and that write failed. The object exists and carries no marker naming estate %q; the next plan will not find it at this address. %s",
			addr, object, err, cfnType, n.Estate, fix,
		)))
	return applied, diags
}

// withheldMarkers is the marker map [NodeResolver.stampedTags] would have
// added to addr's tags and [NodeResolver.AdjustCreateConfigValue] withheld:
// tofu-estate, tofu-address, and tofu-slot where the sweep assigned one,
// minus any key [NodeResolver.PolicyUntag] releases for this instance.
func (n *NodeResolver) withheldMarkers(addr addrs.AbsResourceInstance) map[string]string {
	address := markers.EscapeAddress(addr.String())
	untagKey := n.PolicyUntag[addr.String()]
	out := map[string]string{}
	if untagKey != markers.TagEstate {
		out[markers.TagEstate] = n.Estate
	}
	if untagKey != markers.TagAddress {
		out[markers.TagAddress] = address
	}
	if slot, ok := n.Slots[address]; ok && untagKey != markers.TagSlot {
		out[markers.TagSlot] = slot
	}
	return out
}

// markerTagsArgument renders tags as the aws CLI's --tags shorthand,
// key=value pairs joined by commas, in key order, single-quoted so that an
// escaped address's brackets and quotes survive a shell.
func markerTagsArgument(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+tags[k])
	}
	return "'" + strings.Join(pairs, ",") + "'"
}

// appliedString reads one top-level string attribute off the object the
// provider returned, or "" when it is absent, null, unknown, marked or not
// a string.
func appliedString(obj cty.Value, name string) string {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || obj.IsMarked() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(name) {
		return ""
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || v.IsMarked() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

// withWrittenMarkers returns obj with written merged into its tags and
// tags_all maps: what the object carries once [NodeResolver.WriteAppliedMarkers]'s
// write has landed, and what a refresh would read back from the cloud.
//
// It changes only a map(string) attribute it can read. A tags or tags_all
// that is absent, unknown, of another type, or marked as a whole is left
// exactly as the provider returned it, since nothing here can merge into a
// value it cannot see; marks elsewhere in obj are carried through untouched.
// A null map becomes the written markers. Keys already present keep the
// marker's value, which is the value the write just stored.
func withWrittenMarkers(obj cty.Value, written map[string]string) cty.Value {
	if len(written) == 0 || obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return obj
	}
	unmarked, pvm := obj.UnmarkDeepWithPaths()
	attrs := unmarked.AsValueMap()
	changed := false
	for _, name := range []string{"tags", "tags_all"} {
		v, ok := attrs[name]
		if !ok || !v.Type().Equals(cty.Map(cty.String)) || !v.IsKnown() || markedAt(pvm, name) {
			continue
		}
		merged := map[string]cty.Value{}
		// v came out of UnmarkDeepWithPaths, so it carries no mark; the
		// explicit test is for internal/live/marksafe, which proves a read
		// safe only from a guard it can see at the call site.
		if !v.IsNull() && !v.ContainsMarked() {
			for k, e := range v.AsValueMap() {
				merged[k] = e
			}
		}
		for k, val := range written {
			merged[k] = cty.StringVal(val)
		}
		attrs[name] = cty.MapVal(merged)
		changed = true
	}
	if !changed {
		return obj
	}
	return cty.ObjectVal(attrs).MarkWithPaths(pvm)
}

// markedAt reports whether any mark in pvm sits on attribute name or inside
// it, so [withWrittenMarkers] never rewrites a value carrying a mark.
func markedAt(pvm []cty.PathValueMarks, name string) bool {
	for _, m := range pvm {
		if len(m.Path) == 0 {
			continue
		}
		if step, ok := m.Path[0].(cty.GetAttrStep); ok && step.Name == name {
			return true
		}
	}
	return false
}
