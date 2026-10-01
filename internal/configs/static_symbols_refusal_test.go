// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"testing"

	"github.com/intentius/choudoufu/internal/configs/symlib"
)

// TestStaticEvaluator_symbolsFunctionRefusedByName pins #1778's ruling 8:
// OpenTofu v1.13.0 admits a symbol library function (symbols::lib::fn) in
// static context with a bare `continue`, and choudoufu's static subset
// refuses it by name instead, until upstream stabilises symbol libraries.
//
// The refusal has to be the named one, not whatever evaluation happens to
// produce next. With upstream's `continue` the same expression fails later
// and differently (the function is not in an empty symbol table), which
// would let a refusal ID drift with upstream's evaluator rather than say what
// this fork decided.
func TestStaticEvaluator_symbolsFunctionRefusedByName(t *testing.T) {
	parser := testParser(map[string]string{"sym.tf": `
locals {
	sym = symbols::lib::fn("x")
}
`})
	file, fileDiags := parser.LoadConfigFile("sym.tf")
	if fileDiags.HasErrors() {
		t.Fatal(fileDiags)
	}

	call := RootModuleCallForTesting()
	mod, modDiags := NewModule([]*File{file}, nil, "dir", SelectiveLoadAll)
	if modDiags.HasErrors() {
		t.Fatal(modDiags)
	}
	_ = mod.Finalize(symlib.EmptyTable, call)
	eval := NewStaticEvaluator(mod, nil, call)

	local := mod.Locals["sym"]
	_, diags := eval.Evaluate(t.Context(), local.Expr, StaticIdentifier{Subject: "local.sym", DeclRange: local.DeclRange})
	assertExactDiagnostics(t, diags, []string{
		`sym.tf:3,8-24: Symbol library function in static context; Unable to use symbols::lib::fn in static context, which is required by local.sym`,
	})
}
