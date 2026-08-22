package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestModuleForeachFilterOverPoisonedValueResolves is corpus-alb-complete's
// Family A shape, reduced to a fixture: a module call argument's object
// literal has one poisoned leaf (a sibling resource's identity attribute)
// beside several plain-literal siblings, and the child module's own for_each
// over that argument is filtered by a lookup() on the whole element - so the
// filter itself needed the poisoned element's whole value just as much as
// any each.value.<attr> selection inside the resource block does. See
// testdata/module-foreach-forexpr-filter-sibling-value/main.tf for the full
// account and the three routes this exercises.
func TestModuleForeachFilterOverPoisonedValueResolves(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-foreach-forexpr-filter-sibling-value"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("refused: %s", diags.Err())
	}

	// The filter itself (lookup(v, "create", true)) and the poisoned leaf
	// read directly (each.value.arn).
	attachment := resolutionAt(t, result, `module.attach.aws_iam_role_policy_attachment.this["ImageBuilder"]`)
	if attachment.Class != ClassParentDerived {
		t.Fatalf(`aws_iam_role_policy_attachment.this["ImageBuilder"] resolved %s, want PARENT_DERIVED`, attachment.Class)
	}
	const wantAttachmentFormula = `gh-image-builder/${aws_iam_policy.imagebuilder.arn}`
	if got := attachment.Formula.String(); got != wantAttachmentFormula {
		t.Errorf("aws_iam_role_policy_attachment.this formula is %q, want %q", got, wantAttachmentFormula)
	}

	// A ternary whose CONDITION reads a plain-literal SIBLING attribute of
	// the same poisoned element - corpus-alb-complete's own
	// aws_lb_target_group_attachment.this port argument
	// (try(each.value.target_type, null) == "lambda" ? null : ...), reduced
	// to a string identity component so it can be asserted by value.
	special := resolutionAt(t, result, `module.attach.aws_iam_user.tag["ImageBuilder"]`)
	if special.Class != ClassConcrete || special.ImportID != "special-ImageBuilder" {
		t.Errorf(`aws_iam_user.tag["ImageBuilder"] resolved %s %q, want CONCRETE "special-ImageBuilder"`, special.Class, special.ImportID)
	}
	plain := resolutionAt(t, result, `module.attach.aws_iam_user.tag["Other"]`)
	if plain.Class != ClassConcrete || plain.ImportID != "plain-Other" {
		t.Errorf(`aws_iam_user.tag["Other"] resolved %s %q, want CONCRETE "plain-Other"`, plain.Class, plain.ImportID)
	}

	// An indexed reference into a DIFFERENT resource, where the index is a
	// plain-literal sibling (target_key) of the same poisoned element -
	// corpus-alb-complete's own aws_lb_target_group_attachment.additional's
	// target_group_arn argument
	// (aws_lb_target_group.this[each.value.target_group_key].arn).
	byIndex := resolutionAt(t, result, `module.attach.aws_iam_role_policy_attachment.byindex["ImageBuilder"]`)
	if byIndex.Class != ClassParentDerived {
		t.Fatalf(`aws_iam_role_policy_attachment.byindex["ImageBuilder"] resolved %s, want PARENT_DERIVED`, byIndex.Class)
	}
	const wantByIndexFormula = `role-one/${aws_iam_policy.imagebuilder.arn}`
	if got := byIndex.Formula.String(); got != wantByIndexFormula {
		t.Errorf("aws_iam_role_policy_attachment.byindex formula is %q, want %q", got, wantByIndexFormula)
	}
}
