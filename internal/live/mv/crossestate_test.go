// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// A cross-estate move keeps its address in the ordinary case - a resource
// block moved between two configurations unchanged - so the identical-
// addresses refusal a rename raises has to stand down exactly when
// FromEstate is set, and only then.
func TestCheckAddressesAdmitsASameAddressCrossEstateMove(t *testing.T) {
	addr := resInst(nil, "aws_iam_role", "team", addrs.NoKey)

	if diags := checkAddresses(Request{Old: addr, New: addr, FromEstate: "monolith"}); diags.HasErrors() {
		t.Fatalf("a same-address move across estates was refused: %s", diags.Err())
	}
	diags := checkAddresses(Request{Old: addr, New: addr})
	if !diags.HasErrors() {
		t.Fatal("a same-address rename within one estate was admitted")
	}
	if got := diags.Err().Error(); !strings.Contains(got, "-from-estate") {
		t.Errorf("the refusal does not point at -from-estate: %s", got)
	}
}

// sourceEstate is where the resource is looked for; the destination is what
// the write carries. Without FromEstate the two are the same estate.
func TestSourceEstateFallsBackToTheDestination(t *testing.T) {
	m := &mover{req: Request{Estate: "data"}}
	if got := m.sourceEstate(); got != "data" {
		t.Errorf("sourceEstate() = %q, want data", got)
	}
	m.req.FromEstate = "monolith"
	if got := m.sourceEstate(); got != "monolith" {
		t.Errorf("sourceEstate() = %q, want monolith", got)
	}
}
