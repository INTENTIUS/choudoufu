// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1052. A least-privilege role that cannot call each Cloud
// Control list handler's own read - xray:GetGroups for AWS::XRay::Group,
// access-analyzer:ListAnalyzers for AWS::AccessAnalyzer::Analyzer - gets
// AccessDeniedException per type, and the sweep used to raise one
// "Incomplete sweep" warning per type. The first real-AWS run of
// examples/ci-pipelines printed hundreds of them, and the one fatal error
// of that run sat underneath.
//
// The ruling (2026-09-21): one warning, grouped by cause. Every AccessDenied
// Cloud Control type collapses to one warning with the count, the first
// five type names and the one IAM action pattern to grant. Other causes
// keep their own line. The full list goes to the log. Nothing is hidden.
//
// So a denied listing goes through [sweepGapDenied] rather than
// [sweepGapDiag]: the gap is recorded on [Result.SweepGaps] exactly as
// before (the sweep-coverage section and the JSON view still itemize every
// one), one [WARN] log line names the type and the action its denial
// named, and the warning itself is deferred to [deniedSweepDiag], which
// [Discover] raises once at the end of the run.
//
// GitHub issue #1513: a live-plan runs Discover once per provider
// configuration, so "once at the end of Discover" was once per
// configuration. Such a caller sets [Request.DeferDeniedSweepWarning] and
// raises [DeniedSweepWarning] over all of its passes' results instead; the
// log line per type names the configuration it was denied through.

// sweepDenial is one Cloud Control listing this run's own credential was
// refused.
type sweepDenial struct {
	typeName string // the provider type
	cfnType  string // the Cloud Control type the listing was made on
	action   string // "<service>:<Action>" the denial named, or "" when it did not
	provider string // the provider configuration the listing went through, short form, or "" when the caller named none
}

// providerConfigLabel is a provider configuration as an operator writes it
// in a resource block's provider argument: "aws", "aws.west", or under a
// module "module.net.aws.west". "" for the zero value, which a caller that
// names no configuration (a package test, a single standalone pass) leaves.
func providerConfigLabel(p addrs.AbsProviderConfig) string {
	if p.Provider.Type == "" {
		return ""
	}
	label := p.Provider.Type
	if p.Alias != "" {
		label += "." + p.Alias
	}
	if !p.Module.IsRoot() {
		label = p.Module.String() + "." + label
	}
	return label
}

// deniedActionRE finds the IAM action an AWS AccessDeniedException names:
// "User: arn:... is not authorized to perform: xray:GetGroups on resource:
// ... because no identity-based policy allows the xray:GetGroups action".
var deniedActionRE = regexp.MustCompile(`is not authorized to perform: ([A-Za-z0-9-]+:[A-Za-z0-9*]+)`)

// deniedAction is the "<service>:<Action>" a denial names, or "" when the
// message is not in AWS's shape (an emulator, a proxy that rewrote it).
func deniedAction(err error) string {
	if err == nil {
		return ""
	}
	m := deniedActionRE.FindStringSubmatch(err.Error())
	if m == nil {
		return ""
	}
	return m[1]
}

// sweepGapDenied is [sweepGapDiag] for a sweep listing Cloud Control
// refused with AccessDeniedException: the gap is recorded, the denial is
// logged with the action it named, and the warning is deferred to
// [deniedSweepDiag] so the run raises one for all of them. It returns no
// diagnostic of its own.
func sweepGapDenied(req Request, res *Result, g SweepGap, cfnType string, err error) tfdiags.Diagnostics {
	res.SweepGaps = append(res.SweepGaps, g)
	action := deniedAction(err)
	provider := providerConfigLabel(req.VouchProvider)
	res.sweepDenied = append(res.sweepDenied, sweepDenial{typeName: g.TypeName, cfnType: cfnType, action: action, provider: provider})
	needs := action
	if needs == "" {
		needs = "an action the denial did not name"
	}
	// The configuration, when the caller named one, is what says whose
	// credential was refused: two configurations are two roles.
	through := ""
	if provider != "" {
		through = ", through provider " + provider
	}
	log.Printf("[WARN] stateless/discovery: sweep denied: Cloud Control ListResources on %s (for %s%s) needs %s: %v", cfnType, g.TypeName, through, needs, err)
	return nil
}

// deniedTypesNamed is how many denied types the one warning names before
// saying "and N more"; the rest are in the log.
const deniedTypesNamed = 5

// grantPatternsNamed is how many per-service patterns the one warning
// names when the denials span more services than fit on a few lines.
const grantPatternsNamed = 5

// DeniedSweepWarning is the one warning for every Cloud Control listing
// the run's credentials were refused, over every pass the caller ran with
// [Request.DeferDeniedSweepWarning] set (GitHub issue #1513): one count of
// distinct types, the first five of them, and one grant pattern over every
// action the denials named, whichever provider configuration each came
// through. Nothing when nothing was denied. A nil result is skipped.
func DeniedSweepWarning(results ...*Result) tfdiags.Diagnostics {
	var denials []sweepDenial
	for _, r := range results {
		if r != nil {
			denials = append(denials, r.sweepDenied...)
		}
	}
	return deniedSweepDiag(denials)
}

// deniedSweepDiag is the one warning for every Cloud Control listing in
// denials, raised once by [Discover] after the sweep, or by
// [DeniedSweepWarning] over several passes. Nothing when nothing was
// denied.
func deniedSweepDiag(denials []sweepDenial) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(denials) == 0 {
		return diags
	}

	seen := map[string]bool{}
	var cfnTypes, actions, providers []string
	for _, d := range denials {
		if !seen["t "+d.cfnType] {
			seen["t "+d.cfnType] = true
			cfnTypes = append(cfnTypes, d.cfnType)
		}
		if d.action != "" && !seen["a "+d.action] {
			seen["a "+d.action] = true
			actions = append(actions, d.action)
		}
		if d.provider != "" && !seen["p "+d.provider] {
			seen["p "+d.provider] = true
			providers = append(providers, d.provider)
		}
	}
	sort.Strings(cfnTypes)
	sort.Strings(actions)
	sort.Strings(providers)

	// One configuration is one role, and the text says "the role" as it
	// always has. Several are several roles, each refused on its own
	// types: the warning names them, and the log says which type went
	// through which.
	through, role, perLine := "", "the role", "Every denied type and the action it named"
	if len(providers) > 1 {
		through = " through provider configurations " + joinAnd(providers)
		role = "their roles"
		perLine = "Every denied type, the configuration it went through and the action it named"
	}

	// The full list, on one line, for a reader who has the log and not the
	// per-type lines above it.
	log.Printf("[WARN] stateless/discovery: the sweep was denied Cloud Control ListResources on %d %s; each is logged above with the action its denial named. Denied: %s",
		len(cfnTypes), plural(len(cfnTypes), "type", "types"), strings.Join(cfnTypes, ", "))

	return diags.Append(tfdiags.Sourceless(
		tfdiagsSeverity(SeverityForRefusal(SummaryIncompleteSweep)),
		SummaryIncompleteSweep,
		fmt.Sprintf(
			"Cloud Control ListResources was denied for %d of the %s the sweep covers (%s)%s, so a resource of any of those types this estate owns but no longer declares WILL NOT be proposed for destruction by this run. Each denial names the read its type's list handler makes. Grant %s %s, then re-run. %s is one [WARN] line in the log: run with TF_LOG=WARN, or TF_LOG_PATH to write it to a file.",
			len(cfnTypes), plural(len(cfnTypes), "type", "types"), nameFirst(cfnTypes, deniedTypesNamed), through, role, grantPattern(actions), perLine),
	))
}

// nameFirst renders the first n of names and counts the rest: "A, B, C, D,
// E and 295 more".
func nameFirst(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:n], ", "), len(names)-n)
}

// verbRE is the leading verb of an IAM action name: Get in GetGroups,
// Describe in DescribeDBInstances.
var verbRE = regexp.MustCompile(`^[A-Z][a-z]+`)

// grantPattern is the IAM action pattern that covers every action the
// denials named, collapsed as far as the set allows: the one action when
// there is one, otherwise one "<service>:<Verb>*" per service and verb
// (Cloud Control list handlers use read verbs - List, Describe, Get - so a
// service's denials usually collapse to one pattern), and when the services
// are more than fit on a few lines, the first of those with the count of
// the rest, or the AWS-managed policy that reads the whole account.
func grantPattern(actions []string) string {
	switch len(actions) {
	case 0:
		return "the action each denial names (in the log)"
	case 1:
		return actions[0]
	}
	seen := map[string]bool{}
	var patterns []string
	for _, a := range actions {
		service, name, ok := strings.Cut(a, ":")
		pattern := a
		if ok {
			if verb := verbRE.FindString(name); verb != "" && verb != name {
				pattern = service + ":" + verb + "*"
			}
		}
		if !seen[pattern] {
			seen[pattern] = true
			patterns = append(patterns, pattern)
		}
	}
	sort.Strings(patterns)
	if len(patterns) <= grantPatternsNamed+1 {
		return strings.Join(patterns, ", ")
	}
	return fmt.Sprintf("%s and %d more per-service patterns (every one is in the log), or AWS's managed ReadOnlyAccess policy if it may read the whole account",
		strings.Join(patterns[:grantPatternsNamed], ", "), len(patterns)-grantPatternsNamed)
}

// joinAnd renders names as "a and b" or "a, b and c".
func joinAnd(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// plural picks the noun for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
