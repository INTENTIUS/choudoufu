// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// checkLiveStrict validates the optional strict block nested in a live block,
// GitHub issue #365's profile toggles.
//
// Two things live here today. marker_repair is checked below; the
// `markers "record"` selection is checked by [checkStrictMarkers], which
// runs once over the whole configuration because half of what it has to say
// needs the module tree and the provider schemas.
//
// # marker_repair, and what makes a setting implemented
//
// A value outside internal/live/strict's vocabulary is a typo. A value
// inside it whose mechanism this build does not have is the more interesting
// case, and it is refused rather than accepted for the reason
// [strict.Implemented] spells out: accepting a setting that says "leave the
// tags alone" while the ordinary plan carried on writing markers would tell
// an operator their estate was safe from this tool when it was not. A
// refusal is loud and reversible (HANDOFF.md, "The safety rule"); a setting
// that does nothing is silent and misleading.
//
// "never" is the one whose answer moved in slice 2, and it moved because it
// acquired a mechanism rather than because the bar was lowered. In this
// codebase nothing repairs a marker directly - see [strict.Implemented] -
// so "never" can only ever mean one thing: honour lifecycle { ignore_changes }
// over the marker tags the way stock honours it, instead of refusing it. That
// is safe exactly where the instance has another identity source, which is
// what a `markers "record"` selection gives it. So "never" is implemented
// WITH such a selection and unimplemented without one
// ([strict.ImplementedWithSelection]), and the boundary is not a thing an
// operator has to read a document to find: [checkIgnoreChanges] keeps firing
// on every resource the selection does not cover, so an estate-wide "never"
// meets the limit at the first resource it does not reach, loudly.
//
// The default is untouched by all of this. An omitted argument - and an
// absent strict block, which is every configuration written before it
// existed - resolves to [strict.DefaultMarkerRepair], today's behavior, and
// is not checked at all. So is `marker_repair = "repair"` written out by
// hand, which must mean exactly the same thing as omitting it; #101 is the
// standing lesson there, where writing a documented default by hand was a
// lint error while omitting it was clean.
//
// This rule follows checkLivePolicy's module handling rather than inventing
// its own: a live block does decode in a child module, nothing outside the
// root's block is acted on, and the conservative direction is to fire on
// whatever is found. The silent half is tracked with the other child-module
// silent-ignores on issue #104.
func checkLiveStrict(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if mod.Live == nil || mod.Live.Strict == nil {
		return
	}
	st := mod.Live.Strict

	if !st.MarkerRepairSet {
		// An omitted argument resolves to strict.DefaultMarkerRepair, which
		// is today's behavior by construction. Nothing to check.
		return
	}

	// Whether the block carries a selection at all, which is what decides
	// which of the two implemented-sets applies. "Non-empty" rather than
	// "present": a markers block naming no type and no address narrows
	// nothing, so it can no more give "never" a mechanism than an absent one
	// could. [checkStrictMarkers] refuses that block on its own account.
	selected := !selectionIn(mod).Empty()

	repair := strict.MarkerRepair(st.MarkerRepair)
	switch {
	case strict.ImplementedWithSelection(repair) && selected:
		// A setting whose mechanism reaches the resources this block
		// selects. See this function's doc comment.
	case strict.Implemented(repair):
		// The default, written out. Same run as omitting it.
	case strict.Valid(repair):
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkerRepair,
			Construct: fmt.Sprintf("strict.marker_repair = %q", st.MarkerRepair),
			Module:    path,
			Detail:    unimplementedRepairDetail(strict.MarkerRepair(st.MarkerRepair)),
			Subject:   st.MarkerRepairRange,
		})
	default:
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkerRepair,
			Construct: fmt.Sprintf("strict.marker_repair = %q", st.MarkerRepair),
			Module:    path,
			Detail: fmt.Sprintf(
				"%q is not a marker_repair setting. Valid settings: %s, of which this build implements %s. "+
					"Omitting the argument means %q, which is what every configuration written before the strict "+
					"block existed gets.",
				st.MarkerRepair, strict.Names(), strict.ImplementedNames(), strict.DefaultMarkerRepair,
			),
			Subject: st.MarkerRepairRange,
		})
	}
}

// unimplementedRepairDetail is the sentence for a setting the schema defines
// and this build cannot act on here. The two settings reach it for different
// reasons and an operator can act on only one of them, so they get different
// next steps rather than one message covering both.
func unimplementedRepairDetail(v strict.MarkerRepair) string {
	if strict.ImplementedWithSelection(v) {
		return fmt.Sprintf(
			"%q means leaving an ownership marker alone when it disagrees with the marker this configuration "+
				"declares, and this build can only do that for a resource that has somewhere ELSE to hold its "+
				"identity: a `markers \"record\"` selection in the same strict block. Without one, every plan would "+
				"carry on writing markers while this setting said otherwise, so it is refused rather than accepted "+
				"silently. Add `markers \"record\" { types = [...] }` or `{ addresses = [...] }` naming the "+
				"resources whose tags something else owns, or remove this argument (which means %q).",
			v, strict.DefaultMarkerRepair,
		)
	}
	return fmt.Sprintf(
		"%q is a marker_repair setting this fork's schema defines and this build does not implement yet "+
			"(GitHub issue #365). It would mean leaving an ownership marker on a live object alone when it "+
			"disagrees with the marker this configuration declares, and nothing in this build does that: "+
			"markers are repaired by the plan's ordinary tags update, which is not yet suppressible per key. "+
			"Accepting the setting would report an estate as protected from this tool while every plan carried "+
			"on rewriting its markers, so it is refused instead. Settings this build implements: %s.",
		v, strict.ImplementedNames(),
	)
}

// checkStrictMarkers validates the `markers "record"` selection: GitHub
// issue #365's third toggle, HANDOFF.md's "per-type or per-address
// markers = record, for tag budgets and tag policies, trading IAM
// governability for a record-held identity".
//
// It runs once over the whole configuration rather than per module, because
// three of its five checks need something a single module node does not
// have: the address list is resolved against every module in the tree, the
// record_store gate is the root's, and the recordable-identity question
// needs the provider schemas. A live block in a child module is refused
// outright by [checkChildLiveConfig], so reading only the root's is not a
// silent ignore here the way it is for [checkLivePolicy].
//
// # What each refusal is protecting
//
// The selection withholds an ownership marker. Every way it can be wrong
// therefore ends in the same place - a live object with no marker and no
// usable record, which no later run can find and every later plan proposes
// creating again - so all five are refusals rather than warnings:
//
//   - An empty block. It narrows nothing, so reading it as a selection means
//     guessing whether the author meant everything or nothing. Same rule, and
//     the same reasoning, as an empty policy scope block (see [scopeIsSet]).
//   - No record_store. The record store is where the identity would go. A
//     selection with nowhere to put one is not a selection.
//   - An address that is not a usable resource address. See
//     [strict.ParseSelection] for the three shapes it refuses beyond the
//     -target grammar's own, of which the instance key is the one worth
//     knowing about: the selection's unit is the resource BLOCK.
//   - An address naming no resource in this configuration. A typo here is
//     silent in the worst way: the resource the author meant to leave
//     unmarked keeps its marker, everything works, and the tag budget they
//     were buying back is still spent.
//   - A selected type whose identity a record cannot hold, or that the
//     provider will not import back. This is the one that is really about
//     the safety rule, and [identity.SelectedLocatedRefusal] names which
//     condition failed, because the two have opposite next steps.
//
// The last one is checked only where the schemas are available to check it,
// the same asymmetry [checkIgnoreChanges] and admitted() already have: a
// caller with no schemas gets a stricter answer downstream, not a different
// one, because internal/live/identity and internal/live/stamp both decline
// to honour a selection they cannot verify and leave the marker where it is.
func checkStrictMarkers(cfg *configs.Config, schemas map[string]providers.Schema, issues *[]Issue) {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return
	}
	m := cfg.Module.Live.Strict.MarkersRecord
	if m == nil {
		return
	}
	path := cfg.Path

	sel, problems := strict.ParseSelection(m.Types, m.Addresses)

	for _, p := range problems {
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkers,
			Construct: fmt.Sprintf("strict.markers %q addresses = [%q]", m.Kind, p.Raw),
			Module:    path,
			Detail:    p.Detail,
			Subject:   m.AddressesRange,
		})
	}

	// Emptiness is measured on the lists as WRITTEN, not on the parsed
	// selection. An addresses list whose every entry was just refused above
	// parses to an empty selection, and saying "you named nothing" on top of
	// "this entry is not usable" would be two refusals for one mistake, the
	// second of them describing a block that plainly does name something.
	if len(m.Types) == 0 && len(m.Addresses) == 0 {
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkers,
			Construct: fmt.Sprintf("strict.markers %q", m.Kind),
			Module:    path,
			Detail: fmt.Sprintf(
				"markers %q names no type and no address, so it narrows nothing. An empty selection block is "+
					"indistinguishable from no block at all, and reading it as \"every resource\" would withhold an "+
					"ownership marker from resources nobody named. Give it a \"types\" list, an \"addresses\" list, "+
					"or both - or remove the block, which means every taggable resource keeps its marker.",
				m.Kind,
			),
			Subject: m.DeclRange,
		})
		return
	}

	if cfg.Module.Live.RecordStore == nil {
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkers,
			Construct: fmt.Sprintf("strict.markers %q", m.Kind),
			Module:    path,
			Detail: fmt.Sprintf(
				"markers %q holds a resource's identity in the estate's record store instead of in an ownership "+
					"marker tag, and this live block declares no record_store, so there is nowhere for that identity "+
					"to go. A resource with neither a marker nor a record cannot be found by any later run: every "+
					"plan after the first would propose creating another one. Add a record_store block - "+
					"record_store \"local\" {} is the solo default, record_store \"ssm\" {} the team one - or remove "+
					"this selection.",
				m.Kind,
			),
			Subject: m.DeclRange,
		})
		return
	}

	// An address that resolves to no resource block. Checked against the
	// whole static module tree, because an address may be module-qualified.
	declared := declaredResources(cfg)
	for _, want := range sel.Resources() {
		if declared[want] {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkers,
			Construct: fmt.Sprintf("strict.markers %q addresses = [%q]", m.Kind, want),
			Module:    path,
			Detail: fmt.Sprintf(
				"%s is not a resource this configuration declares, so this entry selects nothing. That is worth "+
					"refusing rather than ignoring: the resource the entry was meant to name keeps its ownership "+
					"marker, every run works, and the tag it was written to save is still written. Correct the "+
					"address, or remove it.",
				want,
			),
			Subject: m.AddressesRange,
		})
	}

	// The safety condition, over every type the selection actually reaches:
	// the types named outright, plus the types of the addresses named.
	//
	// [identity.SelectedLocatedRefusal]'s three conditions do not all need a
	// schema, and the split matters for what this rule can say to a caller
	// running before a provider has started. The importability veto is a
	// generated roster and is read here whatever the run holds. The other
	// two are schema reads, and with no schema that function answers "the
	// selection is not applied" - which is true, and is not a fact about
	// the CONFIGURATION, so it is not raised as one. Not applying a
	// selection leaves every marker exactly where it was, which is the safe
	// direction and the same one internal/live/identity and
	// internal/live/stamp take from the same absence.
	for _, typeName := range selectedTypes(cfg, sel, m.Types) {
		if _, haveSchema := schemas[typeName]; !haveSchema && !identity.NotImportable(typeName) {
			continue
		}
		refusal := identity.SelectedLocatedRefusal(typeName, schemas)
		if refusal == "" {
			continue
		}
		subject := m.TypesRange
		if !m.TypesSet {
			subject = m.AddressesRange
		}
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkersUnrecordable,
			Construct: fmt.Sprintf("strict.markers %q covers %s", m.Kind, typeName),
			Type:      typeName,
			Module:    path,
			Detail:    refusal,
			Subject:   subject,
		})
	}
}

// selectionIn is the root module's selection, parsed, with the unusable
// address entries dropped - which is exactly what internal/live/identity,
// internal/live/stamp and internal/live/projection see. [checkStrictMarkers]
// refuses each dropped entry by name, so the two readings agree on every
// configuration that lints clean.
func selectionIn(mod *configs.Module) *strict.Selection {
	if mod == nil || mod.Live == nil || mod.Live.Strict == nil || mod.Live.Strict.MarkersRecord == nil {
		return nil
	}
	m := mod.Live.Strict.MarkersRecord
	sel, _ := strict.ParseSelection(m.Types, m.Addresses)
	return sel
}

// declaredResources is every managed resource block in the static module
// tree, keyed by its [addrs.ConfigResource] string - the same spelling
// [strict.Selection] normalizes an address entry to.
func declaredResources(cfg *configs.Config) map[string]bool {
	out := map[string]bool{}
	var walk func(*configs.Config)
	walk = func(node *configs.Config) {
		if node == nil || node.Module == nil {
			return
		}
		for _, rc := range node.Module.ManagedResources {
			addr := addrs.ConfigResource{Module: node.Path, Resource: rc.Addr()}
			out[addr.String()] = true
		}
		names := make([]string, 0, len(node.Children))
		for name := range node.Children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			walk(node.Children[name])
		}
	}
	walk(cfg)
	return out
}

// selectedTypes is every resource type the selection reaches: the ones its
// types list names outright, plus the type of every resource its addresses
// list names and this configuration declares. Sorted, so the refusals a
// configuration produces are in a stable order.
//
// A named type that nothing declares is included deliberately. It selects no
// instance today, so it can mis-mark nothing today - but it is a standing
// instruction, and telling an author now that the type they listed cannot be
// held in a record is better than telling them the first time they add one.
func selectedTypes(cfg *configs.Config, sel *strict.Selection, named []string) []string {
	seen := map[string]bool{}
	for _, t := range named {
		if t != "" {
			seen[t] = true
		}
	}

	var walk func(*configs.Config)
	walk = func(node *configs.Config) {
		if node == nil || node.Module == nil {
			return
		}
		for _, rc := range node.Module.ManagedResources {
			addr := addrs.ConfigResource{Module: node.Path, Resource: rc.Addr()}
			if sel.Selects(addr) {
				seen[rc.Type] = true
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cfg)

	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
