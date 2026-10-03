// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
)

func raw(t testing.TB, v any) json.RawMessage {
	t.Helper()
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func change(t testing.TB, addr string, actions []string, before, after map[string]any) ResourceChange {
	t.Helper()
	rc := ResourceChange{Address: addr, Change: Change{Actions: actions}}
	if before != nil {
		rc.Change.Before = raw(t, before)
	}
	if after != nil {
		rc.Change.After = raw(t, after)
	}
	return rc
}

var (
	create  = []string{"create"}
	update  = []string{"update"}
	replace = []string{"delete", "create"}
	destroy = []string{"delete"}
)

func root(dir, estate string, changes ...ResourceChange) SetRoot {
	return SetRoot{Root: dir, Estate: estate, Status: "planned", Plan: &Plan{FormatVersion: "1.2", ResourceChanges: changes}}
}

// tableRow is one pair of roots and whether the summary must put them in
// one group.
type tableRow struct {
	name  string
	a, b  SetRoot
	group bool
}

func normalizationTable(t testing.TB) []tableRow {
	instance := func(estate, id, size string) ResourceChange {
		return change(t, "aws_instance.web", update,
			map[string]any{"id": id, "arn": "arn:aws:ec2:us-east-1:111:instance/" + id, "instance_type": "t3.small"},
			map[string]any{"id": id, "arn": "arn:aws:ec2:us-east-1:111:instance/" + id, "instance_type": size})
	}
	bucket := func(addr string, attrs map[string]any) ResourceChange {
		return change(t, addr, create, nil, attrs)
	}
	tagged := func(addr, estate string, extra map[string]any) map[string]any {
		tags := map[string]any{markers.TagEstate: estate, markers.TagAddress: markers.EscapeAddress(addr)}
		for k, v := range extra {
			tags[k] = v
		}
		return map[string]any{"bucket": "central-logs", "tags": tags}
	}
	policy := func(action, estate string) ResourceChange {
		doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"` + action + `","Resource":"arn:aws:s3:::` + estate + `-data/*"}]}`
		return change(t, "aws_iam_policy.read", create, nil, map[string]any{"name": estate + "-read", "policy": doc})
	}

	return []tableRow{
		// Must group: the two roots differ only in what names them.
		{
			name:  "server-assigned identifier only",
			a:     root("roots/acme", "acme", instance("acme", "i-0aaa111", "t3.medium")),
			b:     root("roots/globex", "globex", instance("globex", "i-0bbb222", "t3.medium")),
			group: true,
		},
		{
			name:  "estate name in the tofu-estate marker",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs", tagged("aws_s3_bucket.logs", "acme", nil))),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs", tagged("aws_s3_bucket.logs", "globex", nil))),
			group: true,
		},
		{
			name:  "estate name in a value",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "acme-logs"})),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "globex-logs"})),
			group: true,
		},
		{
			name:  "resource name carries the estate",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.acme_logs", map[string]any{"bucket": "central-logs"})),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.globex_logs", map[string]any{"bucket": "central-logs"})),
			group: true,
		},
		{
			name:  "tag value carrying the estate",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "central-logs", "tags": map[string]any{"Owner": "team-acme"}})),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "central-logs", "tags": map[string]any{"Owner": "team-globex"}})),
			group: true,
		},
		{
			name:  "address index, with the tofu-address marker of each",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs[0]", tagged("aws_s3_bucket.logs[0]", "acme", nil))),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs[1]", tagged("aws_s3_bucket.logs[1]", "globex", nil))),
			group: true,
		},
		{
			name:  "address key, with the tofu-address marker of each",
			a:     root("roots/acme", "acme", bucket(`aws_s3_bucket.logs["acme"]`, tagged(`aws_s3_bucket.logs["acme"]`, "acme", nil))),
			b:     root("roots/globex", "globex", bucket(`aws_s3_bucket.logs["globex"]`, tagged(`aws_s3_bucket.logs["globex"]`, "globex", nil))),
			group: true,
		},
		{
			name:  "address module prefix",
			a:     root("roots/acme", "acme", bucket("module.acme.aws_s3_bucket.logs", map[string]any{"bucket": "central-logs"})),
			b:     root("roots/globex", "globex", bucket("module.globex.aws_s3_bucket.logs", map[string]any{"bucket": "central-logs"})),
			group: true,
		},
		{
			name:  "root directory name in a value",
			a:     root("roots/eu-west", "acme", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "eu-west-logs"})),
			b:     root("roots/us-east", "globex", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "us-east-logs"})),
			group: true,
		},

		// Must not group: a real difference, however small.
		{
			name:  "different instance size",
			a:     root("roots/acme", "acme", instance("acme", "i-0aaa111", "t3.medium")),
			b:     root("roots/globex", "globex", instance("globex", "i-0bbb222", "t3.large")),
			group: false,
		},
		{
			name:  "different CIDR",
			a:     root("roots/acme", "acme", bucket("aws_subnet.a", map[string]any{"cidr_block": "10.1.0.0/16"})),
			b:     root("roots/globex", "globex", bucket("aws_subnet.a", map[string]any{"cidr_block": "10.2.0.0/16"})),
			group: false,
		},
		{
			name:  "different policy document",
			a:     root("roots/acme", "acme", policy("s3:GetObject", "acme")),
			b:     root("roots/globex", "globex", policy("s3:*", "globex")),
			group: false,
		},
		{
			name:  "different tag value that does not carry the estate",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "central-logs", "tags": map[string]any{"Env": "prod"}})),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "central-logs", "tags": map[string]any{"Env": "dev"}})),
			group: false,
		},
		{
			name:  "a tofu-estate marker naming some other estate",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs", tagged("aws_s3_bucket.logs", "acme", nil))),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs", tagged("aws_s3_bucket.logs", "acme", nil))),
			group: false,
		},
		{
			name: "update against replace of the same attribute",
			a:    root("roots/acme", "acme", instance("acme", "i-0aaa111", "t3.medium")),
			b: func() SetRoot {
				r := root("roots/globex", "globex", instance("globex", "i-0bbb222", "t3.medium"))
				r.Plan.ResourceChanges[0].Change.Actions = replace
				return r
			}(),
			group: false,
		},
		{
			name:  "the estate name inside a longer word is not the estate",
			a:     root("roots/acme", "acme", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "acmelogs"})),
			b:     root("roots/globex", "globex", bucket("aws_s3_bucket.logs", map[string]any{"bucket": "globexlogs"})),
			group: false,
		},
	}
}

// grouper says whether two roots land in one group.
type grouper func(a, b SetRoot) bool

func realGrouper(a, b SetRoot) bool {
	s := Summarize(Input{Set: &SetDocument{Roots: []SetRoot{a, b}}})
	return len(s.Failed) == 0 && len(s.Groups) == 1
}

// naiveGrouper is grouping by a hash of the raw change set: equal bytes or
// nothing.
func naiveGrouper(a, b SetRoot) bool {
	ra, _ := json.Marshal(a.Plan.ResourceChanges)
	rb, _ := json.Marshal(b.Plan.ResourceChanges)
	return string(ra) == string(rb)
}

// overEagerGrouper is the normalizer this package must not be: it blanks
// every string value before grouping, which groups everything that differs
// only in a string.
func overEagerGrouper(a, b SetRoot) bool {
	return realGrouper(blankStrings(a), blankStrings(b))
}

func blankStrings(r SetRoot) SetRoot {
	var blank func(v any) any
	blank = func(v any) any {
		switch t := v.(type) {
		case string:
			return "x"
		case []any:
			for i := range t {
				t[i] = blank(t[i])
			}
		case map[string]any:
			for k := range t {
				t[k] = blank(t[k])
			}
		}
		return v
	}
	plan := *r.Plan
	plan.ResourceChanges = nil
	for _, rc := range r.Plan.ResourceChanges {
		for _, side := range []*json.RawMessage{&rc.Change.Before, &rc.Change.After} {
			if v := decode(*side); v != nil {
				*side, _ = json.Marshal(blank(v))
			}
		}
		plan.ResourceChanges = append(plan.ResourceChanges, rc)
	}
	r.Plan = &plan
	return r
}

func checkTable(t *testing.T, g grouper) {
	for _, row := range normalizationTable(t) {
		if got := g(row.a, row.b); got != row.group {
			t.Errorf("%s: grouped=%v, want %v", row.name, got, row.group)
		}
	}
}

// TestNormalizationTable is #1753's normalization table against the real
// summary.
func TestNormalizationTable(t *testing.T) {
	checkTable(t, realGrouper)
}

// TestNaiveGroupingFailsEveryMustGroupRow keeps the table's red-first proof:
// every must-group row is a pair a raw hash keeps apart, so none of them
// passes by accident of identical input.
func TestNaiveGroupingFailsEveryMustGroupRow(t *testing.T) {
	for _, row := range normalizationTable(t) {
		if row.group && naiveGrouper(row.a, row.b) {
			t.Errorf("%s: a raw hash already groups this pair, so the row proves nothing about normalization", row.name)
		}
	}
}

// TestOverEagerNormalizerFailsMustNotRows is the other direction: a
// normalizer that strips every string falsely groups the must-not rows
// whose difference is a string. If this stops failing them, the table no
// longer guards against blanket stripping.
func TestOverEagerNormalizerFailsMustNotRows(t *testing.T) {
	falselyGrouped := map[string]bool{}
	for _, row := range normalizationTable(t) {
		if !row.group && overEagerGrouper(row.a, row.b) {
			falselyGrouped[row.name] = true
		}
	}
	want := map[string]bool{
		"different instance size":                                true,
		"different CIDR":                                         true,
		"different policy document":                              true,
		"different tag value that does not carry the estate":     true,
		"a tofu-estate marker naming some other estate":          true,
		"the estate name inside a longer word is not the estate": true,
	}
	if !reflect.DeepEqual(falselyGrouped, want) {
		t.Errorf("an over-eager normalizer falsely grouped %v, want %v", falselyGrouped, want)
	}
}

// TestPlanInstanceTable is the same rule over one plan's for_each/count
// expansion, where an instance's own key is its name.
func TestPlanInstanceTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a, b  ResourceChange
		group bool
	}{
		{
			name:  "count index in a name",
			a:     change(t, "aws_instance.web[0]", create, nil, map[string]any{"name": "web-0", "instance_type": "t3.small"}),
			b:     change(t, "aws_instance.web[1]", create, nil, map[string]any{"name": "web-1", "instance_type": "t3.small"}),
			group: true,
		},
		{
			name:  "for_each key in a tag",
			a:     change(t, `aws_instance.web["blue"]`, create, nil, map[string]any{"tags": map[string]any{"Name": "web-blue"}}),
			b:     change(t, `aws_instance.web["green"]`, create, nil, map[string]any{"tags": map[string]any{"Name": "web-green"}}),
			group: true,
		},
		{
			name:  "count index inside a CIDR is not a name",
			a:     change(t, "aws_subnet.a[0]", create, nil, map[string]any{"cidr_block": "10.0.0.0/24"}),
			b:     change(t, "aws_subnet.a[1]", create, nil, map[string]any{"cidr_block": "10.1.0.0/24"}),
			group: false,
		},
		{
			name:  "different instance size",
			a:     change(t, "aws_instance.web[0]", create, nil, map[string]any{"name": "web-0", "instance_type": "t3.small"}),
			b:     change(t, "aws_instance.web[1]", create, nil, map[string]any{"name": "web-1", "instance_type": "t3.large"}),
			group: false,
		},
		{
			name:  "two different resources never share a group",
			a:     change(t, "aws_instance.web[0]", create, nil, map[string]any{"instance_type": "t3.small"}),
			b:     change(t, "aws_instance.api[0]", create, nil, map[string]any{"instance_type": "t3.small"}),
			group: false,
		},
	} {
		s := Summarize(Input{Plan: &Plan{ResourceChanges: []ResourceChange{tc.a, tc.b}}})
		if got := len(s.Groups) == 1; got != tc.group {
			t.Errorf("%s: grouped=%v, want %v (%d groups)", tc.name, got, tc.group, len(s.Groups))
		}
	}
}
