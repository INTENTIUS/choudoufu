// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #354: a module output consumed as a module CALL argument could
// not carry a deferred parent read. The issue's own framing is that the
// value-shaped route (`tolerantVariables` -> `rebuildConstructor` ->
// `moduleOutputValue`) has to learn to carry a [ParentRef], and that is not
// what this fixes, because it cannot be done: a deferred read is not a
// cty.Value and inventing a sentinel every wholly-known gate would have to
// learn about is exactly the change that makes a wrong marker possible.
//
// What is done instead is a LAYERING. The value route keeps its answer,
// unchanged and first, and the element's own expression is kept beside it for
// the attributes of that value that came back unknown. The two are consulted
// in that order by construction rather than by care:
// [instScope.eachValueDeferred] is invisible to [resolver.isSymbolic], so
// every argument is evaluated exactly as it was, and the expression is
// reached only after [resolver.stringValueIn] has already refused an unknown.
//
// # The three walls, measured
//
// `corpus-autoscaling-complete` is the estate, and reducing it found three
// separate obstacles between the shape and a resolution, not one:
//
//  1. The for_each source is a GUARD CONDITIONAL, `var.create &&
//     var.attachments != null ? var.attachments : {}`.
//     [resolver.staticCollElems] has no arm for a conditional, so the whole
//     source falls through to [resolver.tolerantRetry], which answers with a
//     value and carries no element expressions at all.
//     [resolver.elementExprBindings] collects them beside that value, and
//     does its own guard unwrapping rather than teaching the shared chase
//     about conditionals - see its doc for the regression that measured.
//  2. The DECLARED TYPE drops the element expression at the module-variable
//     hop (#251/#301), because a conversion is not the identity function on
//     an element as a whole. [preservedExpr]'s object case carries it across
//     together with the type it must be read under, and
//     [declaredAttrString] is the rule that reads it.
//  3. The leaf is `module.alb.target_groups["ex_asg"].arn`, over an output
//     that publishes the WHOLE resource. [resolver.resolveModuleOutput]
//     enters module.alb and hands `aws_lb_target_group.this` to
//     [resolver.selectStatic] with `["ex_asg"]` and `.arn` still owed, and
//     that function had no arm for a managed resource reference with steps
//     left, so it declined at the last hop.
//
// Measured against the estate: `corpus-autoscaling-complete` goes from 5
// refusals across 230 sites with 48 instances resolved to 4 across 229 with
// 49, and the `Non-static identity argument` on
// aws_autoscaling_traffic_source_attachment is gone. The one identity refusal
// left is the zero-element `prefix_list_ids` on aws_security_group_rule,
// which is [Component.SoleElement]'s own deliberate refusal and a different
// question.
//
// # What it does NOT reach, also measured
//
// `corpus-rds-complete-postgres` and `corpus-ecs-fargate` are named on the
// same issue and neither moves, for a reason that is not a route at all. Both
// apply a FUNCTION to the deferred value - `compact(split(",", <leaf>))` and
// `element(split("/", <leaf>), 1)` - and an [identity.Formula] holds literals
// and ParentRefs with no way to express "split this parent attribute and take
// element 1". Reduced and measured directly: rewriting the rds module's
// `cidr_blocks` to a literal index with no lookup() and no compact/split
// makes both of that estate's diagnostics disappear, and putting only the
// compact(split(...)) back brings them straight back. That is a render-time
// transform in Formula, tracked separately.

func resolveWholeResourceOutput(t *testing.T) *Result {
	t.Helper()
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-whole-resource"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if result == nil {
		t.Fatal("resolution produced no result at all")
	}
	// The two negative controls are the only things that may refuse here.
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		detail := d.Description().Detail
		if strings.Contains(detail, ".numeric[") || strings.Contains(detail, ".other[") {
			continue
		}
		t.Errorf("unexpected refusal: %s | %s", d.Description().Summary, detail)
	}
	return result
}

// TestModuleOutputWholeResourceDeferredRead is the issue itself, asserted by
// VALUE rather than by class.
//
// Three other strings would satisfy a class check and be wrong in a cloud
// tag: the target group's own name ("ex_asg"), the module output's name, and
// the literal the type default supplies for the OTHER attribute. So the
// assertion is on the rendered formula, and it has to name the target group
// instance the caller indexed - `["ex_asg"]`, not the resource as a whole and
// not some other key.
func TestModuleOutputWholeResourceDeferredRead(t *testing.T) {
	result := resolveWholeResourceOutput(t)

	res := resolutionAt(t, result, `module.asg.aws_autoscaling_traffic_source_attachment.this["ex-alb"]`)
	if res.Class != ClassParentDerived {
		t.Fatalf("resolved %s, want PARENT_DERIVED", res.Class)
	}
	const want = `asg-fixed,elbv2,${module.alb.aws_lb_target_group.this["ex_asg"].arn}`
	if got := res.Formula.String(); got != want {
		t.Errorf("renders %q, want %q", got, want)
	}
}

// TestModuleOutputWholeResourceValueStillWins is the ordering control, and it
// is the one that caught a real regression while this was being built.
//
// `name = try(coalesce(each.value.name, each.key), "")` over an element the
// caller wrote no `name` in resolves TODAY, because the declared type makes
// name null inside the module and coalesce() therefore takes the key. An
// earlier version of this change taught [resolver.staticCollElems] about the
// guard conditional directly; that made the structural chase succeed, which
// pre-empted the tolerant retry, and the structural chase's own value for an
// unreadable element is an unknown of the WHOLE element rather than an object
// with one unknown attribute in it. `corpus-autoscaling-complete`'s
// aws_autoscaling_policy.this["request-count-per-target"] stopped resolving.
//
// So the collector runs beside the retry instead of in front of it, and this
// pins that: the value answers, the expression does not get a say, and the
// identity is the key the language would have chosen.
func TestModuleOutputWholeResourceValueStillWins(t *testing.T) {
	result := resolveWholeResourceOutput(t)

	res := resolutionAt(t, result, `module.asg.aws_autoscaling_policy.this["p1"]`)
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s, want CONCRETE", res.Class)
	}
	if want := "asg-fixed/p1"; res.ImportID != want {
		t.Errorf("renders %q, want %q", res.ImportID, want)
	}
}

// TestModuleOutputWholeResourceRefusesANonStringDeclaredType is
// [declaredAttrString]'s whole reason to exist, and it is #251 restated for
// this route: OpenTofu converts a module call's argument to the variable's
// declared type before anything inside the module reads it, so rendering the
// caller's own expression is only sound where that conversion is the identity
// function. It is, for a string. It is not for a number, and an ARN rendered
// into a number-typed position is a value OpenTofu would reject outright.
//
// Removing the declared-type gate, and only that, resolves this instance.
func TestModuleOutputWholeResourceRefusesANonStringDeclaredType(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-whole-resource"), nil)
	result, _ := Resolve(context.Background(), cfg)

	for _, r := range result.All() {
		if strings.HasPrefix(r.Addr.String(), `module.asg.aws_autoscaling_traffic_source_attachment.numeric[`) {
			t.Errorf("%s resolved to %q / %q, but its declared type converts a string to a number",
				r.Addr, r.ImportID, r.Formula.String())
		}
	}
}

// TestModuleOutputWholeResourceRefusesANonIdentityAttribute keeps the
// deferred selection a route rather than a licence. `port` is not an identity
// attribute of aws_lb_target_group, and reaching it through a module output,
// a declared type and a for_each element does not make it one:
// [resolver.parentPart] refuses it here for exactly the reason it refuses a
// direct `aws_lb_target_group.this["ex_asg"].port`.
func TestModuleOutputWholeResourceRefusesANonIdentityAttribute(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-whole-resource"), nil)
	result, _ := Resolve(context.Background(), cfg)

	for _, r := range result.All() {
		if strings.HasPrefix(r.Addr.String(), `module.asg.aws_autoscaling_traffic_source_attachment.other[`) {
			t.Errorf("%s resolved to %q / %q over a non-identity attribute of a server-assigned parent",
				r.Addr, r.ImportID, r.Formula.String())
		}
	}
}
