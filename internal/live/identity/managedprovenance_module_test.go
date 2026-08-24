// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Unit corpus-alb-complete/test_plan, following #388/7e3eb9e2e3's no-Importer
// fix (04620ffdb2): once aws_acm_certificate_validation.this[0] genuinely
// participates in the graph, module.alb.aws_lb_listener_certificate.this and
// both aws_route53_record.validation instances - whose own certificate_arn
// arguments have NO Cognito reference anywhere in the configuration -
// misattribute to "takes certificate_arn from aws_cognito_user_pool.this"
// and refuse with "Unmarked apply of a marker-only resource".
//
// # The shape, copied close to verbatim from its real sources
//
// terraform-aws-modules/terraform-aws-alb v9.9.0's own main.tf builds
// `local.additional_certs` by flattening every listener's
// `additional_certificate_arns` into a map keyed "listener_key/idx"
// (lines 456-473, reproduced in testdata/managed-read-module-blind-crosstalk
// nearly verbatim), then `aws_lb_listener_certificate.this` for_eachs over
// it. corpus-alb-complete's own example combines an HTTPS listener (whose
// certificate is a CHILD MODULE's output,
// module.wildcard_cert.acm_certificate_arn) and an unrelated
// Cognito-authenticated listener (aws_cognito_user_pool.this.arn, referenced
// DIRECTLY, no module boundary) into the SAME `listeners` map. The module
// output itself is terraform-aws-modules/terraform-aws-acm v4.5.0's own
// outputs.tf: `try(aws_acm_certificate_validation.this[0].certificate_arn,
// aws_acm_certificate.this[0].arn, "")`.
//
// # Where the misattribution actually happens
//
// `additional_certs`'s nested for-comprehensions/merge()/lookup() are too
// deep for [resolver.staticForEachKeys]'s structural chase (lookup() is not
// one of the shapes [resolver.staticCollElems] recognises as a source), so
// resolving `aws_lb_listener_certificate.this`'s for_each falls through to
// [resolver.forEachExpansion]'s LAST resort: the whole for_each expression is
// evaluated once through [resolver.tolerantRetry]/[resolver.tolerantEvaluator],
// and [expansion.managedFrom] is computed ONCE for the whole expansion by
// chasing that SAME expression with [resolver.managedFromExpr]
// (resolve.go:4265). Every instance's own `each.value.<attr>` that cannot
// resolve on its own then falls back to [resolver.managedFromScope], which
// just returns that one, single, already-decided managedFrom address -
// whatever it is, for every key, regardless of which listener that key
// actually belongs to.
//
// The chase itself (managedprovenance.go's [resolver.managedFromExprAt]) only
// ever hops through "local" and "var" roots (lines 165-172); a "module" root
// is never followed. [resolver.managedUnknownAt]'s own switch
// (managedprovenance.go:437-445) can only classify a traversal whose
// addrs.Ref.Subject is addrs.Resource or addrs.ResourceInstance, so a
// module-output traversal (module.wildcard_cert.acm_certificate_arn) can
// never be a candidate: it hits the switch's default case - "", false -
// whatever the underlying resource's own knownness is.
//
// aws_cognito_user_pool.this.arn sits in the very same `local.listeners`
// object, but with NO module boundary between it and the traversal, so when
// its own value is unknown (as it is here, matching a run where
// CHOUDOUFU_NODE_RESOLVE=1 has not yet resolved that node),
// [resolver.managedUnknownAt] admits it without hesitation. Because the ACM
// leg was never a candidate at all, `found` ends up with exactly one entry -
// Cognito - and [resolver.managedFromExprAt]'s own ambiguity guard
// (`if len(found) != 1`) never fires, because as far as it can see there was
// never more than one candidate. The wrong single answer looks exactly like
// a confident right one.
//
// This is HANDOFF's row 2 (the plans differ: a defect), found by
// instrumenting managedUnknownAt's own switch rather than by guessing at
// resolve.go's stack discipline, which the prior worker had already checked
// and found careful everywhere they looked.
func moduleBlindCrosstalkManagedResults(validationUnknown, cognitoUnknown bool) map[string]cty.Value {
	validationArn := cty.StringVal("arn:aws:acm:us-east-1:1:certificate/real-wildcard-cert")
	if validationUnknown {
		validationArn = cty.UnknownVal(cty.String)
	}
	poolArn := cty.StringVal("arn:aws:cognito-idp:us-east-1:1:userpool/real-pool")
	if cognitoUnknown {
		poolArn = cty.UnknownVal(cty.String)
	}
	return map[string]cty.Value{
		// The plain certificate is always known - only the VALIDATION
		// resource (the no-Importer-fix carrier) and Cognito vary, matching
		// [Context.ManagedResults]'s own per-node population under
		// CHOUDOUFU_NODE_RESOLVE=1: a resource this run has not yet
		// resolved is simply absent or unknown, never wrong.
		"module.wildcard_cert.aws_acm_certificate.this[0]": cty.ObjectVal(map[string]cty.Value{
			"arn": cty.StringVal("arn:aws:acm:us-east-1:1:certificate/root-cert-fallback"),
		}),
		"module.wildcard_cert.aws_acm_certificate_validation.this[0]": cty.ObjectVal(map[string]cty.Value{
			"certificate_arn": validationArn,
		}),
		"aws_cognito_user_pool.this": cty.ObjectVal(map[string]cty.Value{
			"arn": poolArn,
		}),
	}
}

// moduleBlindCrosstalkSchemas supplies aws_acm_certificate_validation's real
// schema (hashicorp/aws 6.59.0, read against a real cold-deployed
// corpus-alb-complete estate - see
// internal/live/projection/noimporter_test.go's certificateValidationSchema,
// copied here rather than imported across a package boundary that does not
// otherwise exist between these two packages' test files). It is what makes
// the type admitted on the nameability axis (identity.Derivable resolves
// certificate_arn) exactly as the real estate has it, rather than through
// DefaultTable, which does not carry this type at all.
func moduleBlindCrosstalkSchemas() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_acm_certificate_validation": {
			args: map[string]string{"certificate_arn": "req", "validation_record_fqdns": "opt"},
		},
	})
}

// TestManagedFromModuleOutputBlindCrosstalk is the repro, asserted BY
// VALUE, of the bug [namesAModuleOutput] fixes: with the validation
// resource's own certificate_arn still unknown (this run has not resolved
// module.wildcard_cert's own graph node yet) and Cognito's pool ARN also
// unknown (same reason, a different resource),
// module.alb.aws_lb_listener_certificate.this["https/0"] must never be
// attributed to aws_cognito_user_pool.this - its own certificate_arn
// argument reads module.wildcard_cert's output, and names no Cognito
// reference anywhere in the configuration.
//
// Before the fix, it does exactly that: the instance resolves
// NEEDS_DISCOVERY with CauseArgs[0] == "aws_cognito_user_pool.this" and a
// Reason claiming it "takes certificate_arn from aws_cognito_user_pool.this"
// - HANDOFF's row 2 (the plans differ: a defect), not merely a missed
// resolution. After the fix, the instance declines honestly instead (its
// own module-routed value genuinely cannot be read in this run), which is
// what the second half of this test proves: the diagnostic naming this
// address is the ordinary "Non-static identity argument" refusal, with no
// Cognito anywhere in it.
//
// The mutation check is built into the assertion shape itself, not a
// separate run: reverting [namesAModuleOutput]'s call in
// managedFromExprAt reproduces the FIRST failure mode this test checks for
// (a resolved instance attributed to Cognito) precisely, and every
// assertion below is written to catch that specific shape, not merely "no
// error".
func TestManagedFromModuleOutputBlindCrosstalk(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "managed-read-module-blind-crosstalk"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: moduleBlindCrosstalkManagedResults(true, true),
		Schemas:        moduleBlindCrosstalkSchemas(),
	})
	if result == nil {
		t.Fatalf("resolution produced no result at all: %s", renderDiags(diags))
	}

	addr := mustAddr(t, `module.alb.aws_lb_listener_certificate.this["https/0"]`)
	if res, ok := result.Get(addr); ok {
		if res.Class == ClassNeedsDiscovery && len(res.CauseArgs) > 0 && res.CauseArgs[0] == "aws_cognito_user_pool.this" {
			t.Fatalf(
				`%s was attributed to aws_cognito_user_pool.this (reason: %q) - its own certificate_arn argument reads module.wildcard_cert's output, not Cognito, and Cognito is unrelated`,
				addr, res.Reason)
		}
		// Any OTHER resolution (declining to resolve is also acceptable,
		// asserted below via diags) is fine as long as it is not the
		// Cognito misattribution above.
		return
	}

	// Declined to resolve: the honest outcome once the module-output blind
	// spot no longer lets a direct-but-unrelated reference win by default.
	// The diagnostic naming this instance must be the ordinary evaluation
	// refusal, never a sibling-apply sentence that mentions Cognito.
	var sawOwnRefusal, sawCognito bool
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		if !strings.Contains(d.Description().Detail, addr.String()) {
			continue
		}
		if d.Description().Summary == SummaryNonStaticIdentityArgument {
			sawOwnRefusal = true
		}
		if strings.Contains(d.Description().Detail, "aws_cognito_user_pool.this") {
			sawCognito = true
		}
	}
	if !sawOwnRefusal {
		t.Errorf("%s: no %q diagnostic found; diags:\n%s", addr, SummaryNonStaticIdentityArgument, renderDiags(diags))
	}
	if sawCognito {
		t.Errorf("%s: a diagnostic mentioned aws_cognito_user_pool.this - the misattribution moved into the diagnostic text instead of the resolution; diags:\n%s", addr, renderDiags(diags))
	}
}

// TestManagedFromModuleOutputBlindCrosstalkKnownValidationResolvesConcrete
// is the negative control that keeps the fix a rule, not a licence: once the
// validation resource's own value is known (this run HAS resolved
// module.wildcard_cert's node), aws_lb_listener_certificate.this["https/0"]
// must resolve CONCRETE from that known ARN - never dragged into a Cognito
// attribution just because the chase, before any fix, walked the whole
// combined `local.listeners` looking for ANY covered-unknown reference and
// Cognito's own pool ARN happens to still be unknown at that point.
func TestManagedFromModuleOutputBlindCrosstalkKnownValidationResolvesConcrete(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "managed-read-module-blind-crosstalk"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: moduleBlindCrosstalkManagedResults(false, true),
		Schemas:        moduleBlindCrosstalkSchemas(),
	})
	if result == nil {
		t.Fatalf("resolution produced no result at all: %s", renderDiags(diags))
	}

	res, ok := result.Get(mustAddr(t, `module.alb.aws_lb_listener_certificate.this["https/0"]`))
	if !ok {
		t.Fatalf(`aws_lb_listener_certificate.this["https/0"] did not resolve at all: %s`, renderDiags(diags))
	}
	if res.Class != ClassConcrete {
		t.Fatalf(`aws_lb_listener_certificate.this["https/0"] resolved %s (cause %s, args %v), want CONCRETE - its own certificate_arn is a known value with no Cognito dependency`, res.Class, res.Cause, res.CauseArgs)
	}
	want := "arn:aws:elasticloadbalancing:us-east-1:1:listener/app/x/1/2_arn:aws:acm:us-east-1:1:certificate/real-wildcard-cert"
	if res.ImportID != want {
		t.Errorf("resolved ImportID %q, want %q", res.ImportID, want)
	}
}
