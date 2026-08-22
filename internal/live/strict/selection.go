// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
)

// This file is the second of HANDOFF.md's three principles-as-toggles:
// "per-type or per-address markers = record, for tag budgets and tag
// policies, trading IAM governability for a record-held identity".
//
// The trade is the whole point and it is worth stating before the code. An
// ownership marker is a tag, and a tag is what an aws:ResourceTag condition
// governs, what a cost report groups by, and what any other tool can list
// without knowing this one exists. A record-held identity is none of those:
// it is a key in the estate's own store, visible to this tool and to
// whoever can read the store. An operator who selects a resource here is
// buying a tag back - because the account has a tag budget, or a tag policy
// forbids the keys - and paying for it in governability. This package's job
// is only to say WHICH resources they selected; internal/live/lint decides
// whether the trade is one this build can actually make for them, because
// that question needs the provider's schemas.
//
// # Why the unit is the resource block and not the instance
//
// internal/live/stamp rewrites CONFIGURATION: one HCL body serves every
// instance a count or for_each block expands to, and the tofu-address it
// injects into that body is a template over count.index or each.key. There
// is no way to write a marker for instance 1 and not for instance 0, so a
// selection whose unit were the instance could only be honoured by
// unmarking the siblings - which would create them unfindable, the exact
// failure the safety rule exists to prevent - or by ignoring the selection,
// which would tell an operator their tags were spared when they were not.
//
// So the unit is [addrs.ConfigResource]: a module PATH with no instance
// keys, and a resource with no instance key. Every consumer reduces to that
// form before asking, and internal/live/lint refuses an address written
// with an instance key rather than silently widening it.

// Selection is the set of resources one `markers "record"` block covers:
// resources whose identity lives in the estate's record store instead of in
// an ownership marker tag.
//
// The zero value, and a nil *Selection, select nothing. That is what every
// configuration written before this block existed gets, and it is what makes
// HANDOFF.md's "compatible out of the box" true here by construction rather
// than by review.
type Selection struct {
	types     map[string]struct{}
	resources map[string]struct{}
}

// Empty reports whether s narrows nothing: no type and no address.
//
// A `markers "record" {}` block naming neither is indistinguishable from no
// block at all, exactly as an empty scope block is indistinguishable from no
// scope block in a policy (see [configs.LivePolicyScope] and
// internal/live/lint's scopeIsSet), so internal/live/lint refuses it rather
// than reading it as a selection of everything or of nothing.
func (s *Selection) Empty() bool {
	return s == nil || (len(s.types) == 0 && len(s.resources) == 0)
}

// SelectsType reports whether the selection names resourceType outright, so
// that every resource of that type in the configuration is covered.
func (s *Selection) SelectsType(resourceType string) bool {
	if s == nil {
		return false
	}
	_, ok := s.types[resourceType]
	return ok
}

// Selects reports whether addr is covered, by its type or by its address.
//
// addr is the CONFIG resource - a module path with no instance keys - for
// the reason this file's header gives. A caller holding an
// [addrs.AbsResourceInstance] reduces with its ConfigResource method, which
// drops both the module instance keys and the resource instance key; that
// widening is deliberate and is the same block-level coarsening
// internal/live/stamp's PolicyUntag already documents.
func (s *Selection) Selects(addr addrs.ConfigResource) bool {
	if s == nil {
		return false
	}
	if addr.Resource.Mode != addrs.ManagedResourceMode {
		// A data resource has no ownership marker to withhold and no
		// identity to record: nothing here can be about one.
		return false
	}
	if s.SelectsType(addr.Resource.Type) {
		return true
	}
	_, ok := s.resources[addr.String()]
	return ok
}

// Types is the type list this selection was built from, sorted, for a
// diagnostic or a report.
func (s *Selection) Types() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.types))
	for t := range s.types {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Resources is the address list this selection was built from, sorted and
// rendered in the canonical [addrs.ConfigResource] spelling.
func (s *Selection) Resources() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.resources))
	for a := range s.resources {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// AddressProblem is one entry of a `markers "record"` block's addresses list
// that cannot be used, with the sentence saying why.
//
// It is a value rather than a diagnostic because this package holds no
// source ranges: internal/configs recorded where the list was written and
// internal/live/lint turns these into [lint.Issue]s pointing at it. The Raw
// field is what the author typed, so the message can quote it back.
type AddressProblem struct {
	// Raw is the string as written in the configuration.
	Raw string

	// Detail is one sentence naming what is wrong with it, aimed at an
	// operator and phrased as the fix where there is one.
	Detail string
}

// ParseSelection builds a [Selection] from the two literal lists a
// `markers "record"` block carries.
//
// Types are taken as written: whether a name is a real provider resource
// type is a question for the schemas, which this package does not hold, and
// a type nothing declares selects nothing anyway.
//
// Addresses go through [addrs.ParseTargetStr], the `-target` grammar, which
// gives module qualification and rejects wildcards for free. Three further
// shapes are refused here, each because honouring it would mean guessing:
//
//   - A module address with no resource ("module.net"). Selecting every
//     resource under a module is a wider blast radius than either list
//     names, and widening a marker-withholding selection by guess is the
//     wrong direction.
//   - A data address. There is no marker on one and no identity to record.
//   - An instance key, on the resource or on any module step
//     ("aws_instance.web[0]", "module.net[\"a\"].aws_subnet.this"). See
//     this file's header: the unit is the block, and a per-instance
//     selection cannot be honoured by a pass that rewrites one shared body.
//
// Every problem is reported and the usable entries still build a selection,
// so an operator fixing a list sees every entry that needs fixing rather
// than one per run. A caller that must not act on a partial selection - the
// resolver, the stamp pass - checks that the problems slice is empty, which
// internal/live/lint has already turned into refusals by the time either
// runs.
func ParseSelection(types, addresses []string) (*Selection, []AddressProblem) {
	s := &Selection{
		types:     make(map[string]struct{}, len(types)),
		resources: make(map[string]struct{}, len(addresses)),
	}
	for _, t := range types {
		if t == "" {
			continue
		}
		s.types[t] = struct{}{}
	}

	var problems []AddressProblem
	for _, raw := range addresses {
		addr, problem := parseSelectionAddress(raw)
		if problem != nil {
			problems = append(problems, *problem)
			continue
		}
		s.resources[addr.String()] = struct{}{}
	}
	return s, problems
}

// parseSelectionAddress reads one addresses entry, returning the config
// resource it names or the reason it cannot be used. See [ParseSelection]
// for the three shapes refused beyond the target grammar's own.
func parseSelectionAddress(raw string) (addrs.ConfigResource, *AddressProblem) {
	reject := func(detail string) (addrs.ConfigResource, *AddressProblem) {
		return addrs.ConfigResource{}, &AddressProblem{Raw: raw, Detail: detail}
	}

	if raw == "" {
		return reject("an empty string names no resource.")
	}

	target, diags := addrs.ParseTargetStr(raw)
	if diags.HasErrors() {
		return reject(fmt.Sprintf(
			"%q is not a resource address: %s. Write it the way -target takes one - \"aws_instance.web\", or \"module.net.aws_subnet.this\".",
			raw, diags.Err(),
		))
	}

	switch subject := target.Subject.(type) {
	case addrs.AbsResource:
		if subject.Resource.Mode != addrs.ManagedResourceMode {
			return reject(fmt.Sprintf(
				"%q names a data resource. A data resource carries no ownership marker and has no identity to record, so there is nothing here to select.",
				raw,
			))
		}
		if keyed, step := moduleInstanceKeyed(subject.Module); keyed {
			return reject(fmt.Sprintf(
				"%q names one instance of module call %q. The selection's unit is the resource BLOCK: one configuration body serves every instance a module call expands to, so a marker cannot be withheld from one of them and written for the rest. Name the module call without a key - %q.",
				raw, step, addrs.ConfigResource{Module: subject.Module.Module(), Resource: subject.Resource}.String(),
			))
		}
		return addrs.ConfigResource{Module: subject.Module.Module(), Resource: subject.Resource}, nil

	case addrs.AbsResourceInstance:
		whole := addrs.ConfigResource{Module: subject.Module.Module(), Resource: subject.Resource.Resource}
		return reject(fmt.Sprintf(
			"%q names one instance of a resource. The selection's unit is the resource BLOCK: one configuration body serves every instance a count or for_each expands to, and the tofu-address written into it is a template over count.index or each.key, so a marker cannot be withheld from one instance and written for its siblings. Name the whole resource - %q - or split the instance you mean into a resource block of its own.",
			raw, whole.String(),
		))

	case addrs.ModuleInstance, addrs.Module:
		return reject(fmt.Sprintf(
			"%q names a module, not a resource. Selecting every resource under a module would withhold markers from resources this list never named; name each resource - \"%s.aws_instance.web\".",
			raw, raw,
		))

	default:
		return reject(fmt.Sprintf("%q is not a resource address.", raw))
	}
}

// moduleInstanceKeyed reports whether any step of a module instance path
// carries an instance key, and names the first one that does.
func moduleInstanceKeyed(mi addrs.ModuleInstance) (bool, string) {
	for _, step := range mi {
		if step.InstanceKey != addrs.NoKey {
			return true, step.Name
		}
	}
	return false, ""
}
