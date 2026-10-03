// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseLivePlanSet(t *testing.T) {
	got, closer, diags := ParseLivePlanSet([]string{"-json", "-parallel-estates=7", "-out-dir=plans", "estates/e01", "estates/e02"})
	defer closer()
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags.Err())
	}
	if !reflect.DeepEqual(got.Roots, []string{"estates/e01", "estates/e02"}) || got.ParallelEstates != 7 || got.OutDir != "plans" || got.View.ViewType != ViewJSON {
		t.Errorf("parsed %+v (view %v)", got, got.View.ViewType)
	}

	got, closer2, diags := ParseLivePlanSet([]string{"a"})
	defer closer2()
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags.Err())
	}
	if got.ParallelEstates != 4 || got.OutDir != DefaultPlanSetOutDir || got.View.ViewType != ViewHuman {
		t.Errorf("defaults: %+v", got)
	}
}

func TestParseLivePlanSet_invalid(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no roots":      {nil, "No roots given"},
		"zero parallel": {[]string{"-parallel-estates=0", "a"}, "Invalid -parallel-estates"},
		"empty out dir": {[]string{"-out-dir=", "a"}, "Invalid -out-dir"},
	} {
		t.Run(name, func(t *testing.T) {
			_, closer, diags := ParseLivePlanSet(tc.args)
			defer closer()
			if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), tc.want) {
				t.Errorf("diagnostics %v, want one containing %q", diags.Err(), tc.want)
			}
		})
	}
}
