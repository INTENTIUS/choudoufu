// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package approval is the comparison behind GitHub issue #878's approval
// gate: does the plan an apply is about to execute say the same thing as the
// plan a human approved?
//
// It exists because a pipeline runs Terraform as "plan on the pull request,
// a human approves, apply exactly what was approved", and under live markers
// an apply plans against the live system at the moment it runs. Both halves
// of that are kept: the world stays authoritative and an apply still reads
// it, AND the artifact the approval crossed on is compared against what the
// fresh read produced. Where they differ the apply refuses; it never
// executes the saved plan instead.
//
// Nothing here reads a cloud, a record store or a provider. It takes two
// finished plans and produces rows, so the refusal it feeds can be asserted
// by value rather than by a boolean. The wording of that refusal lives in
// internal/command beside the other command-invocation guards (see
// internal/command/live_approval.go), which is where this repository puts
// them - internal/live/check's check_test.go records the same rule for
// internal/live/markerstrip.
package approval

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
)

// Row is one resource-instance change, reduced to the three things the
// approval covers: which instance, what the apply will do to it, and which
// live object it computed that against.
//
// Attribute values are deliberately NOT part of a row. The approval question
// this answers is "same resources, same actions, same live objects"; value
// equality is a strictly larger claim, and one this cannot make honestly
// while a plan legitimately carries unknowns. See the design comment on
// GitHub issue #878.
type Row struct {
	// Address is the absolute resource instance address, with the deposed
	// key appended when the change is about a deposed object rather than the
	// current one - two different changes can share an address otherwise.
	Address string

	// Action is the plan action, spelled as plans.Action does: Create,
	// Update, Delete, DeleteThenCreate, CreateThenDelete and so on.
	Action string

	// Identity is the identity of the live object this change was computed
	// against - the "id" attribute of the instance's object in the plan's
	// prior state. It is IdentityNone for a create, which has no prior
	// object, and IdentityUnknown when a prior object exists but does not
	// carry a readable string id.
	Identity string
}

const (
	// IdentityNone is [Row.Identity] for a change with no prior object.
	IdentityNone = "-"

	// IdentityUnknown is [Row.Identity] when a prior object exists but its
	// id could not be read as a string. It is a distinct value rather than
	// IdentityNone so that "there was no object" and "there was an object
	// this cannot name" never compare equal.
	IdentityUnknown = "?"
)

// String renders a row the way the refusal prints it and the way the
// comparison keys it. Two rows are the same change exactly when their
// String values match.
func (r Row) String() string {
	return fmt.Sprintf("%s  %s  %s", r.Address, r.Action, r.Identity)
}

// ChangeSet is the approval-relevant view of a finished plan: one row per
// managed resource instance the apply will actually act on, sorted.
//
// Two things are left out on purpose, and both are the old coverage rule
// re-derived rather than copied: a data source read, which describes the
// world rather than changing it, and a no-op, which the apply does not
// execute. Anything left out cannot make two plans compare equal that a
// reader would call different, because a resource moving in or out of the
// acted-on set changes the rows either way.
func ChangeSet(plan *plans.Plan) []Row {
	if plan == nil || plan.Changes == nil {
		return nil
	}
	rows := make([]Row, 0, len(plan.Changes.Resources))
	for _, change := range plan.Changes.Resources {
		if change == nil {
			continue
		}
		if change.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if change.Action == plans.NoOp {
			continue
		}
		rows = append(rows, Row{
			Address:  renderAddress(change.Addr, change.DeposedKey),
			Action:   change.Action.String(),
			Identity: priorIdentity(plan.PriorState, change.Addr, change.DeposedKey),
		})
	}
	Sort(rows)
	return rows
}

// Sort orders rows by their rendered form, so that the order two plans
// happened to walk the graph in can never be a difference.
func Sort(rows []Row) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].String() < rows[j].String() })
}

// renderAddress is [Row.Address]: the instance address, plus the deposed key
// when there is one.
func renderAddress(addr addrs.AbsResourceInstance, deposed states.DeposedKey) string {
	if deposed == states.NotDeposed {
		return addr.String()
	}
	return fmt.Sprintf("%s (deposed %s)", addr.String(), deposed)
}

// priorIdentity reads the id of the object a change was computed against.
//
// It reads the attribute JSON directly rather than decoding against a
// provider schema, because the caller that matters most - reading a saved
// plan file before any provider has been launched - has no schemas in hand,
// and an identity that could only be read on one side of the comparison
// would be no comparison at all.
func priorIdentity(prior *states.State, addr addrs.AbsResourceInstance, deposed states.DeposedKey) string {
	if prior == nil {
		return IdentityNone
	}
	is := prior.ResourceInstance(addr)
	if is == nil {
		return IdentityNone
	}
	var obj *states.ResourceInstanceObjectSrc
	if deposed == states.NotDeposed {
		obj = is.Current
	} else {
		obj = is.Deposed[deposed]
	}
	if obj == nil || len(obj.AttrsJSON) == 0 {
		return IdentityNone
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(obj.AttrsJSON, &attrs); err != nil {
		return IdentityUnknown
	}
	raw, ok := attrs["id"]
	if !ok {
		return IdentityUnknown
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil || id == "" {
		return IdentityUnknown
	}
	return id
}

// Difference is what two change sets disagree about, as rows rather than as
// a count or a verdict, so that the refusal can name them.
type Difference struct {
	// Extra are rows the fresh plan has that the approved artifact does not:
	// what the apply would do that nobody approved.
	Extra []Row

	// Missing are rows the approved artifact has that the fresh plan does
	// not: what was approved and would not happen.
	Missing []Row
}

// Empty reports whether the two change sets are the same one.
func (d Difference) Empty() bool { return len(d.Extra) == 0 && len(d.Missing) == 0 }

// Compare diffs an approved change set against a fresh one as multisets of
// rendered rows, so that ordering cannot differ and a duplicate row cannot
// hide behind an identical one.
func Compare(approved, fresh []Row) Difference {
	counts := make(map[string]int, len(approved)+len(fresh))
	byKey := make(map[string]Row, len(approved)+len(fresh))
	for _, r := range approved {
		counts[r.String()]--
		byKey[r.String()] = r
	}
	for _, r := range fresh {
		counts[r.String()]++
		byKey[r.String()] = r
	}

	var d Difference
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n := counts[k]
		for i := 0; i < n; i++ {
			d.Extra = append(d.Extra, byKey[k])
		}
		for i := 0; i < -n; i++ {
			d.Missing = append(d.Missing, byKey[k])
		}
	}
	return d
}
