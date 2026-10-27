// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"
	"testing"
)

// GitHub issue #2287: -adopt takes the place of -state and needs
// -index-store.
func TestParseLiveImportAdopt(t *testing.T) {
	li, diags := ParseLiveImport([]string{"-adopt=adopt.json", "-index-store=/store", "-estate=acme"})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if li.AdoptPath != "adopt.json" || li.IndexStore != "/store" || li.StatePath != "" {
		t.Errorf("parsed %+v", li)
	}

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"both":                {[]string{"-adopt=a.json", "-index-store=/s", "-state=x.tfstate", "-estate=acme"}, "Both -state and -adopt"},
		"neither":             {[]string{"-estate=acme"}, "No state file named"},
		"adopt without store": {[]string{"-adopt=a.json", "-estate=acme"}, "No index store named"},
		"store without adopt": {[]string{"-state=x.tfstate", "-index-store=/s", "-estate=acme"}, "-index-store without -adopt"},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := ParseLiveImport(tc.args)
			if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), tc.want) {
				t.Errorf("diags = %v, want %q", diags.Err(), tc.want)
			}
		})
	}
}
