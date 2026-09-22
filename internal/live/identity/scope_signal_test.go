// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// signalScopeRoot is GitHub issue #1470's shape: one block the run is
// about, beside the ACM/Route53 validation pair whose record has a for_each
// no static evaluation can enumerate ("Non-static for_each expression").
// The pair is what -target=aws_cloudwatch_log_group.wanted drops from the
// plan graph, so the scope below excludes both of them.
const signalScopeRoot = `
resource "aws_cloudwatch_log_group" "wanted" {
  name = "/wanted"
}

resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
`

// TestScopeWithholdsAnExcludedBlocksExpansionRefusal is GitHub issue #1470.
//
// [resolver.walkOutOfScope] promises that a block -target / -exclude removed
// from the plan graph "cannot refuse the run", and rolls back whatever its
// own attempt raised. It did not get the chance for a for_each refusal:
// [resolver.collectSignal] runs before the walk, called expansionFor on
// every block with no scope at all, and the record's "Non-static for_each
// expression" was raised there. expansionFor memoizes the failure, so
// walkOutOfScope's own call returned false with no new diagnostic and its
// rollback removed nothing. One error survived to the caller, for a block
// the run excluded.
//
// The in-scope arm is the mutation check on the fix: the same
// configuration with the record IN scope still refuses, so the out-of-scope
// arm cannot pass against a resolver that stopped refusing the shape.
func TestScopeWithholdsAnExcludedBlocksExpansionRefusal(t *testing.T) {
	cfg := writeScopeFixture(t, signalScopeRoot, "")

	t.Run("out of scope", func(t *testing.T) {
		res, diags := ResolveWith(t.Context(), cfg, Context{
			Scope: scopeOnly("aws_cloudwatch_log_group.wanted"),
		})
		if n := countErrors(diags); n != 0 {
			t.Fatalf("a targeted run refused with %d error(s) for a block outside its own scope:\n%s", n, renderScopeDiags(diags))
		}
		// The certificate is out of scope and resolves anyway, for
		// TestScopeStillDeclaresWhatItDoesNotEvaluate's reason; the record
		// cannot, so it is absent exactly as the plan's targeting made it.
		assertResolved(t, res, []string{
			"aws_acm_certificate.cert",
			"aws_cloudwatch_log_group.wanted",
		})
	})

	t.Run("in scope", func(t *testing.T) {
		for name, scope := range map[string]Scope{
			"untargeted": nil,
			"everything": func(addrs.ConfigResource) bool { return true },
		} {
			_, diags := ResolveWith(t.Context(), cfg, Context{Scope: scope})
			if !hasErrorSummary(diags, "Non-static for_each expression") {
				t.Errorf("%s: the record's for_each no longer refuses, so the out-of-scope arm proves nothing:\n%s", name, renderScopeDiags(diags))
			}
		}
	})
}

// TestScopeWithholdsAnExcludedChildModuleBlocksExpansionRefusal is the same
// defect one module down: [resolver.collectSignalInto] recurses into every
// child, and a child's excluded block was expanded there just as unscoped
// as a root one.
func TestScopeWithholdsAnExcludedChildModuleBlocksExpansionRefusal(t *testing.T) {
	cfg := writeScopeFixture(t, `
resource "aws_cloudwatch_log_group" "wanted" {
  name = "/wanted"
}

module "child" {
  source = "./child"
}
`, `
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
`)

	if _, diags := ResolveWith(t.Context(), cfg, Context{}); !hasErrorSummary(diags, "Non-static for_each expression") {
		t.Fatalf("the child module's record no longer refuses; the case below proves nothing:\n%s", renderScopeDiags(diags))
	}

	res, diags := ResolveWith(t.Context(), cfg, Context{
		Scope: scopeOnly("aws_cloudwatch_log_group.wanted"),
	})
	if n := countErrors(diags); n != 0 {
		t.Fatalf("excluding the child module's record did not withhold its expansion refusal (%d error(s)):\n%s", n, renderScopeDiags(diags))
	}
	assertResolved(t, res, []string{
		"aws_cloudwatch_log_group.wanted",
		"module.child.aws_acm_certificate.cert",
	})
}

func countErrors(diags tfdiags.Diagnostics) int {
	n := 0
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			n++
		}
	}
	return n
}

func hasErrorSummary(diags tfdiags.Diagnostics, summary string) bool {
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == summary {
			return true
		}
	}
	return false
}

func renderScopeDiags(diags tfdiags.Diagnostics) string {
	var b strings.Builder
	for _, d := range diags {
		desc := d.Description()
		fmt.Fprintf(&b, "  %c: %s: %s\n", d.Severity(), desc.Summary, desc.Detail)
	}
	return b.String()
}
