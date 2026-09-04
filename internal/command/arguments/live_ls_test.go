// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"
	"testing"
)

func TestParseLiveLs_valid(t *testing.T) {
	testCases := map[string]struct {
		args           []string
		wantEstate     string
		wantRegion     string
		wantConsistent bool
		wantConfigDir  string
		wantViewType   ViewType
	}{
		"estate only": {
			[]string{"-estate=prod"},
			"prod", "", false, "", ViewHuman,
		},
		"estate space form": {
			[]string{"-estate", "prod"},
			"prod", "", false, "", ViewHuman,
		},
		"region": {
			[]string{"-estate=prod", "-region=us-east-1"},
			"prod", "us-east-1", false, "", ViewHuman,
		},
		"consistent": {
			[]string{"-estate=prod", "-consistent"},
			"prod", "", true, "", ViewHuman,
		},
		"json": {
			[]string{"-estate=prod", "-json"},
			"prod", "", false, "", ViewJSON,
		},
		"config dir": {
			[]string{"-estate=prod", "testdata"},
			"prod", "", false, "testdata", ViewHuman,
		},
		"every flag together": {
			[]string{"-estate=prod", "-region=eu-west-1", "-consistent", "-json", "testdata"},
			"prod", "eu-west-1", true, "testdata", ViewJSON,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got, closer, diags := ParseLiveLs(tc.args)
			defer closer()
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %v", diags.Err())
			}
			if got.Estate != tc.wantEstate {
				t.Errorf("Estate = %q, want %q", got.Estate, tc.wantEstate)
			}
			if got.Region != tc.wantRegion {
				t.Errorf("Region = %q, want %q", got.Region, tc.wantRegion)
			}
			if got.Consistent != tc.wantConsistent {
				t.Errorf("Consistent = %v, want %v", got.Consistent, tc.wantConsistent)
			}
			if got.ConfigDir != tc.wantConfigDir {
				t.Errorf("ConfigDir = %q, want %q", got.ConfigDir, tc.wantConfigDir)
			}
			if got.ViewOptions.ViewType != tc.wantViewType {
				t.Errorf("ViewType = %v, want %v", got.ViewOptions.ViewType, tc.wantViewType)
			}
		})
	}
}

func TestParseLiveLs_invalid(t *testing.T) {
	testCases := map[string]struct {
		args        []string
		wantSummary string
	}{
		"no estate":       {nil, "No estate named"},
		"unknown flag":    {[]string{"-estate=prod", "-nope"}, "Invalid option"},
		"two directories": {[]string{"-estate=prod", "a", "b"}, "Too many arguments"},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			_, closer, diags := ParseLiveLs(tc.args)
			defer closer()
			if !diags.HasErrors() {
				t.Fatal("no diagnostics")
			}
			if got := diags.Err().Error(); !strings.Contains(got, tc.wantSummary) {
				t.Errorf("wrong diagnostic %q, want it to name %q", got, tc.wantSummary)
			}
		})
	}
}
