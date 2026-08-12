// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
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
	if cfg == nil {
		return nil
	}

	var issues []Issue
	checkConfig(ctx, cfg, &issues)
	sortIssues(issues)
	return issues
}

// checkConfig appends the issues found in one node of the module tree and then
// recurses into its children.
func checkConfig(ctx context.Context, cfg *configs.Config, issues *[]Issue) {
	if cfg == nil || cfg.Module == nil {
		return
	}

	mod := cfg.Module
	path := cfg.Path

	checkStateBackends(mod, path, issues)
	checkChildModules(mod, path, issues)
	checkMovedBlocks(mod, path, issues)
	checkManagedResources(mod, path, issues)
	checkForEachKeys(ctx, mod, path, issues)
	checkOverlongAddresses(ctx, mod, path, issues)
	checkDataResources(mod, path, issues)
	checkReceiptLeafRule(mod, path, issues)

	names := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		checkConfig(ctx, cfg.Children[name], issues)
	}
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
				"Stateless mode has no state file to store: prior state is a projection, " +
				"rebuilt from the live system at the start of every operation and discarded " +
				"at the end. Remove the backend block",
			Subject: backend.DeclRange,
		})
	}

	if cloud := mod.CloudConfig; cloud != nil {
		*issues = append(*issues, Issue{
			Rule:      RuleStateBackend,
			Construct: "cloud block",
			Module:    path,
			Detail: "a cloud block is a remote state backend under another name, with " +
				"remote locking attached. Stateless mode has neither. Remove the cloud block",
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
				"tofu-address marker (stateless/MARKERS.md), so renaming a resource means " +
				`rewriting that marker: run "choudoufu live-mv <old-address> <new-address>" ` +
				"(phase 3) and delete this block",
			Subject: moved.DeclRange,
		})
	}
}

// checkManagedResources runs the rules that apply to resource blocks:
// provisioners and their connection blocks, logical resource types, and the v0
// admission table.
func checkManagedResources(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	for _, resource := range mod.ManagedResources {
		addr := resource.Addr().String()

		checkProvisioners(resource, addr, path, issues)
		checkCountIndex(resource, addr, path, issues)

		// Type classification is managed-resources-only on purpose: a data
		// source stores nothing and is re-read every operation, so it has no
		// identity to recover and no admission question to answer.
		if prefix, ok := logicalType(resource.Type); ok {
			*issues = append(*issues, Issue{
				Rule:      RuleLogicalResource,
				Construct: addr,
				Module:    path,
				Detail: fmt.Sprintf(
					"%q is a logical resource (%s*): it has no existence outside the record "+
						"that OpenTofu keeps of it, so that record is the store stateless mode "+
						"removes. Nothing can recover its value from the live system, because "+
						"there is no live system holding it. Pass the value in as a variable or "+
						"a local, or read it from a resource that really exists",
					resource.Type, prefix,
				),
				Subject: resource.DeclRange,
			})
			// One verdict per resource: a logical type is already out, and
			// telling the author it is also missing from the admission table
			// would just be noise.
			continue
		}

		if !admitted(resource.Type) {
			*issues = append(*issues, Issue{
				Rule:      RuleUnadmittedType,
				Construct: addr,
				Module:    path,
				Detail: fmt.Sprintf(
					"resource type %q is not in the stateless v0 admission table. A type "+
						"participates only if its identity is recoverable from the live system "+
						"with no memory, by one of the four admission paths: client-assigned "+
						"identity, marker, parent-derived, or list plus content match. The v0 "+
						"table is hardcoded in internal/stateless/lint/admission.go and grows "+
						"with the provider survey and, later, provider identity schemas",
					resource.Type,
				),
				Subject: resource.DeclRange,
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
				"authority stateless mode gives up: the live system can say what exists, " +
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
				"provisioners are not available in stateless mode. Remove the connection block",
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
			Detail: "this data source reads a state file, and stateless mode has no state to " +
				"read. Pass the values across explicitly, as variables or outputs of a " +
				"module call, or read the live resource with a data source of its own type",
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
