// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// mustConfigResource parses a config-resource address for a test's want
// side, so an expectation is written the way an operator writes an address
// rather than as a struct literal.
func mustConfigResource(t *testing.T, s string) addrs.ConfigResource {
	t.Helper()
	target, diags := addrs.ParseTargetStr(s)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", s, diags.Err())
	}
	switch subject := target.Subject.(type) {
	case addrs.AbsResource:
		return addrs.ConfigResource{Module: subject.Module.Module(), Resource: subject.Resource}
	default:
		t.Fatalf("%q is not a whole-resource address", s)
		return addrs.ConfigResource{}
	}
}

// TestSelectionSelects is the membership rule, over both lists and over the
// shapes that must NOT match.
//
// The negative rows are the ones worth having. A selection withholds an
// ownership marker, so an over-match is a resource created unfindable, and
// every plausible way to write the comparison too loosely - comparing the
// local address and ignoring the module, comparing by prefix, ignoring the
// resource mode - has a row here.
func TestSelectionSelects(t *testing.T) {
	sel, problems := ParseSelection(
		[]string{"aws_ebs_volume"},
		[]string{"aws_instance.worker", "module.server.aws_instance.instance"},
	)
	if len(problems) != 0 {
		t.Fatalf("usable entries were refused: %v", problems)
	}

	for _, tc := range []struct {
		addr string
		want bool
	}{
		// By type, anywhere in the tree.
		{"aws_ebs_volume.data", true},
		{"module.server.aws_ebs_volume.scratch", true},

		// By address, exactly.
		{"aws_instance.worker", true},
		{"module.server.aws_instance.instance", true},

		// Same type, different name.
		{"aws_instance.other", false},
		// Same local address, different module. A comparison that dropped
		// the module path would match this.
		{"module.other.aws_instance.instance", false},
		// The root twin of a module-qualified entry.
		{"aws_instance.instance", false},
		// A name the selected one is a prefix of.
		{"aws_instance.worker_two", false},
		// A type the selected one is a prefix of.
		{"aws_ebs_volume_attachment.data", false},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			if got := sel.Selects(mustConfigResource(t, tc.addr)); got != tc.want {
				t.Errorf("Selects(%s) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// TestSelectionSelectsNoDataResource: a data resource carries no ownership
// marker and has no identity to record, so nothing here can be about one -
// including when its type happens to be one the types list names.
func TestSelectionSelectsNoDataResource(t *testing.T) {
	sel, _ := ParseSelection([]string{"aws_ebs_volume"}, nil)

	data := addrs.ConfigResource{
		Module: addrs.RootModule,
		Resource: addrs.Resource{
			Mode: addrs.DataResourceMode,
			Type: "aws_ebs_volume",
			Name: "existing",
		},
	}
	if sel.Selects(data) {
		t.Error("a data resource was selected; there is no marker on one to withhold")
	}
}

// TestSelectionNilAndEmpty: the zero states, which are what every
// configuration written before this block existed gets.
func TestSelectionNilAndEmpty(t *testing.T) {
	var nilSel *Selection
	if !nilSel.Empty() {
		t.Error("a nil selection is not Empty")
	}
	if nilSel.Selects(mustConfigResource(t, "aws_vpc.main")) {
		t.Error("a nil selection selected something")
	}

	empty, problems := ParseSelection(nil, nil)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if !empty.Empty() {
		t.Error("a selection built from two empty lists is not Empty")
	}
	if empty.Selects(mustConfigResource(t, "aws_vpc.main")) {
		t.Error("an empty selection selected something")
	}
}

// TestParseSelectionRefusals covers every address shape that cannot be used,
// and checks the message names the fix where there is one.
//
// The instance-key rows are the substantive ones. See this package's
// selection.go header: the selection's unit is the resource block because
// internal/live/stamp rewrites one shared configuration body per block, so a
// per-instance selection could only be honoured by unmarking the siblings.
func TestParseSelectionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"a resource instance key", "aws_instance.web[0]", `"aws_instance.web"`},
		{"a for_each instance key", `aws_instance.web["a"]`, `"aws_instance.web"`},
		{"a keyed module step", `module.net["a"].aws_subnet.this`, `"module.net.aws_subnet.this"`},
		{"a module with no resource", "module.net", "names a module, not a resource"},
		{"a data address", "data.aws_ami.ubuntu", "names a data resource"},
		{"a wildcard", "aws_instance.web[*]", "is not a resource address"},
		{"not an address at all", "not an address", "is not a resource address"},
		{"the empty string", "", "names no resource"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel, problems := ParseSelection(nil, []string{tc.raw})
			if len(problems) != 1 {
				t.Fatalf("got %d problems, want 1: %v", len(problems), problems)
			}
			if problems[0].Raw != tc.raw {
				t.Errorf("Raw = %q, want %q", problems[0].Raw, tc.raw)
			}
			if !strings.Contains(problems[0].Detail, tc.want) {
				t.Errorf("Detail does not contain %q:\n%s", tc.want, problems[0].Detail)
			}
			if !sel.Empty() {
				t.Errorf("a refused entry still selected something: %v", sel.Resources())
			}
		})
	}
}

// TestParseSelectionReportsEveryBadEntry: an operator fixing a list should
// see every entry that needs fixing, not one per run. The usable entries
// still build a selection, which is safe in the only direction that matters
// - internal/live/lint has already refused the configuration by then, and a
// dropped entry selects LESS, leaving its resource marked.
func TestParseSelectionReportsEveryBadEntry(t *testing.T) {
	sel, problems := ParseSelection(nil, []string{
		"aws_instance.web[0]",
		"aws_vpc.main",
		"module.net",
	})
	if len(problems) != 2 {
		t.Fatalf("got %d problems, want 2: %v", len(problems), problems)
	}
	if got, want := strings.Join(sel.Resources(), ","), "aws_vpc.main"; got != want {
		t.Errorf("Resources() = %q, want %q", got, want)
	}
}

// TestSelectionListsAreSortedAndDeduplicated: the two accessors feed
// diagnostics and a report, so two runs over one configuration have to print
// the same thing. Naming a resource in both lists is not an error either -
// it selects it once.
func TestSelectionListsAreSortedAndDeduplicated(t *testing.T) {
	sel, problems := ParseSelection(
		[]string{"aws_vpc", "aws_ebs_volume", "aws_vpc"},
		[]string{"aws_instance.b", "aws_instance.a", "aws_instance.b"},
	)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if got, want := strings.Join(sel.Types(), ","), "aws_ebs_volume,aws_vpc"; got != want {
		t.Errorf("Types() = %q, want %q", got, want)
	}
	if got, want := strings.Join(sel.Resources(), ","), "aws_instance.a,aws_instance.b"; got != want {
		t.Errorf("Resources() = %q, want %q", got, want)
	}
}

// TestImplementedWithSelection is the one difference between the two
// implemented-sets, pinned so that widening it is a deliberate edit rather
// than a table entry nobody reads.
//
// "never" is implemented with a selection because the selection is what
// gives an instance somewhere else to hold its identity - see [Never].
// "report" is not, with or without one, because nothing in this build
// reports a marker it declined to repair.
func TestImplementedWithSelection(t *testing.T) {
	for _, tc := range []struct {
		v                    MarkerRepair
		always, withSelected bool
	}{
		{Repair, true, true},
		{Report, false, false},
		{Never, false, true},
	} {
		t.Run(string(tc.v), func(t *testing.T) {
			if got := Implemented(tc.v); got != tc.always {
				t.Errorf("Implemented(%q) = %v, want %v", tc.v, got, tc.always)
			}
			if got := ImplementedWithSelection(tc.v); got != tc.withSelected {
				t.Errorf("ImplementedWithSelection(%q) = %v, want %v", tc.v, got, tc.withSelected)
			}
			if !Valid(tc.v) {
				t.Errorf("%q is not Valid", tc.v)
			}
		})
	}

	// ImplementedNames still reports only the unconditional set: a caller
	// rendering "settings this build implements" for a configuration with no
	// selection must not list one that needs one.
	if got, want := ImplementedNames(), `"repair"`; got != want {
		t.Errorf("ImplementedNames() = %s, want %s", got, want)
	}
}
