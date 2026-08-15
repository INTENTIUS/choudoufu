// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	residue "github.com/intentius/choudoufu/live"
)

// remoteStateDataType is the data source that reads a state file. Its whole
// purpose is the thing stateless mode removes.
const remoteStateDataType = "terraform_remote_state"

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

	var issues []Issue
	checkConfig(ctx, cfg, addrs.RootModuleInstance, lctx.Schemas, signal, recordStoreConfigured, &issues)
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

// checkConfig appends the issues found in one node of the module tree and then
// recurses into its children.
//
// modInst is the worst-case module instance leading to this node: unkeyed at
// every step reached through a static module call, and - through a
// for_each'd one (59c, issue #59 phase 3) - carrying the longest of that
// call's own keys, chosen the same way [checkOverlongAddresses] already
// picks a count block's highest index over enumerating every instance. It
// is what lets that rule measure an escaped address's worst case at a node
// nested under a keyed module without this pass enumerating every
// combination of every ancestor's keys, which multiplies combinatorially
// with tree depth for no more information than the longest one already
// gives (a marker's length grows with a key's own length, not with which
// key was chosen).
//
// recordStoreConfigured is GitHub issue #73's admission gate, read once
// from the root module (see [recordStoreConfiguredIn]) and threaded
// unchanged through every recursive call, the same way schemas and signal
// already are: it is a property of the whole run, not of whichever module
// node the walk happens to be visiting.
func checkConfig(ctx context.Context, cfg *configs.Config, modInst addrs.ModuleInstance, schemas map[string]providers.Schema, signal *identity.ConfigSignal, recordStoreConfigured bool, issues *[]Issue) {
	if cfg == nil || cfg.Module == nil {
		return
	}

	mod := cfg.Module
	path := cfg.Path

	checkStateBackends(mod, path, issues)
	checkChildModules(ctx, mod, path, issues)
	checkModuleProviderMapping(mod, path, issues)
	checkModuleProviderBlocks(mod, path, issues)
	checkUndeclaredProviderAlias(mod, path, issues)
	checkChildLiveConfig(mod, path, issues)
	checkMovedBlocks(mod, path, issues)
	checkLivePolicy(mod, path, issues)
	checkManagedResources(mod, path, schemas, signal, recordStoreConfigured, issues)
	checkForEachKeys(ctx, mod, path, issues)
	checkOverlongAddresses(ctx, mod, modInst, issues)
	checkDataResources(mod, path, issues)
	checkReceiptLeafRule(mod, path, issues)
	checkReceiptValueRule(mod, path, issues)
	checkReceiptSecretRule(mod, path, issues)

	names := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		childInst := modInst.Child(name, worstCaseChildKey(ctx, mod, mod.ModuleCalls[name]))
		checkConfig(ctx, cfg.Children[name], childInst, schemas, signal, recordStoreConfigured, issues)
	}
}

// worstCaseChildKey is the instance key [checkConfig] descends a child
// module call with for the overlong-address budget: [addrs.NoKey] for a
// static call, and the longest of a for_each call's own keys otherwise -
// ties broken lexicographically, purely for determinism, since two keys of
// equal length produce equally long addresses either way. A for_each whose
// keys this pass cannot enumerate (refused by RuleChildModule; see
// checkChildModules) descends unkeyed: there is no better answer available,
// and RuleChildModule is what stops the run over it, not this rule.
func worstCaseChildKey(ctx context.Context, mod *configs.Module, call *configs.ModuleCall) addrs.InstanceKey {
	if call == nil || call.ForEach == nil {
		return addrs.NoKey
	}
	keys, diag := identity.ChildModuleKeys(ctx, mod, fmt.Sprintf("module %q", call.Name), call.ForEach)
	if diag != nil {
		return addrs.NoKey
	}
	var longest string
	for _, k := range keys {
		sk, ok := k.(addrs.StringKey)
		if !ok {
			continue
		}
		s := string(sk)
		if len(s) > len(longest) || (len(s) == len(longest) && s > longest) {
			longest = s
		}
	}
	if longest == "" {
		return addrs.NoKey
	}
	return addrs.StringKey(longest)
}

// checkStateBackends rejects backend and cloud blocks. Stateless mode has no
// state file, so there is nowhere for a backend to put one and nothing for a
// lock to protect.
func checkStateBackends(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if backend := mod.Backend; backend != nil {
		*issues = append(*issues, Issue{
			Rule:      RuleStateBackend,
			Construct: fmt.Sprintf("backend %q", backend.Type),
			Module:    path,
			Detail: "a backend configures where authoritative state is stored and locked. " +
				"Here the live system is that store. Prior state is a projection, rebuilt " +
				"from it at the start of every operation and discarded at the end. Remove " +
				"the backend block",
			Subject: backend.DeclRange,
		})
	}

	if cloud := mod.CloudConfig; cloud != nil {
		*issues = append(*issues, Issue{
			Rule:      RuleStateBackend,
			Construct: "cloud block",
			Module:    path,
			Detail: "a cloud block is a remote state backend under another name, with " +
				"remote locking attached. Here the live system is the store, and concurrent " +
				"writes to a record are settled by conditional write. Remove the cloud block",
			Subject: cloud.DeclRange,
		})
	}
}

// checkMovedBlocks rejects moved blocks. A moved block edits a stored record
// of which address owns which object; stateless mode keeps that record on the
// object itself, as a tag.
func checkMovedBlocks(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	for _, moved := range mod.Moved {
		*issues = append(*issues, Issue{
			Rule:      RuleMovedBlock,
			Construct: "moved block",
			Module:    path,
			Detail: "a moved block rewrites which state entry belongs to which address, and " +
				"there is no state to rewrite. Ownership lives on the resource itself, in its " +
				"tofu-address marker (live/MARKERS.md), so renaming a resource means " +
				`rewriting that marker: run "choudoufu live-mv <old-address> <new-address>" ` +
				"(phase 3) and delete this block",
			Subject: moved.DeclRange,
		})
	}
}

// checkManagedResources runs the rules that apply to resource blocks:
// provisioners and their connection blocks, logical resource types, and the v0
// admission table.
func checkManagedResources(mod *configs.Module, path addrs.Module, schemas map[string]providers.Schema, signal *identity.ConfigSignal, recordStoreConfigured bool, issues *[]Issue) {
	for _, resource := range mod.ManagedResources {
		addr := resource.Addr().String()

		checkProvisioners(resource, addr, path, issues)
		checkCountIndex(resource, addr, path, issues)
		checkIgnoreChanges(resource, addr, path, schemas, issues)

		// Type classification is managed-resources-only on purpose: a data
		// source stores nothing and is re-read every operation, so it has no
		// identity to recover and no admission question to answer.
		if lt, ok := ClassifyLogicalType(resource.Type); ok {
			if lt.Class == ClassRecordAdmitted && recordStoreConfigured {
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
				continue
			}
			*issues = append(*issues, Issue{
				Rule:      RuleLogicalResource,
				Construct: addr,
				Type:      resource.Type,
				Module:    path,
				Detail:    logicalResourceDetail(resource.Type, lt),
				Subject:   resource.DeclRange,
			})
			// One verdict per resource: a logical type is already out, and
			// telling the author it is also missing from the admission table
			// would just be noise.
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
					"one of the four admission paths: client-assigned identity, marker, "+
					"parent-derived, or list plus content match. Two things can change "+
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
// resource of any type. Both describe effects, and an effect leaves nothing
// behind that a stateless run can read back to learn whether it already ran.
func checkProvisioners(resource *configs.Resource, addr string, path addrs.Module, issues *[]Issue) {
	managed := resource.Managed
	if managed == nil {
		return
	}

	for _, provisioner := range managed.Provisioners {
		*issues = append(*issues, Issue{
			Rule:      RuleProvisioner,
			Construct: fmt.Sprintf("provisioner %q on %s", provisioner.Type, addr),
			Module:    path,
			Detail: "a provisioner runs an effect, not a resource. Whether it has already run " +
				"is knowable only from a stored record of the run, which is exactly the " +
				"authority live resource markers give up: the live system can say what exists, " +
				"never what happened to it. Remove the provisioner",
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
			Detail: "a connection block configures how provisioners reach the resource, and " +
				"provisioners are not available under live resource markers. Remove the connection block",
			Subject: conn.DeclRange,
		})
	}
}

// checkDataResources rejects the terraform_remote_state data source.
func checkDataResources(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	for _, resource := range mod.DataResources {
		if resource.Type != remoteStateDataType {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleRemoteState,
			Construct: resource.Addr().String(),
			Module:    path,
			Detail: "this data source reads a state file, and here prior state is rebuilt from " +
				"the live system rather than stored. Read the producer's own live resource with a data source of its own " +
				"type, filtered on its tofu-estate/tofu-address marker tags (live/OUTPUTS.md " +
				"is the recorded decision and this pattern's spec), or pass values across " +
				"explicitly as variables or outputs of a module call",
			Subject: resource.DeclRange,
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
