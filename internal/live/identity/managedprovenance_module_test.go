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
// UPDATE (corpus-alb-complete/test_plan, the chase-through unit): the
// paragraph above describes the state before ANY fix landed. The first fix
// made a "module" root decline the whole level outright, whatever it named.
// The current one ([resolver.managedFromModuleOutput]) instead resolves it -
// entering the child module and asking the identical provenance question of
// the output's own defining expression - so it becomes a real entry in
// `found` precisely when it can be proven to name exactly one candidate.
// Nothing above stops being true as HISTORY; it is why the mechanism looks
// the way it does today.
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
// VALUE, of the original blind-spot bug and the negative case for the later
// chase-through widening ([resolver.managedFromModuleOutput]): with the
// validation resource's own certificate_arn still unknown (this run has not
// resolved module.wildcard_cert's own graph node yet) AND Cognito's pool ARN
// also unknown (same reason, a different resource),
// module.alb.aws_lb_listener_certificate.this["https/0"] must never be
// attributed to aws_cognito_user_pool.this - its own certificate_arn
// argument reads module.wildcard_cert's output, and names no Cognito
// reference anywhere in the configuration.
//
// Before ANY fix, it does exactly that: the instance resolves
// NEEDS_DISCOVERY with CauseArgs[0] == "aws_cognito_user_pool.this" and a
// Reason claiming it "takes certificate_arn from aws_cognito_user_pool.this"
// - HANDOFF's row 2 (the plans differ: a defect), not merely a missed
// resolution. The first fix (git history) declined outright the moment ANY
// traversal named a "module" root, whether or not that traversal could be
// chased - safe, but blind to the real answer even where one existed. The
// current fix ([resolver.managedFromModuleOutput]) instead PROVES the
// module-routed leg: it chases through module.wildcard_cert's own output
// expression and finds a real candidate there too
// (aws_acm_certificate_validation.this[0], still unknown in THIS test's
// setup), so `found` ends up with TWO genuine candidates - Cognito and the
// ACM resource - and the SAME len(found) != 1 ambiguity guard declines for
// an honest reason instead of a blind one. Either way the instance declines
// rather than resolves, which is what the second half of this test proves:
// the diagnostic naming this address is the ordinary "Non-static identity
// argument" refusal, with no Cognito anywhere in it.
// TestManagedFromModuleOutputChasesThroughToACMResource is the sibling test
// that proves the chase actually resolves something when only ONE candidate
// is real (Cognito already known, only the ACM leg unknown) - the case this
// test's own shape can never exercise, because both legs unknown here is
// always genuinely ambiguous.
//
// The mutation check is built into the assertion shape itself, not a
// separate run: reverting managedFromExprAt's found-accumulation loop to
// skip a "module" root instead of chasing or declining on it reproduces the
// FIRST failure mode this test checks for (a resolved instance attributed
// to Cognito) precisely, and every assertion below is written to catch that
// specific shape, not merely "no error". Weakening the len(found) != 1
// ambiguity guard itself is TestManagedFromModuleOutputChasesThroughToACMResource's
// and this file's own mutation note - see that test's doc comment.
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

// TestManagedFromModuleOutputChasesThroughToACMResource is the positive
// case [resolver.managedFromModuleOutput] exists for: when Cognito's own
// pool ARN has already resolved (known) and ONLY the module-routed ACM
// validation resource is still unknown, module.alb.aws_lb_listener_certificate.this
// ["https/0"] must attribute to that REAL resource - by value - instead of
// declining the way the pre-chase fix always did for any module-output
// reference, proven or not.
//
// This is what makes the ambiguity guard in managedFromExprAt HONEST rather
// than blind: at the level that chases local.listeners (the object
// constructor combining both listeners), [resolver.managedUnknownAt] no
// longer finds Cognito's own reference a candidate at all (its ARN is known
// now), and [resolver.managedFromModuleOutput] proves the module-routed leg
// resolves to exactly one candidate -
// module.wildcard_cert.aws_acm_certificate_validation.this[0], the SAME
// resource TestManagedFromModuleOutputBlindCrosstalkKnownValidationResolvesConcrete
// already lets `certificate_arn` read a known VALUE from - so `found` ends
// up with exactly one entry and the guard lets it through.
//
// Mutation check (b) from the unit's own brief: reverting
// [resolver.managedFromModuleOutput]'s call in managedFromExprAt back to an
// unconditional decline (git stash this file's diff, or hand-edit the loop
// to `continue` instead of chasing/declining on a "module" root) makes this
// test fail - the instance stops resolving at all and diags carry only the
// "Non-static identity argument" refusal, not a sibling-apply Reason naming
// the ACM resource. Mutation check (a) is
// TestManagedFromModuleOutputBlindCrosstalk itself, unmodified above: it
// still must fail (Cognito misattribution) if the len(found) != 1 ambiguity
// guard this function also relies on is ever removed.
func TestManagedFromModuleOutputChasesThroughToACMResource(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "managed-read-module-blind-crosstalk"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: moduleBlindCrosstalkManagedResults(true, false),
		Schemas:        moduleBlindCrosstalkSchemas(),
	})
	if result == nil {
		t.Fatalf("resolution produced no result at all: %s", renderDiags(diags))
	}

	addr := mustAddr(t, `module.alb.aws_lb_listener_certificate.this["https/0"]`)
	res, ok := result.Get(addr)
	if !ok {
		t.Fatalf("%s did not resolve at all: %s", addr, renderDiags(diags))
	}
	if res.Class != ClassNeedsDiscovery {
		t.Fatalf("%s resolved %s (cause %s, args %v, reason %q), want NEEDS_DISCOVERY attributed to the ACM validation resource",
			addr, res.Class, res.Cause, res.CauseArgs, res.Reason)
	}
	if res.Cause != DiscoverySiblingApply {
		t.Errorf("%s resolved NEEDS_DISCOVERY with cause %s, want %s", addr, res.Cause, DiscoverySiblingApply)
	}
	// Module-qualified, not the bare "aws_acm_certificate_validation.this":
	// [resolver.qualifyFoundAddr] prefixes every candidate this package
	// discovers inside a child module with that module's own absolute path
	// the moment it is found, because two SIBLING module calls of the same
	// source (this fixture's "wildcard_cert" module is one of exactly the
	// shape corpus-alb-complete's real "acm" and "wildcard_cert" calls are)
	// can each declare their own same-named "aws_acm_certificate_validation.this",
	// and an unqualified `found` key would silently fold two DIFFERENT real
	// resources into one string - a wrong claim, not merely an imprecise one.
	wantSibling := "module.wildcard_cert.aws_acm_certificate_validation.this"
	if len(res.CauseArgs) == 0 || res.CauseArgs[0] != wantSibling {
		t.Fatalf("%s: CauseArgs = %v, want [0] == %q (the module's own validation resource, chased through module.wildcard_cert's acm_certificate_arn output, module-qualified)",
			addr, res.CauseArgs, wantSibling)
	}
	if !strings.Contains(res.Reason, wantSibling) {
		t.Errorf("%s: Reason %q does not name %q", addr, res.Reason, wantSibling)
	}
	if strings.Contains(res.Reason, "aws_cognito_user_pool") {
		t.Errorf("%s: Reason %q names Cognito, which is unrelated to this listener's certificate", addr, res.Reason)
	}
}

// TestValuesSplatPerElementProvenance is #397's own positive case, on the
// isolated testdata/values-splat-per-element fixture (see its own header
// comment): unlike TestManagedFromModuleOutputChasesThroughToACMResource
// above, BOTH the module-routed ACM leg AND Cognito's own pool ARN are
// unknown here at once - the shape [expansion.managedFrom]'s ONE
// combined, whole-expansion answer cannot tell apart, because chasing
// local.flat's own definition (managedFromExprAt's Variables() walk)
// reaches BOTH aws_cognito_user_pool.this and
// module.wildcard_cert.aws_acm_certificate_validation.this[0] from the SAME
// expression, whichever for_each key started the chase. Before #397's two
// fixes (staticCollElems's values() case and forEachExpansion capturing
// eachValueDeferred on the eval-succeeded path, not only the tolerant-retry
// one), aws_lb_listener_certificate.this["https/0"] had nothing but that
// one ambiguous answer to fall back on and declined outright - exactly
// TestManagedFromModuleOutputBlindCrosstalk's own shape, one level of
// map-of-maps deeper.
//
// With both fixes, ["https/0"]'s own eachValueDeferred entry is
// `{certificate_arn = module.wildcard_cert.acm_certificate_arn}` - ITS OWN
// element's expression, reached by decomposing local.flat's definition
// (merge(values(local.per_listener)...)) structurally instead of
// evaluating it as one opaque value - and [resolver.eachValueDeferredParts]
// selects certificate_arn out of THAT, reaching only the ACM leg. Cognito's
// own unknown, sitting in the SAME flattened map under a different key,
// never enters this instance's own chase at all.
func TestValuesSplatPerElementProvenance(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "values-splat-per-element"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: moduleBlindCrosstalkManagedResults(true, true),
		Schemas:        moduleBlindCrosstalkSchemas(),
	})
	if result == nil {
		t.Fatalf("resolution produced no result at all: %s", renderDiags(diags))
	}

	addr := mustAddr(t, `aws_lb_listener_certificate.this["https/0"]`)
	res, ok := result.Get(addr)
	if !ok {
		t.Fatalf("%s did not resolve at all: %s", addr, renderDiags(diags))
	}
	if res.Class != ClassNeedsDiscovery {
		t.Fatalf("%s resolved %s (cause %s, args %v, reason %q), want NEEDS_DISCOVERY attributed to the ACM validation resource",
			addr, res.Class, res.Cause, res.CauseArgs, res.Reason)
	}
	if res.Cause != DiscoverySiblingApply {
		t.Errorf("%s resolved NEEDS_DISCOVERY with cause %s, want %s", addr, res.Cause, DiscoverySiblingApply)
	}
	wantSibling := "module.wildcard_cert.aws_acm_certificate_validation.this"
	if len(res.CauseArgs) == 0 || res.CauseArgs[0] != wantSibling {
		t.Fatalf("%s: CauseArgs = %v, want [0] == %q (this instance's own ACM leg, reached through its own eachValueDeferred expression, not expansion.managedFrom's shared, ambiguous answer)",
			addr, res.CauseArgs, wantSibling)
	}
	if !strings.Contains(res.Reason, wantSibling) {
		t.Errorf("%s: Reason %q does not name %q", addr, res.Reason, wantSibling)
	}
	if strings.Contains(res.Reason, "aws_cognito_user_pool") {
		t.Errorf("%s: Reason %q names Cognito - the SIBLING instance's own unknown leaked into this one's attribution, which is exactly the crosstalk hazard this file exists to avoid", addr, res.Reason)
	}

	// The sibling instance must resolve too, and to Cognito specifically -
	// proving this is genuine per-element precision, not a lucky guess that
	// happens to land on the alphabetically-first (or map-iteration-first)
	// candidate. If both instances came back naming the SAME resource, that
	// would be [resolver.managedFromScope]'s coarse, shared answer winning
	// by accident on an expansion that (by construction here) contains only
	// two keys - the failure mode this test exists to rule out.
	cognitoAddr := mustAddr(t, `aws_lb_listener_certificate.this["cognito/0"]`)
	cognitoRes, ok := result.Get(cognitoAddr)
	if !ok {
		t.Fatalf("%s did not resolve at all: %s", cognitoAddr, renderDiags(diags))
	}
	if cognitoRes.Class != ClassNeedsDiscovery || cognitoRes.Cause != DiscoverySiblingApply {
		t.Fatalf("%s resolved %s/%s (args %v, reason %q), want NEEDS_DISCOVERY/%s attributed to Cognito",
			cognitoAddr, cognitoRes.Class, cognitoRes.Cause, cognitoRes.CauseArgs, cognitoRes.Reason, DiscoverySiblingApply)
	}
	if len(cognitoRes.CauseArgs) == 0 || cognitoRes.CauseArgs[0] != "aws_cognito_user_pool.this" {
		t.Fatalf("%s: CauseArgs = %v, want [0] == %q", cognitoAddr, cognitoRes.CauseArgs, "aws_cognito_user_pool.this")
	}
	if strings.Contains(cognitoRes.Reason, "acm_certificate") {
		t.Errorf("%s: Reason %q names the ACM resource - the SIBLING instance's own unknown leaked into this one's attribution", cognitoAddr, cognitoRes.Reason)
	}
}

// TestNestedForScopePerElementProvenance is #397's remaining half, on
// testdata/nested-for-scope-per-element: the REAL
// terraform-aws-modules/terraform-aws-alb local.additional_certs shape
// (main.tf:456-473) with TWO cert-carrying listeners instead of one, so
// per-element attribution is provable in both directions.
//
// Two mechanisms have to hold at once for either instance to resolve:
//
//   - The outer comprehension's per-listener VALUE clause is itself a
//     for-expression over `lookup(listener_values, ...)`, reading the OUTER
//     comprehension's own loop variable. Decomposing it requires the outer
//     element's binding to be in scope while the inner one is chased, which
//     is what threading instScope through staticCollElems/forExprElems/
//     forSourceElements/evaluatedCollElements buys.
//   - The outer filter is
//     `length(lookup(listener_values, "additional_certificate_arns", [])) > 0`.
//     [resolver.forCondIncludesTolerant] previously recognised only a bare
//     lookup()/try() as the WHOLE condition, so this filter could not be
//     decided without listener_values' own value - which never proves,
//     because one listener's list holds a module output and another's holds
//     an unapplied resource attribute.
//
// Both ManagedResults are unknown here, exactly as in
// TestValuesSplatPerElementProvenance: that is the configuration
// [expansion.managedFrom]'s one shared, expansion-wide answer cannot tell
// apart, because chasing local.additional_certs' own definition reaches
// BOTH legs from the same expression whichever key started the chase.
//
// The third listener ("plain") carries no additional_certificate_arns at
// all, so the filter must DROP it: an aws_lb_listener_certificate instance
// keyed "plain/0" existing at all would be a key set invented from a
// default, and is asserted absent below.
func TestNestedForScopePerElementProvenance(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "nested-for-scope-per-element"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: moduleBlindCrosstalkManagedResults(true, true),
		Schemas:        moduleBlindCrosstalkSchemas(),
	})
	if result == nil {
		t.Fatalf("resolution produced no result at all: %s", renderDiags(diags))
	}

	httpsAddr := mustAddr(t, `module.alb.aws_lb_listener_certificate.this["https/0"]`)
	res, ok := result.Get(httpsAddr)
	if !ok {
		t.Fatalf("%s did not resolve at all: %s", httpsAddr, renderDiags(diags))
	}
	if res.Class != ClassNeedsDiscovery || res.Cause != DiscoverySiblingApply {
		t.Fatalf("%s resolved %s/%s (args %v, reason %q), want NEEDS_DISCOVERY/%s attributed to the ACM validation resource",
			httpsAddr, res.Class, res.Cause, res.CauseArgs, res.Reason, DiscoverySiblingApply)
	}
	wantSibling := "module.wildcard_cert.aws_acm_certificate_validation.this"
	if len(res.CauseArgs) == 0 || res.CauseArgs[0] != wantSibling {
		t.Fatalf("%s: CauseArgs = %v, want [0] == %q (this listener's OWN additional_certificate_arns element, not the expansion-wide answer)",
			httpsAddr, res.CauseArgs, wantSibling)
	}
	if strings.Contains(res.Reason, "aws_cognito_user_pool") {
		t.Errorf("%s: Reason %q names Cognito - the sibling listener's own unknown leaked into this one's attribution", httpsAddr, res.Reason)
	}

	cognitoAddr := mustAddr(t, `module.alb.aws_lb_listener_certificate.this["cognito/0"]`)
	cognitoRes, ok := result.Get(cognitoAddr)
	if !ok {
		t.Fatalf("%s did not resolve at all: %s", cognitoAddr, renderDiags(diags))
	}
	if cognitoRes.Class != ClassNeedsDiscovery || cognitoRes.Cause != DiscoverySiblingApply {
		t.Fatalf("%s resolved %s/%s (args %v, reason %q), want NEEDS_DISCOVERY/%s attributed to Cognito",
			cognitoAddr, cognitoRes.Class, cognitoRes.Cause, cognitoRes.CauseArgs, cognitoRes.Reason, DiscoverySiblingApply)
	}
	if len(cognitoRes.CauseArgs) == 0 || cognitoRes.CauseArgs[0] != "aws_cognito_user_pool.this" {
		t.Fatalf("%s: CauseArgs = %v, want [0] == %q", cognitoAddr, cognitoRes.CauseArgs, "aws_cognito_user_pool.this")
	}
	if strings.Contains(cognitoRes.Reason, "acm_certificate") {
		t.Errorf("%s: Reason %q names the ACM resource - the sibling listener's own unknown leaked into this one's attribution", cognitoAddr, cognitoRes.Reason)
	}

	// The filter must drop the listener with no additional_certificate_arns
	// at all: lookup()'s [] default means zero certificates, never one.
	if _, ok := result.Get(mustAddr(t, `module.alb.aws_lb_listener_certificate.this["plain/0"]`)); ok {
		t.Errorf(`module.alb.aws_lb_listener_certificate.this["plain/0"] exists; the "plain" listener declares no additional_certificate_arns, so the length(lookup(...)) > 0 filter must exclude it entirely`)
	}
}

// TestNestedForScopeKnownValidationRendersIdentityByValue is the by-value
// half of the same fixture, and the negative control that keeps #397's two
// fixes a RULE rather than a licence to resolve something.
//
// Once module.wildcard_cert's own validation resource has resolved (its
// certificate_arn is a known ARN) while Cognito's pool ARN has not,
// module.alb.aws_lb_listener_certificate.this["https/0"] must render a
// CONCRETE identity built from ITS OWN listener's certificate - asserted
// here as the exact string that becomes the import ID and the marker - and
// the "cognito/0" sibling, whose own ARN is still unknown, must not.
//
// It does NOT require #397's two fixes to pass: with the validation ARN
// known, the whole for_each expression evaluates and the structural chase is
// never reached, which is exactly why this belongs here as a control. What
// it catches is the other direction - a chase that answers from the WRONG
// element. Making elemVarSource select nothing (pass nil instead of the
// traversal steps, so the whole listener object stands in for its
// certificate list) fails this test with invented instance keys
// "https/certificate_arn" and "cognito/certificate_arn" colliding on one
// identity, which is precisely the wrong-marker shape the value assertion
// exists to catch. Both of the sibling test's own assertions fail under the
// same mutation.
func TestNestedForScopeKnownValidationRendersIdentityByValue(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "nested-for-scope-per-element"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: moduleBlindCrosstalkManagedResults(false, true),
		Schemas:        moduleBlindCrosstalkSchemas(),
	})
	if result == nil {
		t.Fatalf("resolution produced no result at all: %s", renderDiags(diags))
	}

	addr := mustAddr(t, `module.alb.aws_lb_listener_certificate.this["https/0"]`)
	res, ok := result.Get(addr)
	if !ok {
		t.Fatalf("%s did not resolve at all: %s", addr, renderDiags(diags))
	}
	if res.Class != ClassConcrete {
		t.Fatalf("%s resolved %s (cause %s, args %v, reason %q), want CONCRETE - its own certificate_arn is a known value",
			addr, res.Class, res.Cause, res.CauseArgs, res.Reason)
	}
	want := "arn:aws:elasticloadbalancing:us-east-1:1:listener/app/x/1/2_arn:aws:acm:us-east-1:1:certificate/real-wildcard-cert"
	if res.ImportID != want {
		t.Errorf("%s: ImportID %q, want %q", addr, res.ImportID, want)
	}

	// The sibling reads a resource this run has NOT resolved, so it must not
	// come back concrete off the neighbour's known value.
	cognitoAddr := mustAddr(t, `module.alb.aws_lb_listener_certificate.this["cognito/0"]`)
	if cognitoRes, ok := result.Get(cognitoAddr); ok && cognitoRes.Class == ClassConcrete {
		t.Errorf("%s resolved CONCRETE with ImportID %q; aws_cognito_user_pool.this.arn is unknown in this run, so this instance's certificate_arn cannot be known - the sibling's value leaked",
			cognitoAddr, cognitoRes.ImportID)
	}
}
