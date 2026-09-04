// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestRefuseCarriesTheCodeWithoutChangingTheDiagnostic pins [refuse]'s own
// promise: severity, summary and detail come back exactly as
// [tfdiags.Sourceless] would have produced them, and the code is retrievable
// only through [tfdiags.ExtraInfo], never by a caller that reads Description
// alone. GitHub issue #791's -json flag is the first reader of the code;
// every existing test asserting on live-mv's human text (internal/command/
// live_mv_test.go) is the proof that adding it changed nothing those tests
// already pinned.
func TestRefuseCarriesTheCodeWithoutChangingTheDiagnostic(t *testing.T) {
	plain := tfdiags.Sourceless(tfdiags.Error, "Some refusal", "Some detail.")
	coded := refuse(RefusalTwoAtOldAddress, tfdiags.Error, "Some refusal", "Some detail.")

	if coded.Severity() != plain.Severity() {
		t.Errorf("Severity() = %v, want %v", coded.Severity(), plain.Severity())
	}
	if coded.Description() != plain.Description() {
		t.Errorf("Description() = %+v, want %+v", coded.Description(), plain.Description())
	}

	if got := tfdiags.ExtraInfo[RefusalCode](coded); got != RefusalTwoAtOldAddress {
		t.Errorf("tfdiags.ExtraInfo[RefusalCode](coded) = %q, want %q", got, RefusalTwoAtOldAddress)
	}
	if got := tfdiags.ExtraInfo[RefusalCode](plain); got != "" {
		t.Errorf("an ordinary tfdiags.Sourceless diagnostic answered a RefusalCode of %q, want none", got)
	}
}

// TestRefusalFromFindsTheFirstCodedError pins [RefusalFrom]'s two edges: it
// skips warnings (a coded warning would be a bug in a future caller, but
// this proves the scan does not mistake one for the refusal itself) and it
// returns the empty code, not a panic or a made-up value, when nothing in
// the diagnostics carries one - the honest answer for a refusal outside the
// five shapes [RefusalCode] names, or for no refusal at all.
func TestRefusalFromFindsTheFirstCodedError(t *testing.T) {
	var diags tfdiags.Diagnostics
	diags = diags.Append(refuse(RefusalNothingAtOldAddress, tfdiags.Warning, "A coded warning", "Never produced today, but must not be mistaken for the refusal."))
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "An uncoded error", "One of the refusals outside the five named shapes."))
	diags = diags.Append(refuse(RefusalDestinationNotDeclared, tfdiags.Error, "The coded refusal", "This is the one RefusalFrom must find."))

	if got := RefusalFrom(diags); got != RefusalDestinationNotDeclared {
		t.Errorf("RefusalFrom = %q, want %q", got, RefusalDestinationNotDeclared)
	}

	var empty tfdiags.Diagnostics
	empty = empty.Append(tfdiags.Sourceless(tfdiags.Error, "No code at all", "Nothing here carries a RefusalCode."))
	if got := RefusalFrom(empty); got != "" {
		t.Errorf("RefusalFrom over diagnostics with no code = %q, want empty", got)
	}
}
