// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package approval

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
)

// fakePlan builds a plan out of nothing but addresses and actions, so that
// every assertion below is about the rendering rather than about a provider.
func fakePlan(t *testing.T, prior map[string]string, changes ...*plans.ResourceInstanceChangeSrc) *plans.Plan {
	t.Helper()
	state := states.NewState()
	root := state.RootModule()
	for addrStr, id := range prior {
		addr := mustAddr(t, addrStr)
		root.SetResourceInstanceCurrent(
			addr.Resource,
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"id":"` + id + `"}`),
			},
			addrs.AbsProviderConfig{
				Module:   addrs.RootModule,
				Provider: addrs.NewDefaultProvider("aws"),
			},
			addrs.NoKey,
		)
	}
	return &plans.Plan{
		PriorState: state,
		Changes:    &plans.Changes{Resources: changes},
	}
}

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad address %q in the test itself: %s", s, diags.Err())
	}
	return addr
}

func change(t *testing.T, addrStr string, action plans.Action) *plans.ResourceInstanceChangeSrc {
	t.Helper()
	addr := mustAddr(t, addrStr)
	return &plans.ResourceInstanceChangeSrc{
		Addr:        addr,
		PrevRunAddr: addr,
		ChangeSrc:   plans.ChangeSrc{Action: action},
	}
}

// TestChangeSet_rendersActionsAndIdentities asserts the rendered rows by
// value. The whole approval gate is this rendering: a test that only checked
// "the sets are equal" would pass just as happily over a renderer that
// printed the same constant for every instance.
func TestChangeSet_rendersActionsAndIdentities(t *testing.T) {
	plan := fakePlan(t,
		map[string]string{
			"aws_vpc.main":       "vpc-owned",
			"aws_s3_bucket.data": "tofu-data",
		},
		change(t, "aws_vpc.main", plans.Update),
		change(t, "aws_s3_bucket.data", plans.Delete),
		change(t, "aws_cloudwatch_log_group.app", plans.Create),
		// Left out of the rows below: a data source read describes the
		// world rather than changing it, and a no-op is not something the
		// apply executes.
		change(t, "data.aws_ami.latest", plans.Read),
		change(t, "aws_vpc.untouched", plans.NoOp),
	)

	got := ChangeSet(plan)
	want := []string{
		"aws_cloudwatch_log_group.app  Create  -",
		"aws_s3_bucket.data  Delete  tofu-data",
		"aws_vpc.main  Update  vpc-owned",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%s", len(got), len(want), renderRows(got))
	}
	for i, w := range want {
		if got[i].String() != w {
			t.Errorf("row %d rendered %q, want %q", i, got[i].String(), w)
		}
	}
}

// TestChangeSet_deposedObjectsAreDistinctRows: a deposed object and the
// current object at the same address are two different changes, so they must
// not collapse into one row.
func TestChangeSet_deposedObjectsAreDistinctRows(t *testing.T) {
	addr := mustAddr(t, "aws_instance.web")
	plan := &plans.Plan{
		PriorState: states.NewState(),
		Changes: &plans.Changes{Resources: []*plans.ResourceInstanceChangeSrc{
			{Addr: addr, PrevRunAddr: addr, ChangeSrc: plans.ChangeSrc{Action: plans.Update}},
			{Addr: addr, PrevRunAddr: addr, DeposedKey: states.DeposedKey("abcd1234"), ChangeSrc: plans.ChangeSrc{Action: plans.Delete}},
		}},
	}
	got := renderRows(ChangeSet(plan))
	for _, want := range []string{
		"aws_instance.web  Update  -",
		"aws_instance.web (deposed abcd1234)  Delete  -",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the change set does not carry %q:\n%s", want, got)
		}
	}
}

// TestCompare_match is the case that must NOT refuse: the same changes, in a
// different order, over the same live objects.
func TestCompare_match(t *testing.T) {
	approved := []Row{
		{Address: "aws_vpc.main", Action: "Update", Identity: "vpc-owned"},
		{Address: "aws_s3_bucket.data", Action: "Create", Identity: IdentityNone},
	}
	fresh := []Row{
		{Address: "aws_s3_bucket.data", Action: "Create", Identity: IdentityNone},
		{Address: "aws_vpc.main", Action: "Update", Identity: "vpc-owned"},
	}
	if diff := Compare(approved, fresh); !diff.Empty() {
		t.Errorf("ordering alone was read as a difference:\nextra:   %s\nmissing: %s",
			renderRows(diff.Extra), renderRows(diff.Missing))
	}
}

// TestCompare_drift walks the three ways a fresh plan can disagree, and
// asserts the rows the refusal will print rather than a boolean.
func TestCompare_drift(t *testing.T) {
	base := []Row{{Address: "aws_vpc.main", Action: "Update", Identity: "vpc-owned"}}

	t.Run("a change nobody approved", func(t *testing.T) {
		fresh := append([]Row{{Address: "aws_subnet.crashed", Action: "Delete", Identity: "subnet-99"}}, base...)
		diff := Compare(base, fresh)
		if renderRows(diff.Extra) != "aws_subnet.crashed  Delete  subnet-99" {
			t.Errorf("extra rows rendered %q", renderRows(diff.Extra))
		}
		if len(diff.Missing) != 0 {
			t.Errorf("missing rows should be empty, got %q", renderRows(diff.Missing))
		}
	})

	t.Run("an approved change that would no longer happen", func(t *testing.T) {
		diff := Compare(base, nil)
		if renderRows(diff.Missing) != "aws_vpc.main  Update  vpc-owned" {
			t.Errorf("missing rows rendered %q", renderRows(diff.Missing))
		}
	})

	t.Run("the same address against a different live object", func(t *testing.T) {
		fresh := []Row{{Address: "aws_vpc.main", Action: "Update", Identity: "vpc-replaced"}}
		diff := Compare(base, fresh)
		if renderRows(diff.Extra) != "aws_vpc.main  Update  vpc-replaced" {
			t.Errorf("extra rows rendered %q", renderRows(diff.Extra))
		}
		if renderRows(diff.Missing) != "aws_vpc.main  Update  vpc-owned" {
			t.Errorf("missing rows rendered %q", renderRows(diff.Missing))
		}
	})

	t.Run("the same address with a different action", func(t *testing.T) {
		fresh := []Row{{Address: "aws_vpc.main", Action: "DeleteThenCreate", Identity: "vpc-owned"}}
		diff := Compare(base, fresh)
		if renderRows(diff.Extra) != "aws_vpc.main  DeleteThenCreate  vpc-owned" {
			t.Errorf("extra rows rendered %q", renderRows(diff.Extra))
		}
	})
}

// TestCompare_duplicateRowsAreCounted: two identical rows are two changes.
// A set-based comparison would call one of them equal to two of them.
func TestCompare_duplicateRowsAreCounted(t *testing.T) {
	row := Row{Address: "aws_instance.web[0]", Action: "Update", Identity: "i-1"}
	diff := Compare([]Row{row}, []Row{row, row})
	if renderRows(diff.Extra) != "aws_instance.web[0]  Update  i-1" {
		t.Errorf("a duplicated change was not reported once: %q", renderRows(diff.Extra))
	}
}

func renderRows(rows []Row) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.String())
	}
	return strings.Join(out, "\n")
}
