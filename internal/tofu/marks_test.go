// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/lang/marks"
)

func TestMarksEqual(t *testing.T) {
	for i, tc := range []struct {
		a, b  []cty.PathValueMarks
		equal bool
	}{
		{
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			true,
		},
		{
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "A"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			false,
		},
		{
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "b"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "c"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "b"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "c"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			true,
		},
		{
			[]cty.PathValueMarks{
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "c"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "c"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
			},
			true,
		},
		{
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "b"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			false,
		},
		{
			nil,
			nil,
			true,
		},
		{
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			nil,
			false,
		},
		{
			nil,
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			false,
		},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if marksEqual(tc.a, tc.b) != tc.equal {
				t.Fatalf("marksEqual(\n%#v,\n%#v,\n) != %t\n", tc.a, tc.b, tc.equal)
			}
		})
	}
}

func TestCombinePathValueMarks(t *testing.T) {
	paths := map[string]cty.PathValueMarks{
		"a.b": {
			Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
			Marks: cty.NewValueMarks(marks.Sensitive),
		},
		"a.c": {
			Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "c"}},
			Marks: cty.NewValueMarks(marks.Sensitive),
		},
		"[0]": {
			Path:  cty.Path{cty.IndexStep{Key: cty.NumberIntVal(0)}},
			Marks: cty.NewValueMarks("a"),
		},
		"a.b<alt>": {
			Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
			Marks: cty.NewValueMarks("a"),
		},
	}

	tests := []struct {
		name string
		LHS  []cty.PathValueMarks
		RHS  []cty.PathValueMarks
		Want []cty.PathValueMarks
	}{
		{
			name: "no marks",
			LHS:  []cty.PathValueMarks{},
			RHS:  []cty.PathValueMarks{},
			Want: []cty.PathValueMarks{},
		},
		{
			name: "one mark",
			LHS:  []cty.PathValueMarks{paths["a.b"]},
			RHS:  []cty.PathValueMarks{},
			Want: []cty.PathValueMarks{paths["a.b"]},
		},
		{
			name: "one overlapping mark",
			LHS:  []cty.PathValueMarks{paths["a.b"]},
			RHS:  []cty.PathValueMarks{paths["a.b"]},
			Want: []cty.PathValueMarks{paths["a.b"]},
		},
		{
			name: "one non-overlapping mark",
			LHS:  []cty.PathValueMarks{paths["a.b"]},
			RHS:  []cty.PathValueMarks{paths["a.c"]},
			Want: []cty.PathValueMarks{paths["a.b"], paths["a.c"]},
		},
		{
			name: "one overlapping and two non-overlapping marks",
			LHS:  []cty.PathValueMarks{paths["a.b"], paths["a.c"], paths["[0]"]},
			RHS:  []cty.PathValueMarks{paths["a.c"]},
			Want: []cty.PathValueMarks{paths["a.b"], paths["a.c"], paths["[0]"]},
		},
		{
			name: "one overlapping mark with different values",
			LHS: []cty.PathValueMarks{
				{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
			},
			RHS: []cty.PathValueMarks{
				{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks("OTHERMARK"),
				},
			},
			Want: []cty.PathValueMarks{
				{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks(marks.Sensitive, "OTHERMARK"),
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := combinePathValueMarks(test.LHS, test.RHS)
			if len(got) != len(test.Want) {
				t.Fatalf("incorrect result length\ngot:  %#v\nwant: %#v", got, test.Want)
			}

			for i, want := range test.Want {
				if !got[i].Equal(want) {
					t.Errorf("incorrect result\nindex: %d\ngot:  %#v\nwant: %#v", i, got[i], want)
				}
			}
		})
	}
}

func TestSensitiveMarksEqual(t *testing.T) {
	testCases := map[string]struct {
		a, b  []cty.PathValueMarks
		equal bool
	}{
		"singleMarkToFilterOut": {
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive, "customMark")},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			true,
		},
		"simpleDiff": {
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "A"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			false,
		},
		"multipleOverlapingMarksToFilterOut": {
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "b"}}, Marks: cty.NewValueMarks(marks.Sensitive, "customMark")},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "c"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "b"}}, Marks: cty.NewValueMarks(marks.Sensitive, "customMark")},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "c"}}, Marks: cty.NewValueMarks(marks.Sensitive, "customMark")},
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			true,
		},
		"multipleNonOverlapingMarksToFilterOut": {
			[]cty.PathValueMarks{
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "c"}},
					Marks: cty.NewValueMarks(marks.Sensitive, "customMark"),
				},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "c"}},
					Marks: cty.NewValueMarks(marks.Sensitive),
				},
				cty.PathValueMarks{
					Path:  cty.Path{cty.GetAttrStep{Name: "a"}, cty.GetAttrStep{Name: "b"}},
					Marks: cty.NewValueMarks(marks.Sensitive, "customMark"),
				},
			},
			true,
		},
		"anotherSimpleDiff": {
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "b"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			false,
		},
		"bothEmpty": {
			nil,
			nil,
			true,
		},
		"firstEmpty": {
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			nil,
			false,
		},
		"secondEmpty": {
			nil,
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			false,
		},
		"bothEmptyAfterFiltering": {
			nil,
			[]cty.PathValueMarks{
				cty.PathValueMarks{Path: cty.Path{cty.GetAttrStep{Name: "a"}}, Marks: cty.NewValueMarks("customMark")},
			},
			true,
		},
	}

	for name, test := range testCases {
		test := test
		t.Run(name, func(t *testing.T) {
			if sensitiveMarksEqual(test.a, test.b) != test.equal {
				t.Fatalf("marksEqual(\n%#v,\n%#v,\n) != %t\n", test.a, test.b, test.equal)
			}
		})
	}
}

// TestSensitiveMarksEqualIgnoresMarksUnderAMarkedAncestor is the fork's
// minimal-cover rule (see sensitiveMarksEqual): a live-marker plan's prior
// is marked from the schema alone, its planned side from the schema and
// the configuration, and the configuration can mark values inside an
// attribute the schema already marks whole.
func TestSensitiveMarksEqualIgnoresMarksUnderAMarkedAncestor(t *testing.T) {
	sens := cty.NewValueMarks(marks.Sensitive)
	data := cty.GetAttrPath("data")
	key := func(k string) cty.Path { return cty.GetAttrPath("data").Index(cty.StringVal(k)) }

	whole := []cty.PathValueMarks{{Path: data, Marks: sens}}
	wholeAndKeys := []cty.PathValueMarks{
		{Path: data, Marks: sens},
		{Path: key("DATABASE_PASSWORD"), Marks: sens},
		{Path: key("CONNECTION_STRING"), Marks: sens},
	}
	keysOnly := []cty.PathValueMarks{
		{Path: key("DATABASE_PASSWORD"), Marks: sens},
		{Path: key("CONNECTION_STRING"), Marks: sens},
	}
	other := []cty.PathValueMarks{{Path: cty.GetAttrPath("binary_data"), Marks: sens}}

	if !sensitiveMarksEqual(whole, wholeAndKeys) {
		t.Error("marks under an already-marked ancestor counted as a difference; a kubernetes_secret_v1 whose data keys read sensitive variables would plan an update forever")
	}
	if !sensitiveMarksEqual(wholeAndKeys, whole) {
		t.Error("the comparison is not symmetric")
	}
	if sensitiveMarksEqual(whole, keysOnly) {
		t.Error("a whole-attribute mark and key-only marks compared equal; the cover moved and that is a real change")
	}
	if sensitiveMarksEqual(whole, other) {
		t.Error("marks on different attributes compared equal")
	}
	if sensitiveMarksEqual(wholeAndKeys, append(append([]cty.PathValueMarks(nil), wholeAndKeys...), other...)) {
		t.Error("an extra marked attribute compared equal")
	}
}
