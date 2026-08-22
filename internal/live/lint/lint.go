// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	residue "github.com/intentius/choudoufu/live"
)

// Check runs the stateless subset rules over a loaded configuration and
// returns every construct that puts it outside the subset.
//
// An empty result means the configuration can be planned with no authoritative
// state, as far as v0 can tell. A non-empty result is fatal to a stateless
// operation: the caller should render it (see [Diagnostics]) and stop, before
// identity resolution or projection building begins.
//
// The whole module tree is walked, root first, then children in name order.
// Issues are ordered by module path, then by source position, so that repeated
// runs over the same configuration produce the same output despite the
// configuration's resources living in Go maps.
//
// A nil config yields no issues; there is nothing to reject.
//
// The context reaches one place: the static evaluator that computes a
// for_each expression's keys, which every other caller in the repo already
// plumbs a real one into.
func CheckContext(ctx context.Context, cfg *configs.Config) []Issue {
	return CheckWith(ctx, cfg, Context{})
}

// Context is everything a caller may tell Check about the world outside the
// configuration, mirroring [identity.Context]'s shape: lint's admission
// question and identity resolution's are the same question, asked of the
// same schemas, and a caller that has them for one already has them for the
// other.
type Context struct {
	// Schemas are the provider's managed resource type schemas, keyed by
	// type name, as GetProviderSchema returns them. A resource type absent
	// from the v0 admission table passes lint anyway when the provider's
	// own resource identity schema describes it completely enough - the
	// same rule [identity.SynthesizeTypeIdentity] applies during
	// resolution, applied here too so that a lint refusal and a resolution
	// refusal never disagree about the same type. See [admitted].
	//
	// Nil is the default and means the generated table is the whole of what
	// lint knows, which is what [CheckContext] always passes and what
	// every caller running before a provider has started passes.
	Schemas map[string]providers.Schema
}

// CheckWith is [CheckContext] told the provider schemas the caller already
// has, so that a resource type with no row in the generated admission table
// can still pass when the schemas describe it completely enough.
//
// Admission only ever grows when schemas are present, never shrinks: a
// caller with none gets exactly [CheckContext]'s answer, over the same
// fixtures, byte for byte.
func CheckWith(ctx context.Context, cfg *configs.Config, lctx Context) []Issue {
	if cfg == nil {
		return nil
	}

	// The naming signal is collected once, over the whole configuration,
	// for the same reason identity.Resolve collects it once before
	// classification starts: the schema fallback's verdict for a type
	// depends on what the whole configuration sets, not on how much of the
	// walk below has reached it by the time the type is asked about. Left
	// nil, and never computed, when there are no schemas to fall back to -
	// SynthesizeTypeIdentity refuses immediately in that case and never
	// consults it, and a signal computed for nothing would just be a
	// static-evaluator walk this run does not need. Its own diagnostics are
	// dropped for the same reason lint's for_each check drops evalStatic's:
	// a configuration this pass cannot scan is not this pass's finding to
	// report, and an admitted() call with a nil signal just declines the
	// [identity.AdmitConfigSignal] path rather than failing.
	var signal *identity.ConfigSignal
	if len(lctx.Schemas) > 0 {
		signal, _ = identity.ScanConfig(ctx, cfg)
	}

	// The record store gate is read once, from the root module's live block,
	// the same way the estate name and the policy block are: it is a
	// property of the whole run, not of whichever module node the walk
	// happens to be visiting. See [recordStoreConfigured].
	recordStoreConfigured := recordStoreConfiguredIn(cfg)

	// GitHub issue #365's third toggle, read once from the root module for
	// recordStoreConfigured's reason and for one more: the addresses it
	// carries are module-qualified, so the only node that can resolve them
	// is the one that can see the whole tree. See [checkStrictMarkers].
	markersRecord := markerRepairHonoursIgnoreChanges(cfg)

	// GitHub issue #365's first toggle, read once from the root module for
	// recordStoreConfigured's exact reason. identity.SecretsFor is the one
	// place an omitted argument resolves to strict.DefaultSecrets, and it is
	// the same function internal/live/projection and internal/live/liveimport
	// read it with - the three must never disagree about what the operator
	// asked for, or a configuration this package admits produces a record
	// the write side declines to write.
	secrets := identity.SecretsFor(cfg)

	var issues []Issue
	checkStrictMarkers(cfg, lctx.Schemas, &issues)
	checkConfig(ctx, cfg, addrs.RootModuleInstance, lctx.Schemas, signal, recordStoreConfigured, secrets, markersRecord, nil, &issues)
	sortIssues(issues)
	return issues
}

// recordStoreConfiguredIn reports whether cfg's root module declares a live
// block with a record_store block in it - GitHub issue #73's config surface
// for a RECORD_ADMITTED logical type's admission. False for every
// configuration written before that block existed, which is exactly what
// keeps every RECORD_ADMITTED type refused by default: see
// [checkManagedResources].
func recordStoreConfiguredIn(cfg *configs.Config) bool {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil {
		return false
	}
	return cfg.Module.Live.RecordStore != nil
}

// markerRepairHonoursIgnoreChanges is the set of resources
// [checkIgnoreChanges] must stop refusing: the ones a `markers "record"`
// selection covers, and only when the strict block ALSO sets
// marker_repair = "never". Nil when either half is missing, which is every
// configuration written before GitHub issue #365 and every one that sets
// only one of the two.
//
// # Why it takes both halves
//
// Either half alone leaves a reason to refuse standing.
//
// The selection alone says where the identity lives. It does not say the
// operator wants this tool to stop reconciling the marker tags, and an
// `ignore_changes = [tags]` written for some unrelated reason - a tagging
// robot, a copied module - would then silently start meaning something new
// on the resources it happens to cover. Requiring the second half makes the
// lift something the operator asked for in so many words.
//
// marker_repair = "never" alone says the operator wants the tags left alone
// and gives the resource nowhere else to hold its identity, which is the
// "created unfindable" case this whole rule exists to prevent - slice 1's own
// design, in [strict.Implemented]'s words: lifting the refusal safely "needs
// somewhere else for the identity to live". It is also refused outright by
// [checkLiveStrict], so it cannot reach here on its own.
//
// So the conjunction is the conservative direction, and both readings of the
// pair - "the operator asked" and "the resource has an identity" - have to
// hold before a marker stops being written.
func markerRepairHonoursIgnoreChanges(cfg *configs.Config) *strict.Selection {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil {
		return nil
	}
	st := cfg.Module.Live.Strict
	if st == nil || !st.MarkerRepairSet {
		return nil
	}
	if strict.MarkerRepair(st.MarkerRepair) != strict.Never {
		return nil
	}
	sel := selectionIn(cfg.Module)
	if sel.Empty() {
		return nil
	}
	return sel
}

// checkConfig appends the issues found in one node of the module tree and then
// recurses into its children.
//
// modInst is the worst-case module instance leading to this node: unkeyed at
// every step reached through a static module call, and - through an
// expanded one, whether for_each'd (59c, issue #59 phase 3) or count'd
// (issue #195) - carrying whichever of that call's own keys contributes
// most to an escaped address, chosen the same way [checkOverlongAddresses]
// already picks a count block's highest index over enumerating every
// instance. It is what lets that rule measure an escaped address's worst
// case at a node nested under a keyed module without this pass enumerating
// every combination of every ancestor's keys, which multiplies
// combinatorially with tree depth for no more information than the longest
// one already gives (a marker's length grows with a key's own length, not
// with which key was chosen).
//
// recordStoreConfigured is GitHub issue #73's admission gate, read once
// from the root module (see [recordStoreConfiguredIn]) and threaded
// unchanged through every recursive call, the same way schemas and signal
// already are: it is a property of the whole run, not of whichever module
// node the walk happens to be visiting.
//
// noProviderConfigRange mirrors internal/configs/provider_validation.go's own
// argument of that name: nil while every module call from root down to this
// node uses none of count, for_each, enabled or depends_on, and pinned to
// the source range of whichever of those a call used, the first time one is
// seen on the way down - inherited unchanged into every descendant from
// there, exactly as upstream's validateProviderConfigs threads its
// childNoProviderConfigRange, because the restriction is a property of the
// whole call chain once triggered, not of any single link in it. See
// [checkModuleProviderBlocks] (GitHub issue #201), the only rule that reads
// this argument.
func checkConfig(ctx context.Context, cfg *configs.Config, modInst addrs.ModuleInstance, schemas map[string]providers.Schema, signal *identity.ConfigSignal, recordStoreConfigured bool, secrets strict.Secrets, markersRecord *strict.Selection, noProviderConfigRange *hcl.Range, issues *[]Issue) {
	if cfg == nil || cfg.Module == nil {
		return
	}

	mod := cfg.Module
	path := cfg.Path

	checkStateBackends(mod, path, issues)
	checkChildModules(ctx, cfg, path, issues)
	checkModuleProviderMapping(mod, path, issues)
	checkModuleProviderBlocks(mod, path, noProviderConfigRange, issues)
	checkUndeclaredProviderAlias(mod, path, issues)
	checkChildLiveConfig(mod, path, issues)
	checkMovedBlocks(cfg, mod, path, issues)
	checkLivePolicy(mod, path, issues)
	checkLiveStrict(mod, path, issues)
	checkManagedResources(ctx, mod, path, schemas, signal, recordStoreConfigured, secrets, markersRecord, issues)
	checkForEachKeys(ctx, cfg, path, issues)
	checkOverlongAddresses(ctx, mod, modInst, issues)
	checkReservedSymbols(mod, path, issues)
	checkReceiptLeafRule(mod, path, issues)
	checkReceiptValueRule(mod, path, issues)
	checkReceiptSecretRule(mod, path, issues)

	names := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		call := mod.ModuleCalls[name]
		childNoProviderConfigRange := noProviderConfigRange
		if r := moduleCallBlocksLocalProviders(call); r != nil {
			childNoProviderConfigRange = r
		}
		childInst := modInst.Child(name, worstCaseChildKey(ctx, cfg, name))
		checkConfig(ctx, cfg.Children[name], childInst, schemas, signal, recordStoreConfigured, secrets, markersRecord, childNoProviderConfigRange, issues)
	}
}

// worstCaseChildKey is the instance key [checkConfig] descends a child
// module call with for the overlong-address budget: whichever of that
// call's own instance keys adds the most characters to an escaped
// tofu-address, and [addrs.NoKey] when the call contributes no key at all.
//
// The keys come from [identity.ChildCallKeys], which is the one place the
// count / for_each / static three-way dispatch is written, and calling it
// is the whole point rather than an implementation detail. This function
// used to read call.ForEach itself and treat everything else as a static
// call, so a count'd call - admitted since issue #195, when its count is
// statically evaluable and its own arguments do not read count.index -
// measured every address beneath it as if the module step carried no key,
// under-reporting the budget by the "[N]" the marker actually carries. That
// is the fourth instance of exactly the omission ChildCallKeys' own doc
// comment records in three other walks, and routing through it is what
// stops a fifth.
//
// "Worst case" is measured rather than assumed: each candidate is escaped
// the way the marker escapes it - markers.EscapeAddress over the rendered
// "[key]", which is precisely the ":" plus escaped key text the module step
// contributes to reportOverlongAddress's own measurement - and the longest
// wins, ties broken lexicographically purely for determinism, since two
// equally long keys produce equally long addresses either way. Escaping is
// what makes this the right comparison and raw key length the wrong one:
// markers.EscapeKey expands "." and ":" to two characters each, so a
// shorter raw key can contribute a longer address than a longer one.
//
// For a count'd call every key is an [addrs.IntKey] over 0..n-1, so the
// highest index wins: the escaped step is ":" plus the index's decimal
// digits, and decimal digit count never decreases with magnitude over
// non-negative integers. That is the same choice [checkOverlongAddresses]
// already makes for a resource's own count block, for the same reason.
//
// A call whose keys this pass cannot enumerate (refused by RuleChildModule;
// see checkChildModules) descends unkeyed, and so does one with no
// instances at all - count = 0, or an empty for_each. For the first there is
// no better answer available, and RuleChildModule is what stops the run over
// it, not this rule; for the second there is no instance for a longer
// address to belong to.
//
// cfg is the *configs.Config node the call is written in, needed - since
// issue #308 - by [identity.ChildCallKeys]'s own for_each fallback to chase
// a bare var.X/local.X source across a module-call boundary; see
// [identity.ChildModuleKeys]'s doc.
func worstCaseChildKey(ctx context.Context, cfg *configs.Config, name string) addrs.InstanceKey {
	keys, diag := identity.ChildCallKeys(ctx, cfg, name)
	if diag != nil {
		return addrs.NoKey
	}
	worst := addrs.NoKey
	worstLen := 0
	worstRendered := ""
	for _, k := range keys {
		if k == addrs.NoKey {
			continue
		}
		rendered := k.String()
		n := utf8.RuneCountInString(markers.EscapeAddress(rendered))
		if n > worstLen || (n == worstLen && rendered > worstRendered) {
			worst, worstLen, worstRendered = k, n, rendered
		}
	}
	return worst
}

// checkStateBackends warns about backend and cloud blocks (GitHub issue
// #210: [RuleStateBackend] is [SeverityWarning], not fatal). Stateless mode
// has no state file, so there is nowhere for a backend to put one and
// nothing for a lock to protect - and it never asks: no file under
// internal/live reads mod.Backend or mod.CloudConfig, so the block sits
// unread rather than merely unwise.
//
// That is true only of the run that reaches this warning, though, and this
// warning can only ever be reached from a module with no live block: mod
// arrives here already fully decoded, and internal/configs/module.go's
// appendFile hard-refuses to decode a module that has both a live block and
// a backend (or cloud) block, before this function - or anything else - can
// run over it (GitHub issue #268). So every time this fires, deleting the
// backend or cloud block is optional for the run in front of the operator,
// which is the -estate flag path (live-plan, live-import, live-mv are the
// only commands that accept -estate without a live block). It stops being
// optional the moment a live block is added, which is mandatory for every
// other command, apply included - internal/command/arguments has no -estate
// flag for apply. The messages below say so, rather than promising a
// blanket "not required" that only the flag path can honor.
func checkStateBackends(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if backend := mod.Backend; backend != nil {
		*issues = append(*issues, Issue{
			Rule:      RuleStateBackend,
			Construct: fmt.Sprintf("backend %q", backend.Type),
			Module:    path,
			Detail: "a backend configures where authoritative state is stored and locked. " +
				"This configuration has no live block yet, so the live system does not " +
				"govern it: choudoufu reads no state from this block and takes no lock " +
				"through it, and deleting it changes nothing about this run's outcome. " +
				"That holds only while there is no live block, though. Add one - which " +
				"every command but live-plan, live-import and live-mv requires, since " +
				"those three are the only ones with an -estate flag - and the decoder " +
				"refuses to load a module carrying both a live and a backend block at all " +
				"(\"Both a backend and a live configuration are present\"), before lint or " +
				"anything else runs. If a live block is coming, delete this block now: " +
				"leaving it produces that hard load failure at that point, not a warning " +
				"like this one",
			Subject: backend.DeclRange,
		})
	}

	if cloud := mod.CloudConfig; cloud != nil {
		*issues = append(*issues, Issue{
			Rule:      RuleStateBackend,
			Construct: "cloud block",
			Module:    path,
			Detail: "a cloud block is a remote state backend under another name, with " +
				"remote locking attached. This configuration has no live block yet, so " +
				"the live system does not govern it: choudoufu reads no state from this " +
				"block and takes no lock through it, the same way a backend block is " +
				"ignored. That holds only while there is no live block, though. Add one - " +
				"which every command but live-plan, live-import and live-mv requires, " +
				"since those three are the only ones with an -estate flag - and the " +
				"decoder refuses to load a module carrying both a live and a cloud block " +
				"at all (\"Both a cloud and a live configuration are present\"), before " +
				"lint or anything else runs. If a live block is coming, delete this block " +
				"now: leaving it produces that hard load failure at that point, not a " +
				"warning like this one",
			Subject: cloud.DeclRange,
		})
	}
}

// checkMovedBlocks reports the moved blocks the live path cannot carry, and
// stays silent about the ones it can (GitHub issue #198).
//
// A moved block edits a stored record of which address owns which object.
// Stateless mode keeps that record on the object itself, as a tag, so the
// same statement reads as "a live resource carrying the old address is the
// object the new address names" - and internal/live/discovery indexes the
// marker under both addresses, after which the ordinary tags diff rewrites
// the tag to the new address in place. Nothing needs deleting and nothing
// needs a separate command, which is what makes the moved blocks published
// modules ship permanently as upgrade aids (terraform-aws-modules writes
// them under a "Migrations: vX -> vY" header, and a consumer cannot delete
// upstream source) work rather than wall a consumer off.
//
// The refusal that remains is [moved.Honourable]'s, and lint must not have
// its own: a shape lint admits and discovery does not alias is the dangerous
// direction, because the live resource then reads as an orphan at the old
// address while the new address reads as absent - one cloud object, a
// proposed destroy and a proposed create. Sharing the predicate is what makes
// that impossible rather than merely unlikely.
func checkMovedBlocks(cfg *configs.Config, mod *configs.Module, path addrs.Module, issues *[]Issue) {
	for _, stmt := range moved.StatementsIn(mod, path) {
		reason, ok := moved.Honourable(cfg, stmt)
		if ok {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleMovedBlock,
			Construct: "moved block",
			Module:    path,
			Detail: "ownership lives on the resource itself, in its tofu-address marker " +
				"(live/MARKERS.md), so a moved block is carried by binding the live resource " +
				"under both addresses and letting the plan rewrite the tag. This one cannot " +
				"be carried that way: " + reason + `. Run "choudoufu live-mv <old-address> ` +
				`<new-address>" to rewrite the marker directly, or change the block so its ` +
				"endpoints describe a move the marker can follow",
			Subject: stmt.DeclRange,
		})
	}
}

// checkManagedResources runs the rules that apply to resource blocks:
// provisioners and their connection blocks, logical resource types, and the v0
// admission table.
func checkManagedResources(ctx context.Context, mod *configs.Module, path addrs.Module, schemas map[string]providers.Schema, signal *identity.ConfigSignal, recordStoreConfigured bool, secrets strict.Secrets, markersRecord *strict.Selection, issues *[]Issue) {
	for _, resource := range mod.ManagedResources {
		addr := resource.Addr().String()

		// A block this configuration's own count/for_each provably never
		// instantiates has no live instance for anything below to be
		// about: not an admission verdict, not a provisioner, not a
		// count.index injectivity question, not an ignore_changes
		// compatibility check. Every one of those rules exists to say
		// something about an object this run will create or manage, and a
		// zero-instance block creates none, the same way stock OpenTofu
		// never evaluates its body at all in that case. See
		// [blockHasNoInstances] for how "provably" is read off
		// [identity.ScanConfig]'s own expansion rather than guessed.
		//
		// terraform-aws-modules/terraform-aws-vpc's flagship "complete"
		// example is what surfaced this: aws_default_vpc, aws_default_
		// security_group, aws_default_network_acl, aws_default_route_table
		// and aws_vpn_gateway_attachment all sit behind a `count = var.x ?
		// 1 : 0` the example leaves at its default of false, and
		// aws_vpn_gateway_route_propagation's own count.index reads
		// another resource's instances - refused by count-index in
		// general, moot here because the block it would be refused on
		// never exists. Before this check, all six were hard errors on a
		// popular module's flagship example with nothing live behind any
		// of them - a parity violation, since plain OpenTofu plans this
		// configuration without a single complaint.
		if blockHasNoInstances(signal, path, resource.Addr(), resource.Type) {
			continue
		}

		// Type classification is managed-resources-only on purpose: a data
		// source stores nothing and is re-read every operation, so it has no
		// identity to recover and no admission question to answer. It is
		// computed here, ahead of every rule below rather than only ahead of
		// its own admission check, because checkCountIndex needs it too: a
		// rule that decides which arguments are identity-relevant has to run
		// after the classification that says whether this type has any
		// argument-derived identity at all, not before it.
		lt, isLogical := ClassifyLogicalType(resource.Type)

		checkProvisioners(resource, addr, path, isLogical, recordStoreConfigured, issues)
		checkCountIndex(ctx, mod, resource, addr, path, countIndexScopeForType(resource.Type, lt, isLogical), issues)
		checkIgnoreChanges(resource, addr, path, schemas, markersRecord, issues)

		if isLogical {
			if admitsUnder(lt, secrets) && recordStoreConfigured {
				// GitHub issue #73: a RECORD_ADMITTED type flips from
				// refused to admitted once a live block configures a
				// record_store. Its identity is the persisted micro-state
				// record itself (internal/live/identity's
				// ClassRecordBacked); nothing more to say here, and no
				// RuleLogicalResource issue for it. Falling through to the
				// admission-table check below would be wrong too - this
				// type never goes through admitted()'s generated table, so
				// it is skipped entirely, the same way a refused logical
				// type always has been.
				//
				// EXTERNAL_ADMITTED (issue #314) flips on the same
				// condition and resolves through the same
				// ClassRecordBacked path. What differs is upstream of
				// here, in countIndexScopeForType, which has already run.
				//
				// SECRET_REFUSED (issue #365 slice 3) flips on the same
				// condition PLUS the operator's secrets setting - see
				// [admitsUnder], which is the whole of the difference. The
				// record such a type resolves through then holds secret
				// material, which is what the setting is about and is the
				// only thing that distinguishes it from the two classes
				// above. internal/live/identity's resolver asks the same
				// question again for a caller that skipped this one.
				continue
			}
			*issues = append(*issues, Issue{
				Rule:      RuleLogicalResource,
				Construct: addr,
				Type:      resource.Type,
				Module:    path,
				Detail:    logicalResourceDetail(resource.Type, lt, secrets, recordStoreConfigured),
				Subject:   resource.DeclRange,
			})
			// One verdict per resource: a logical type is already out, and
			// telling the author it is also missing from the admission table
			// would just be noise.
			continue
		}

		if markerlessVetoed(resource.Type) {
			if identity.LocatedType(resource.Type, schemas) {
				// GitHub issue #270. The marker answers "may I delete
				// this" and the identity answers "which object is this",
				// and this branch is where the two stop being one
				// question. A markerless type has nowhere to carry a
				// marker, which is a fact about the first; it does not
				// follow that nothing can say which object it is. For an
				// object choudoufu created, the estate's record store can,
				// because choudoufu minted the ID.
				//
				// With a record_store declared the type is admitted and
				// resolution classifies it identity.ClassRecordLocated;
				// nothing is raised here, exactly as a RECORD_ADMITTED
				// type raises nothing above.
				//
				// Without one it is still refused - but the refusal names
				// the store, because that is now a one-block fix rather
				// than a permanent exclusion. Keeping the permanent
				// wording here would be the #101 defect over again: an
				// operator reading "no configuration edit changes that"
				// about a type one block admits.
				if recordStoreConfigured {
					continue
				}
				*issues = append(*issues, Issue{
					Rule:      RuleMarkerlessType,
					Construct: addr,
					Type:      resource.Type,
					Module:    path,
					Detail:    markerlessLocatedDetail(resource.Type),
					Subject:   resource.DeclRange,
				})
				continue
			}
			// Ahead of the unadmitted-type refusal below, and ahead of the
			// schema fallback inside admitted() - see markerlessVetoed and
			// admitted in admission.go for why the order is load-bearing
			// rather than stylistic.
			//
			// Both facts, in one sentence, from the layer that derives them:
			// identity.MarkerlessReason is generated alongside the roster
			// itself, so the roster cannot grow a member this sentence
			// describes wrongly. The consequence clause comes from
			// internal/live/stamp, which says the same thing to an operator
			// who reached apply with an unstampable resource; #111 is what
			// happens when one fact has two wordings and one of them is
			// updated.
			//
			// No next step is offered because there is none. That is the
			// whole difference between this rule and the one below, whose
			// closing clause asks for an issue naming the type and its
			// import ID.
			*issues = append(*issues, Issue{
				Rule:      RuleMarkerlessType,
				Construct: addr,
				Type:      resource.Type,
				Module:    path,
				Detail: fmt.Sprintf(
					"resource type %q is excluded from the live-markers subset by a standing rule "+
						"rather than by a ratification batch that has not reached it: %s. Applying a "+
						"block of this type %s No configuration edit changes that, and no future batch "+
						"reaches it.",
					resource.Type, identity.MarkerlessReason, UnfindableClause,
				),
				Subject: resource.DeclRange,
			})
			continue
		}

		if !admitted(resource.Type, schemas, signal) {
			// Three clauses of this sentence have gone stale in turn (#101).
			// It said the table was "hardcoded in
			// internal/live/lint/admission.go", which stopped being true
			// when row-gen -emit took the table over; and it promised
			// provider identity schemas "later", when in fact admitted()
			// has already consulted them by the time this line runs, and
			// they declined. Telling an operator to wait for a mechanism
			// that just ran and said no is worse than saying nothing.
			//
			// The first repair then put "generated into
			// admission_generated.go by go run ./tools/row-gen -emit" in
			// the remedy slot, which is worse still: emit.go:44 makes -emit
			// a fixed point, so an operator who runs the command this
			// refusal handed them gets a byte-identical file and the same
			// refusal. Provenance is not a remedy. The rule for this
			// sentence is that its closing clause must tell the reader what
			// to DO, and "nothing, here" is an acceptable answer where
			// pointing at a no-op is not.
			//
			// Do not let the phrase "The provider" into this base sentence:
			// TestAdmittedRefusesRouteWithTableRowBypassed uses it as the
			// marker for [identity.SchemaRefusal]'s appended clause, to tell
			// a schema-informed refusal from a schema-less one.
			detail := fmt.Sprintf(
				"resource type %q is not in the live-markers admission table, and "+
					"neither the provider's identity schema nor this configuration's own "+
					"arguments settled its identity either. A type participates only if "+
					"its identity is recoverable from the live system with no memory, by "+
					"one of the three admission paths: client-assigned identity, marker, "+
					"or parent-derived. Two things can change "+
					"that. If this type's identity argument is one the provider lets you "+
					"omit, setting it explicitly on every block of this type admits it - "+
					"a *_prefix argument in place of the name itself is the usual reason "+
					"a type lands here. Failing that, the table is generated from "+
					"ratified identity rows and is not extensible locally: if this type "+
					"has a documented import ID, open an issue naming the type and the ID",
				resource.Type,
			)
			// A caller with no schemas gets exactly the sentence above, byte
			// for byte: SchemaRefusal returns "" when it has none to
			// consult, the same silence identity.Resolve's own refusal
			// gives. One only when schemas were offered and still refused
			// the type, in the identity layer's own words, so a lint
			// refusal and a resolution refusal never disagree about why.
			if refusal := identity.SchemaRefusal(resource.Type, schemas, signal); refusal != "" {
				detail += "." + refusal
			}
			// The residue roster's second consumer (issue #49): when the
			// refused type falls in a named exclusion cohort - a
			// deprecated service, a TF type live/mapping.json's join
			// found no CFN counterpart for, a type mapped to a CFN type
			// the Registry ships no working handler for, or a type
			// blocked from e2e proof by a floci emulator gap - say so in
			// one more sentence, in the same voice as the schema clause
			// above. A type in no cohort (most of them: simply not wired
			// yet, the scoping boundary live/LIMITATIONS.md's
			// unadmitted-type entry already describes) gets nothing more,
			// so the base message above is unchanged for it.
			if _, sentence, ok := residue.Lookup(resource.Type); ok {
				detail += " " + sentence
			}
			*issues = append(*issues, Issue{
				Rule:      RuleUnadmittedType,
				Construct: addr,
				Type:      resource.Type,
				Module:    path,
				Detail:    detail,
				Subject:   resource.DeclRange,
			})
		}
	}
}

// checkProvisioners rejects provisioner blocks and connection blocks on a
// managed cloud resource that has nowhere to carry the one bit stock
// OpenTofu keeps about a provisioner.
//
// # What the bit is, and why it is the whole question
//
// Stock has exactly one piece of provisioner memory. When a create-time
// provisioner fails, the resource's state object is marked
// states.ObjectTainted (internal/tofu's maybeTainted), and the next plan
// turns a tainted prior object into a synthetic Replace, which re-runs the
// provisioner because a replace is a create. Stock remembers nothing else -
// not what the command was, not whether it changed, not whether a
// SUCCESSFUL provisioner has run before. So the only thing this fork has to
// be able to do is store one bit per instance.
//
// A live-marker-tracked resource has no state entry to put that bit in, and
// internal/live/stamp writes its ownership markers BEFORE the create
// request goes out - so on a provisioner failure the estate is left with a
// fully-marked, live, unprovisioned object that the next plan reads back as
// healthy. That silent under-run, and nothing else, is what this rule
// existed to prevent. GitHub issue #353 gave the bit a home
// (internal/live/projection's tofu-provisioned namespace, provisioned.go),
// so the refusal now applies only where that home does not exist.
//
// recordStoreConfigured is that home: the root module's live block declares
// a record_store (see [recordStoreConfiguredIn]), the same admission gate
// issue #73's RECORD_ADMITTED types already turn on. The predicate is
// derived and names no provisioner type and no provider type - it is "does
// this instance have somewhere to carry a tainted bit", answered by
// "RecordBacked (the isLogical branch below) or a record_store is
// configured", and it covers local-exec, remote-exec and file uniformly
// because there is nothing in it that could tell them apart.
//
// The destroy-time case needs no storage at all and is admitted by the same
// gate for a different reason, stated here so nobody looks for the missing
// half: stock only runs a destroy-time provisioner when it is also calling
// the provider's delete, strictly before it. On failure the delete never
// happens, nothing is written, and the live object survives WITH ITS MARKER
// INTACT - so the marker's continued existence already is the "still needs
// destroying" signal, and the next plan re-proposes the destroy and re-runs
// the provisioner. At-least-once, for free, through a mechanism this fork
// already has.
//
// isLogical narrows that to resources actually exposed to it: a logical,
// record-backed type (null_resource, terraform_data, time_*, non-secret
// random_*, issue #73) is either refused outright by RuleLogicalResource
// (no record_store configured) or, once one is, "running through the stock
// provider lifecycle against a record hydrated from and written back to
// that store" (logical_type.go's own wording for what admission means) -
// its provisioners are exactly as recoverable as they always were, because
// the record store is what replaces state for that type, tainted bit
// included: recordPayload.Status (internal/live/projection/record.go)
// carries states.ObjectTainted through WriteBack and materializeRecord
// (internal/live/projection/writeback.go, build.go) exactly the way a real
// state file's tainted bit survives a plan/apply cycle, so a create-time
// provisioner failure on a record-backed resource still forces a replace
// on the next plan (issue #216, which is the reason this claim is now
// backed by a test - TestTaintedRecordSurvivesWriteBackAndMaterialization
// in record_test.go - and not just this comment). Reporting RuleProvisioner
// on top of RuleLogicalResource would not be a second, independent hazard:
// it would be the same "one verdict per resource" violation
// checkManagedResources already avoids for RuleUnadmittedType once a type
// is already known logical (lint.go, the isLogical branch above) - telling
// an operator to also strip a provisioner that a record_store declaration
// already brings back to life is noise, not a second fix they need to
// make.
func checkProvisioners(resource *configs.Resource, addr string, path addrs.Module, isLogical, recordStoreConfigured bool, issues *[]Issue) {
	if isLogical {
		return
	}
	// The issue #353 gate, mirroring the isLogical branch above: with a
	// record_store declared, a create-time provisioner's failure has
	// somewhere to be remembered, so a provisioner is an ordinary thing to
	// write and stock's behavior is reproduced exactly. Without one, it is
	// not, and the refusal below says which declaration would change that.
	if recordStoreConfigured {
		return
	}
	managed := resource.Managed
	if managed == nil {
		return
	}

	for _, provisioner := range managed.Provisioners {
		*issues = append(*issues, Issue{
			Rule:      RuleProvisioner,
			Construct: fmt.Sprintf("provisioner %q on %s", provisioner.Type, addr),
			Module:    path,
			Detail: "a provisioner that fails while creating a resource leaves the object " +
				"live but half-built, and OpenTofu remembers that as one bit - the tainted " +
				"flag - so the next plan replaces it and runs the provisioner again. A live " +
				"resource marker cannot carry that bit: the marker is written before the " +
				"object is created, so a marked object says nothing about whether its " +
				"provisioner ran. This configuration's estate record store is where that bit " +
				"lives, and this configuration has no live block to have one. Add a live " +
				"block - which implies a local record store, or names a record_store of its " +
				"own - or remove the provisioner",
			Subject: provisioner.DeclRange,
		})
	}

	// A resource-level connection block only exists to configure provisioners,
	// so it is reported in its own right rather than silently tolerated when
	// the provisioners it feeds have been removed.
	if conn := managed.Connection; conn != nil {
		*issues = append(*issues, Issue{
			Rule:      RuleProvisioner,
			Construct: fmt.Sprintf("connection block on %s", addr),
			Module:    path,
			Detail: "a connection block configures how provisioners reach the resource, and a " +
				"provisioner needs the estate's record store to hold its tainted bit before it " +
				"can run under live resource markers. This configuration has no live block, so " +
				"it has no estate and no store - implied or declared. Add a live block, which " +
				"implies a local record store unless it names a record_store of its own, or " +
				"remove the connection block",
			Subject: conn.DeclRange,
		})
	}
}

// sortIssues puts issues into a deterministic order: module path, then file,
// then position within the file, then rule and construct as tiebreakers. The
// configuration's resources come out of Go maps, so without this the output
// order would vary run to run.
func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if av, bv := a.Module.String(), b.Module.String(); av != bv {
			return av < bv
		}
		if a.Subject.Filename != b.Subject.Filename {
			return a.Subject.Filename < b.Subject.Filename
		}
		if a.Subject.Start.Line != b.Subject.Start.Line {
			return a.Subject.Start.Line < b.Subject.Start.Line
		}
		if a.Subject.Start.Column != b.Subject.Start.Column {
			return a.Subject.Start.Column < b.Subject.Start.Column
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Construct < b.Construct
	})
}
