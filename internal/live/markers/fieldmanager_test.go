// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"strings"
	"testing"
)

// TestFieldManagerRoundTrip pins GitHub issue #1191's marker: an estate's
// field manager reads back as that estate, and a manager that is not an
// estate's - the provider's default, kubectl, a controller, the bare
// prefix - reads back as none.
func TestFieldManagerRoundTrip(t *testing.T) {
	for _, estate := range []string{"prod", "team-a.payments", "x"} {
		got, ok := EstateOfFieldManager(FieldManagerFor(estate))
		if !ok || got != estate {
			t.Errorf("EstateOfFieldManager(FieldManagerFor(%q)) = %q, %v", estate, got, ok)
		}
	}
	for _, m := range []string{"Terraform", "kubectl-client-side-apply", "choudoufu", "choudoufu:", "Choudoufu:prod", ""} {
		if e, ok := EstateOfFieldManager(m); ok {
			t.Errorf("EstateOfFieldManager(%q) = %q; it is not an estate's manager", m, e)
		}
	}
}

// TestFieldManagerLength: the API server caps a field manager name at 128
// characters, so an estate whose manager would be longer is refused with a
// reason, and one that fits is not.
func TestFieldManagerLength(t *testing.T) {
	fits := strings.Repeat("a", FieldManagerMaxLen-len(FieldManagerPrefix))
	if why := ValidFieldManagerEstate(fits); why != "" {
		t.Errorf("a %d-character manager name was refused: %s", len(FieldManagerFor(fits)), why)
	}
	if why := ValidFieldManagerEstate(fits + "a"); why == "" {
		t.Errorf("a %d-character manager name was accepted", len(FieldManagerFor(fits+"a")))
	}
	if why := ValidFieldManagerEstate(""); why == "" {
		t.Error("an empty estate was accepted")
	}
}
