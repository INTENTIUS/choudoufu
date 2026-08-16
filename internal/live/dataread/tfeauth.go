// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/opentofu/svchost"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/cliconfig"
	"github.com/intentius/choudoufu/internal/configs"
)

// defaultTfeHostname is what the tfe provider itself defaults "hostname" to
// when the provider block does not set one.
const defaultTfeHostname = "app.terraform.io"

// tfeAuthAvailable draws tfe_outputs' own eligibility rule, checked offline
// the same way rule 3 checks provider-configurability: from the provider
// block (already proven statically evaluable by the time this runs) and the
// process environment, never by starting the provider. Three surfaces, in
// the order the tfe provider itself documents checking them:
//
//  1. an explicit token argument in the provider block;
//  2. the TFE_TOKEN environment variable;
//  3. a credentials entry for the provider's host in the CLI configuration
//     (~/.terraform.d/credentials.tfrc.json, or TF_CLI_CONFIG_FILE) - the
//     same file "tofu login" writes and internal/command/cliconfig already
//     reads, so this reuses that lookup rather than re-parsing the file.
//
// None of this touches the network or spawns the plugin: a provider block
// this permissive can still fail to configure for other reasons (a bad
// token, a host that rejects it), and that failure surfaces at read time
// under the same [SummaryCrossStackOutputsUnavailable] class, quoted from
// the provider. What this rule exists to prevent is attempting a read at
// all when no credential exists anywhere this fork knows to look - the
// common CI shape, and the one case a read call cannot honestly attempt.
func (an *analyzer) tfeAuthAvailable(providerCfg *configs.Provider) (bool, string) {
	host := defaultTfeHostname
	var tokenExpr, hostExpr *hclsyntax.Attribute
	if providerCfg != nil {
		if body, ok := providerCfg.Config.(*hclsyntax.Body); ok {
			tokenExpr = body.Attributes["token"]
			hostExpr = body.Attributes["hostname"]
		}
	}

	if hostExpr != nil {
		if v, ok := an.evalRootString(hostExpr.Expr, "hostname"); ok {
			host = v
		}
	}

	if tokenExpr != nil && an.evalRootPresent(tokenExpr.Expr) {
		return true, ""
	}

	if v, ok := os.LookupEnv("TFE_TOKEN"); ok && v != "" {
		return true, ""
	}

	if hostname, err := svchost.ForComparison(host); err == nil {
		if tfeCredentialsAvailable(an.ctx, hostname) {
			return true, ""
		}
	}

	return false, fmt.Sprintf(
		"its provider has no token available for host %q: no token argument in the provider configuration, TFE_TOKEN is not set in the environment, and the CLI configuration has no credentials entry for that host; set TFE_TOKEN, add a token argument, or run \"tofu login %s\"",
		host, host)
}

// evalRootExpr evaluates one expression through the root module's static
// evaluator, unguarded by any data-source coverage - the same evaluation
// [analyzer.evalProviderExpr] performs, but returning the value instead of
// discarding it, because this rule needs to know what a token or hostname
// argument evaluated to, not merely that it could be evaluated.
func (an *analyzer) evalRootExpr(expr hclsyntax.Expression, label string) (val cty.Value, ok bool) {
	defer func() {
		if recover() != nil {
			val, ok = cty.NilVal, false
		}
	}()
	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("provider auth %s", label),
		DeclRange: expr.Range(),
	}
	v, hclDiags := an.cfg.Module.StaticEvaluator.Pure().Evaluate(an.ctx, expr, ident)
	if hclDiags.HasErrors() {
		return cty.NilVal, false
	}
	return v, true
}

// evalRootString evaluates expr and returns it as a non-empty string, when
// it is one. Never used on the token expression: a token's value must never
// enter a diagnostic, so [analyzer.evalRootPresent] only asks whether one
// exists.
func (an *analyzer) evalRootString(expr hclsyntax.Expression, label string) (string, bool) {
	v, ok := an.evalRootExpr(expr, label)
	if !ok {
		return "", false
	}
	unmarked, _ := v.UnmarkDeep()
	if unmarked.IsNull() || !unmarked.IsWhollyKnown() {
		return "", false
	}
	s, err := convert.Convert(unmarked, cty.String)
	if err != nil {
		return "", false
	}
	str := s.AsString()
	if str == "" {
		return "", false
	}
	return str, true
}

// evalRootPresent reports whether expr evaluates to a known, non-null
// value, without reading what that value is: the token's own text is never
// safe to carry into a diagnostic or a log line.
func (an *analyzer) evalRootPresent(expr hclsyntax.Expression) bool {
	v, ok := an.evalRootExpr(expr, "token")
	if !ok {
		return false
	}
	return !v.IsNull() && v.IsWhollyKnown()
}

// tfeCredentialsAvailable reports whether the CLI configuration - files on
// disk plus TF_TOKEN_* environment variables, exactly what
// [cliconfig.CredentialsSource] already resolves for "tofu login" and
// registry access - has a credentials entry for host. No credentials
// helper plugin is consulted: discovering and running one is a process
// launch, which the offline eligibility analysis must not do; a helper that
// would have answered surfaces instead as a read-time failure under
// [SummaryCrossStackOutputsUnavailable], quoted from the provider.
func tfeCredentialsAvailable(ctx context.Context, host svchost.Hostname) bool {
	cfg, diags := cliconfig.LoadConfig(ctx)
	if diags.HasErrors() || cfg == nil {
		return false
	}
	source, err := cfg.CredentialsSource(nil)
	if err != nil {
		return false
	}
	creds, err := source.ForHost(ctx, host)
	if err != nil {
		return false
	}
	return creds != nil
}
