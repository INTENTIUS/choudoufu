// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Context is what a caller can tell the analysis about the world outside the
// configuration. It is [lint.Context] and [identity.Context]'s shared field,
// under one name, because both passes must be told the same thing: a type
// the schemas admit has to read the same answer at both, or a lint refusal
// and a resolution refusal will disagree about the same type.
type Context struct {
	// Schemas are the provider's managed resource type schemas, keyed by
	// type name.
	//
	// Running without them is supported and is materially less accurate,
	// in one direction that matters to this instrument specifically: a
	// type absent from the generated admission table is refused as
	// unadmitted when the provider's own identity schema would have
	// settled it. That single rule would then top any ranking built from
	// the result, which is the one outcome #102 exists to prevent. So
	// [Report.Schemas] records whether they were present and every front
	// end says so.
	Schemas map[string]providers.Schema
}

// Analyze runs the configuration-only passes over one loaded configuration
// and returns what refused it.
//
// This is the whole of the shared instrument. "choudoufu live-check" renders
// one of these for a human; tools/corpus-gen folds many into a ranking. Both
// see the same findings from the same passes, which is what keeps the
// project's published compatibility claim and a user's own verdict from
// drifting apart.
func Analyze(ctx context.Context, cfg *configs.Config, actx Context) Report {
	report := Report{
		Schemas:   len(actx.Schemas) > 0,
		Checked:   CheckedLayers(),
		Unchecked: UncheckedLayers(),
	}
	if cfg == nil {
		return report
	}

	findings := findingMap{}

	for _, issue := range lint.CheckWith(ctx, cfg, lint.Context{Schemas: actx.Schemas}) {
		site := Site{
			Address: issue.Construct,
			Type:    issue.Type,
			Module:  issue.Module.String(),
			Detail:  issue.Detail,
			File:    issue.Subject.Filename,
			Line:    issue.Subject.Start.Line,
			Column:  issue.Subject.Start.Column,

			StartByte: issue.Subject.Start.Byte,
			EndByte:   issue.Subject.End.Byte,
		}
		// A warning-severity rule (GitHub issue #210: [lint.RuleStateBackend]
		// is the first) is not a blocking finding: it goes to Warnings, the
		// same non-fatal channel identity's schema-disagreement diagnostics
		// already use below, so that [Report.Blocked] and the onboarding
		// ladder (ClassifyOnboarding, which reads Findings only) see it the
		// same way they see every other non-fatal signal.
		if issue.Rule.Severity() == lint.SeverityWarning {
			report.addWarning(LayerLint, string(issue.Rule), site)
			continue
		}
		f := findings.get(LayerLint, string(issue.Rule))
		f.add(site)
	}

	// Where lint already refused a construct, identity's verdict on the
	// same construct is not a second refusal to count.
	//
	// The lint and identity passes ask the same question of some constructs on purpose -
	// lint's admission check and identity's resolution consult the same
	// schemas so that "a lint refusal and a resolution refusal never
	// disagree about the same type". In a live-plan run only one is ever
	// seen, because lint is fatal and identity never runs. Here both run,
	// so that a configuration blocked by lint still reports what identity
	// would have said about everything else - but counting one unadmitted
	// resource as two blocked refusals would inflate exactly the ranking
	// this instrument exists to produce.
	refusedByLint := make(map[string]bool)
	for _, f := range findings {
		for _, site := range f.Sites {
			if loc := site.location(); loc != "" {
				refusedByLint[loc] = true
			}
		}
	}

	result, diags := identity.ResolveWith(ctx, cfg, identity.Context{Schemas: actx.Schemas})
	if result != nil {
		report.Instances = result.Len()
	}

	// Issue #179's third checked pass, computed lazily: the eligibility
	// analysis probes resolution a few more times, and a configuration
	// whose refusals never name a data source should not pay for it.
	var dataAnalysis *dataread.Analysis
	analysis := func() *dataread.Analysis {
		if dataAnalysis == nil {
			dataAnalysis = dataread.Analyze(ctx, cfg, dataread.Options{Schemas: actx.Schemas})
		}
		return dataAnalysis
	}

	// eligibleAddrs is the set of resource-instance addresses (in
	// [addrs.AbsResourceInstance.String] form) whose own identity
	// resolution failed for no reason other than a data-read-eligible
	// site: the direct case below adds one the moment classifyDataSite
	// says Eligible, and the cascade fixpoint after the main loop adds
	// one for every downstream instance whose sole failure traces back to
	// one already in the set. See [classifyCascadeSite].
	eligibleAddrs := map[string]bool{}

	// cascades holds every "Unresolvable identity" diagnostic whose
	// parent could not be classified on this pass, because its own
	// diagnostic - if it names a data read at all - has not been seen
	// yet. They are resolved in one fixpoint pass after the loop, so
	// finding order never matters.
	var cascades []cascadeSite

	// signal is collected once, over the whole configuration, the same way
	// lint.go collects it before calling admitted(): a schema-fallback
	// verdict for a type depends on what the whole configuration sets for
	// it, not on how much of this loop has run by the time a given cascade
	// asks. Left nil when there are no schemas to fall back to -
	// [identity.SynthesizeTypeIdentity] refuses immediately in that case
	// and never consults it - so a signal computed for nothing is never a
	// static-evaluator walk this run does not need.
	var signal *identity.ConfigSignal
	if len(actx.Schemas) > 0 {
		signal, _ = identity.ScanConfig(ctx, cfg)
	}

	for _, diag := range diags {
		desc := diag.Description()
		site := Site{
			Detail:   desc.Detail,
			Category: tfdiags.ExtraInfo[configs.ReferenceCategory](diag),
		}
		if src := diag.Source(); src.Subject != nil {
			site.File = src.Subject.Filename
			site.Line = src.Subject.Start.Line
			site.Column = src.Subject.Start.Column
			site.StartByte = src.Subject.Start.Byte
			site.EndByte = src.Subject.End.Byte
		}

		switch diag.Severity() {
		case tfdiags.Error:
			if loc := site.location(); loc != "" && refusedByLint[loc] {
				report.Shadowed++
				continue
			}
			// A same-stack, tfe_outputs, or terraform_remote_state
			// data-source refusal is re-homed under the data-read pass's
			// own classification: an eligible site is no longer a language
			// refusal at all (a live-plan reads the value before
			// resolution), and an ineligible one gets the class-specific
			// wording instead of the generic dynamic-value text.
			if layer, id, detail, ok := classifyDataSite(diag, analysis); ok {
				site.Detail = detail
				f := findings.get(layer, id)
				f.add(site)
				if layer == LayerDataread && id == dataread.SummaryEligibleRead {
					if addr := neededByResourceAddr(tfdiags.ExtraInfo[configs.RefusedReference](diag).NeededBy); addr != "" {
						eligibleAddrs[addr] = true
					}
				}
				continue
			}
			// "Unresolvable identity" is the one diagnostic
			// [identity.ResolveWith] raises when a resource's failure is
			// not its own: it depends on another instance whose identity
			// could not be built (internal/live/identity/resolve.go's
			// parentPart). That upstream instance's own diagnostic - the
			// one that actually explains the failure - is elsewhere in
			// this same slice and carries no reference back to this one,
			// so it cannot be classified here; it is held for the
			// fixpoint pass below instead of being resolved now.
			if child, parent, ok := parseCascadeDetail(desc.Summary, desc.Detail); ok {
				cascades = append(cascades, cascadeSite{
					site:   site,
					child:  child,
					parent: parent,
					safe:   dependentSafeToReclassify(stripAttrSuffix(child), actx.Schemas, signal),
				})
				continue
			}
			f := findings.get(LayerIdentity, desc.Summary)
			f.add(site)
		default:
			// Warnings are collected but never make the verdict negative.
			// identity's schema-disagreement refusal is the population
			// here, and it means provider-version skew rather than a
			// configuration this mode cannot move.
			report.addWarning(LayerIdentity, desc.Summary, site)
		}
	}

	// Fixpoint: an instance whose only failure is depending on an
	// eligible instance is itself eligible, and that makes anything
	// depending on IT eligible too, transitively. Bounded by len(cascades)
	// rounds - the underlying dependency graph is acyclic (identity
	// refuses a real cycle as its own diagnostic, "Circular identity
	// reference", which never matches parseCascadeDetail) - so this
	// always terminates without needing a visited set.
	resolved := make([]bool, len(cascades))
	for pass := 0; pass < len(cascades)+1; pass++ {
		changed := false
		for i, c := range cascades {
			if resolved[i] || !eligibleAddrs[c.parent] {
				continue
			}
			if !c.safe {
				// GitHub issue #221: resolveInstance
				// (internal/live/identity/resolve.go) returns at the FIRST
				// failing component of entry.Components, so when the
				// dependent's own type has a second real component this
				// diagnostic's parent trace proves nothing about it - it
				// was never reached, let alone checked. Reclassifying here
				// would tell the operator "no configuration edit is
				// needed" while a second, wholly unrelated required
				// argument might have no value at all. Leaving resolved[i]
				// false keeps it on the default hard-refusal path below,
				// and also withholds eligibility from anything that in
				// turn depends on THIS instance's identity, so the
				// conservatism propagates rather than stopping at one hop.
				continue
			}
			resolved[i] = true
			// c.child is [configs.StaticIdentifier.Subject] - the
			// resource-instance address plus ".<attribute>", the same
			// shape [neededByResourceAddr] strips down from NeededBy - so
			// a THIRD instance that depends on THIS one's identity
			// attribute (a chain more than one hop deep, e.g. a scaling
			// policy that names a scaling target that names a service
			// that reads a data source) can match it as a parent on the
			// next round. Without stripping the attribute here, this key
			// would carry ".<attribute>" while every [cascadeSite.parent]
			// never does, and the two would never compare equal - the
			// chain would stop propagating after exactly one hop.
			if addr := stripAttrSuffix(c.child); addr != "" {
				eligibleAddrs[addr] = true
			}
			changed = true
		}
		if !changed {
			break
		}
	}
	for i, c := range cascades {
		if !resolved[i] {
			// Default direction: a cascade this pass cannot trace to an
			// eligible read stays exactly what it always was, a hard
			// "Unresolvable identity" language refusal. An unrecognised
			// or genuinely unrelated failure never reads as "fine".
			f := findings.get(LayerIdentity, "Unresolvable identity")
			f.add(c.site)
			continue
		}
		c.site.Detail = fmt.Sprintf(
			"%s. %s's own identity depends on a data source that a live-plan resolves by reading it from the provider before identity resolution, so this reference resolves too. "+
				"No configuration edit is needed; the read itself was not performed by this check and can still fail at plan time.",
			c.site.Detail, c.parent)
		f := findings.get(LayerDataread, dataread.SummaryEligibleRead)
		f.add(c.site)
	}

	for _, f := range findings {
		report.Findings = append(report.Findings, *f)
	}
	rank(report.Findings)
	rank(report.Warnings)
	return report
}

// cascadeSite is one "Unresolvable identity" diagnostic parsed into the
// child instance whose resolution failed and the parent instance it
// blamed, both in [addrs.AbsResourceInstance.String] form - the shape
// [parseCascadeDetail] recovers and the fixpoint loop in [Analyze]
// consumes.
type cascadeSite struct {
	site   Site
	child  string
	parent string

	// safe is [dependentSafeToReclassify]'s verdict on the child, computed
	// once at construction (the child string, and therefore its type,
	// never changes across the fixpoint's rounds). See #221.
	safe bool
}

// cascadeMiddle and cascadeSuffix bracket the one place
// internal/live/identity/resolve.go's parentPart raises this diagnostic
// (resolve.go's own errorf call, "Unresolvable identity"): the format
// string is "%s depends on the identity of %s, which could not be resolved
// (see the other error)." with the child's [configs.StaticIdentifier.Subject]
// on the left and the parent's [addrs.AbsResourceInstance.String] on the
// right. Both are fixed literals from that one call site, not derived from
// any resource type.
const (
	cascadeMiddle = " depends on the identity of "
	cascadeSuffix = ", which could not be resolved (see the other error)."
)

// parseCascadeDetail recovers the child and parent addresses from an
// "Unresolvable identity" diagnostic's detail text. ok is false for any
// summary other than that one, and for any detail that does not match the
// fixed format - including a future wording change in resolve.go, which
// this package cannot see coming. Either way, the caller's default path
// (a hard language refusal, unchanged) is what fires, never a silent pass.
func parseCascadeDetail(summary, detail string) (child, parent string, ok bool) {
	if summary != "Unresolvable identity" {
		return "", "", false
	}
	mid := strings.Index(detail, cascadeMiddle)
	if mid < 0 || !strings.HasSuffix(detail, cascadeSuffix) {
		return "", "", false
	}
	child = detail[:mid]
	parent = detail[mid+len(cascadeMiddle) : len(detail)-len(cascadeSuffix)]
	if child == "" || parent == "" {
		return "", "", false
	}
	return child, parent, true
}

// neededByResourceAddr recovers the resource-instance address (in
// [addrs.AbsResourceInstance.String] form, matching a cascade diagnostic's
// parent text exactly) that a [configs.RefusedReference.NeededBy] string
// names.
//
// NeededBy is [configs.StaticIdentifier.String]: "<module>:<subject>" when
// the reference is inside a module, "<subject>" at the root - and Subject
// itself, built by identity's own resolver.identifier, is already the full
// dotted resource-instance address plus ".<attribute>" (an
// [addrs.AbsResourceInstance.String] embeds its module path already, so
// the module prefix StaticIdentifier.String adds is a second, redundant
// one). Taking everything after the first colon - present or not - and
// then dropping the final "."-delimited segment (the attribute name, which
// is a bare HCL identifier and so never itself contains a ".") recovers
// exactly the address [cascadeSite.parent] carries, in both the root and
// nested-module cases; TestNeededByResourceAddr pins all three shapes
// (root, nested-module, for_each) against what identity actually emits.
func neededByResourceAddr(neededBy string) string {
	if idx := strings.Index(neededBy, ":"); idx >= 0 {
		neededBy = neededBy[idx+1:]
	}
	return stripAttrSuffix(neededBy)
}

// stripAttrSuffix drops the final "."-delimited segment of a
// [configs.StaticIdentifier.Subject]-shaped string - "<resource-instance
// address>.<attribute>" - leaving the bare resource-instance address. Safe
// because an HCL attribute name is a single identifier and never itself
// contains a ".", regardless of what the address portion contains (a
// for_each key such as ["a.b"] included - the LAST "." in the whole string
// is always the one just before the attribute name). Returns "" when there
// is no "." to split on at all, which is not a shape either NeededBy or a
// cascade's child ever produces, but which the caller must not treat as a
// valid address.
func stripAttrSuffix(subject string) string {
	idx := strings.LastIndex(subject, ".")
	if idx < 0 {
		return ""
	}
	return subject[:idx]
}

// childType recovers a cascade dependent's resource type from its bare
// resource-instance address (child with the ".<attribute>" suffix already
// stripped by [stripAttrSuffix]) - exactly the string
// [addrs.AbsResourceInstance.String] produces, since
// internal/live/identity/resolve.go's resolver.identifier builds
// [configs.StaticIdentifier.Subject] as addr.String()+"."+attrName. ok is
// false when the address does not parse as one, which the caller must treat
// as "type unknown" rather than guess at.
func childType(addr string) (string, bool) {
	parsed, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		return "", false
	}
	return parsed.Resource.Resource.Type, true
}

// dependentSafeToReclassify is GitHub issue #221's conservative interim
// fix: whether a cascade's dependent instance (addr, already stripped of
// its trailing attribute) is provably safe to reclassify as
// data-read-eligible.
//
// resolveInstance (internal/live/identity/resolve.go:648-714) walks
// entry.Components in order and returns at the FIRST failing one - a
// short-circuit that predates this fix. When the failing component traces
// back to an eligible data read, that says nothing about any OTHER real
// component of the same type: it was never reached, let alone evaluated,
// so a second, independently broken required argument produces no
// diagnostic anywhere. Reclassifying such a cascade as "no configuration
// edit is needed" would be exactly that false assurance.
//
// entry is looked up the same two-step way
// internal/live/identity/resolve.go's resolver.lookupType does - the hand
// table first, [identity.SynthesizeTypeIdentity]'s schema fallback second -
// because a type this instrument only knows about through the fallback
// (e.g. an aws_s3_bucket_acl-shaped type whose identity is one required
// attribute the schema names directly) is exactly as real a dependent as
// one the hand table covers, and synthesis is not always single-component:
// an [identity.TypeIdentity.IdentityObjectOnly] entry can carry two or more
// attribute-bearing components joined by no separator at all
// (synthesize.go's names loop). Skipping the fallback here would call such
// a type "unknown" and refuse to reclassify it unconditionally - safe, but
// a needless loss for the single-component majority the fallback actually
// produces.
//
// A "real component" is derived from [identity.TypeIdentity.Components],
// never from a resource type name: a [identity.Component] whose Attrs is
// non-empty is the one shape resolveInstance actually reads out of the
// resource body inside the per-component loop and can fail on
// independently (identity/resolve.go:663-713). A Literal-only component
// (Attrs empty, Cloud unset) is a fixed separator between two real ones and
// can never fail. A Cloud-sourced component (aws_*'s account ID, region) is
// checked once for the whole entry by missingCloudValue before the
// per-component loop even starts (identity/resolve.go:616-622) - never
// hidden by a short-circuit inside a loop it precedes.
//
// This is safe with at most one real component: either it is the one
// already diagnosed by the cascade (nothing else to hide), or the type
// carries no configuration-driven component at all (nothing the resolver
// could have failed on independently, e.g. its whole identity comes from a
// literal, from the cloud context, or the type resolves at a shallower
// check before the components loop runs at all - ServerAssigned or
// RecordBacked). Two or more real components are unsafe, because the
// second one was never reached.
//
// Anything this cannot prove single-real-component - the address failing
// to parse, or the type settled by neither the hand table nor the schema
// fallback (no schemas offered, the provider does not serve the type, or
// its identity schema does not clear [identity.DerivableWith]'s bar) -
// refuses to reclassify. An unproven case must default to false: "no
// configuration edit is needed" is the one claim this whole mechanism
// cannot afford to make on a guess.
func dependentSafeToReclassify(addr string, schemas map[string]providers.Schema, signal *identity.ConfigSignal) bool {
	typeName, ok := childType(addr)
	if !ok {
		return false
	}
	entry, ok := identity.LookupType(typeName)
	if !ok {
		entry, ok = identity.SynthesizeTypeIdentity(typeName, schemas, signal)
	}
	if !ok {
		return false
	}
	real := 0
	for _, comp := range entry.Components {
		if len(comp.Attrs) > 0 {
			real++
		}
	}
	return real <= 1
}

// classifyDataSite maps one identity-layer refusal to the data-read pass's
// verdict on the data source it names, when it names a same-stack one that
// the analysis classified. ok is false for everything else - non-data
// refusals, cross-stack references, and references to data sources the
// module does not declare - and the caller keeps the diagnostic exactly as
// identity raised it.
func classifyDataSite(diag tfdiags.Diagnostic, analysis func() *dataread.Analysis) (Layer, string, string, bool) {
	ref := tfdiags.ExtraInfo[configs.RefusedReference](diag)
	// Same-stack data sources and both cross-stack flavors - tfe_outputs
	// (#179 stage 2) and terraform_remote_state (#179 stage 3) - all go
	// through the data-read pass's own classification now.
	switch ref.Category {
	case configs.CategoryDataSource, configs.CategoryTfeOutputs, configs.CategoryRemoteState:
	default:
		return "", "", "", false
	}
	res, ok := dataread.DataSubject(ref.Subject)
	if !ok {
		return "", "", "", false
	}
	src, found := analysis().SourceFor(ref.Module, res)
	if !found {
		return "", "", "", false
	}
	if src.Eligible {
		detail := fmt.Sprintf(
			"%s reads %s, which a live-plan resolves by reading the data source from the provider before identity resolution. No configuration edit is needed; the read itself was not performed by this check and can still fail at plan time.",
			ref.NeededBy, res.String())
		return LayerDataread, dataread.SummaryEligibleRead, detail, true
	}
	return LayerDataread, src.ReasonSummary, src.ReasonDetail, true
}

// Dir loads one directory and analyzes it: the entry point both front ends
// call, and the reason they cannot drift.
//
// A directory that will not load comes back as a report with no findings and
// a [Report.Load] carrying the diagnostics, which callers must check before
// reading "no findings" as "nothing refused it". [Report.Readable] is that
// check.
//
// varFiles is passed straight through to [Load]; see its doc comment.
func Dir(ctx context.Context, dir string, actx Context, varFiles ...string) Report {
	load := Load(ctx, dir, varFiles...)
	report := Analyze(ctx, load.Config, actx)
	report.Load = load
	return report
}

// Report is one configuration's verdict.
type Report struct {
	// Findings are the refusals that fired, ranked by how many sites each
	// blocks. Empty means every checked pass accepted the configuration.
	Findings []Finding

	// Warnings are the non-fatal diagnostics, ranked the same way. They do
	// not affect [Report.Blocked].
	Warnings []Finding

	// Instances is how many managed resource instances resolved.
	Instances int

	// Shadowed is how many identity refusals fell on a construct lint had
	// already refused, and were therefore not counted as findings. It is
	// reported rather than dropped silently so that the dedupe rule can be
	// checked against a run instead of taken on trust.
	Shadowed int

	// Schemas records whether provider schemas were available. See
	// [Context.Schemas] for why a report is worth less without them.
	Schemas bool

	// Load carries what loading the directory cost, when the caller used
	// [Load]. Unresolved modules and unset variables both bound what the
	// findings below can be trusted to cover.
	Load LoadResult

	// Checked and Unchecked are the live-path stages this analysis did and
	// did not run. Both are reported, always: a verdict that named only
	// what passed would read as a promise about stages nobody looked at.
	Checked   []Layer
	Unchecked []Layer
}

// Readable reports whether the configuration could be loaded at all.
//
// It matters because an unreadable directory also has no findings, and the
// two must never render the same way: "nothing refused this" and "nothing
// could be read" are opposite answers to the question a user asked.
func (r Report) Readable() bool { return r.Load.Config != nil }

// Blocked reports whether this configuration can move under live markers at
// all, as far as the checked passes can tell.
//
// The rule is not this package's opinion: it is what LivePlanCommand already
// does with the same two results. Any error-severity lint issue is fatal
// there (via [lint.HasErrors], GitHub issue #210 - a warning-severity one,
// such as [lint.RuleStateBackend], is rendered and does not stop the run,
// and lands in [Report.Warnings] rather than here), and any identity error
// diagnostic is fatal there, because a partial identity map makes the plan
// propose creating objects that already exist.
func (r Report) Blocked() bool { return len(r.Findings) > 0 }

// Sites is the total number of refused sites across every finding.
func (r Report) Sites() int {
	var n int
	for _, f := range r.Findings {
		n += len(f.Sites)
	}
	return n
}

// Finding is one refusal and every place it fired in this configuration.
type Finding struct {
	Refusal

	// Sites are where it fired, ordered by file and line.
	Sites []Site

	// Registered is false when the refusal's identity was not in
	// [Catalog]. It means this package and identity's registry have
	// drifted, and the finding is reported under its raw summary rather
	// than dropped.
	Registered bool

	// UnsetVarRefs are the required input variables with no value that this
	// refusal's sites reference, sorted, and UnsetVarSites how many of its
	// sites reference one. Both are set by
	// [Report.AttributeUnsetVariables] and are zero until it runs.
	//
	// UnsetVarSites < len(Sites) is the interesting case and the reason
	// this is a count rather than a flag: a refusal that fires in six
	// places, two of which read an unset variable, is still a real refusal
	// in four.
	UnsetVarRefs  []string
	UnsetVarSites int
}

// Site is one place a refusal fired.
type Site struct {
	// Address is the offending construct in address form, where the rule
	// has one.
	Address string

	// Type is the managed resource type, set only for the type-shaped
	// rules. See [lint.Issue.Type] and [Finding.Types].
	Type string

	// Module is the module path the site is in; empty for the root.
	Module string

	// Detail is the per-site explanation, which is also the remedy text
	// #101's audit put into these messages.
	Detail string

	// Category classifies the reference-subject shape behind this site -
	// [configs.ReferenceCategory], read off the raising diagnostic's Extra
	// field rather than parsed from Detail. Set only for
	// [configs.StaticValidateReferences]'s refusals; every other rule
	// leaves it empty rather than guessing. See #178.
	Category configs.ReferenceCategory

	// File, Line and Column locate it.
	File   string
	Line   int
	Column int

	// StartByte and EndByte bound the offending construct in File, kept so
	// that its source text can be recovered after the fact. Line and Column
	// locate it for a reader; these locate it for a program.
	StartByte int
	EndByte   int

	// UnsetVarRefs are the required input variables with no value that this
	// site's own source text references, sorted. Non-empty means this
	// refusal may be an artifact of the missing value rather than of the
	// configuration - see [Report.AttributeUnsetVariables].
	UnsetVarRefs []string
}

// location renders the site as "file:line", the form an editor and a reader
// both follow.
func (s Site) location() string {
	if s.File == "" {
		return ""
	}
	if s.Line == 0 {
		return s.File
	}
	return fmt.Sprintf("%s:%d", s.File, s.Line)
}

// Location is [Site.location] for callers outside this package.
func (s Site) Location() string { return s.location() }

// Types summarizes a type-shaped finding as a count per resource type,
// sorted by count and then name.
//
// It returns nothing for every other rule. This exists because the two
// type-shaped rules are the ones that produce hundreds of near-identical
// sites in a real configuration, and #114 asks for them summarized rather
// than enumerated - the resolution layer's unadmitted-type refusal once
// interpolated the whole admitted list into a single 25KB error, and a
// report that listed every site would be that mistake with more steps.
func (f Finding) Types() []TypeCount {
	counts := make(map[string]int)
	for _, site := range f.Sites {
		if site.Type == "" {
			continue
		}
		counts[site.Type]++
	}
	if len(counts) == 0 {
		return nil
	}

	out := make([]TypeCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, TypeCount{Type: name, Sites: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sites != out[j].Sites {
			return out[i].Sites > out[j].Sites
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// TypeCount is one resource type's share of a type-shaped finding.
type TypeCount struct {
	Type  string `json:"type"`
	Sites int    `json:"sites"`
}

// Remedy is what to do about this refusal, which is the first site's detail.
//
// The refusal registry's What is preferred where it has one, since it is
// written once per rule rather than once per site. Neither is authored
// here: #101 audited every one of these messages into saying what is
// actually true, and restating them in a report would be a second place for
// them to go stale.
func (f Finding) Remedy() string {
	if f.What != "" {
		return f.What
	}
	if len(f.Sites) > 0 {
		return f.Sites[0].Detail
	}
	return ""
}

type findingKey struct {
	layer Layer
	id    string
}

type findingMap map[findingKey]*Finding

func (m findingMap) get(layer Layer, id string) *Finding {
	key := findingKey{layer: layer, id: id}
	if f, ok := m[key]; ok {
		return f
	}
	refusal, registered := lookup(layer, id)
	f := &Finding{Refusal: refusal, Registered: registered}
	m[key] = f
	return f
}

func (f *Finding) add(site Site) {
	f.Sites = append(f.Sites, site)
}

// addWarning records one non-fatal site under its layer and ID, the same
// (layer, id) identity [findingMap.get] keys Findings by - a lint rule's ID
// is its [lint.Rule] string, an identity or pass-through diagnostic's is its
// Summary, and neither ever collides with the other's layer.
func (r *Report) addWarning(layer Layer, id string, site Site) {
	for i := range r.Warnings {
		if r.Warnings[i].Layer == layer && r.Warnings[i].ID == id {
			r.Warnings[i].Sites = append(r.Warnings[i].Sites, site)
			return
		}
	}
	refusal, registered := lookup(layer, id)
	r.Warnings = append(r.Warnings, Finding{
		Refusal:    refusal,
		Sites:      []Site{site},
		Registered: registered,
	})
}

// rank orders findings the way both front ends present them: most sites
// first, then by identity so that two runs over the same configuration
// produce the same order.
//
// Ranking by site count rather than by rule order is #114's second
// requirement, and it is the same choice #102 makes for the corpus with a
// different denominator. Sites within a finding are ordered by position so
// the first few printed are the first few in the file.
func rank(findings []Finding) {
	for i := range findings {
		sites := findings[i].Sites
		sort.SliceStable(sites, func(a, b int) bool {
			if sites[a].File != sites[b].File {
				return sites[a].File < sites[b].File
			}
			return sites[a].Line < sites[b].Line
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if len(findings[i].Sites) != len(findings[j].Sites) {
			return len(findings[i].Sites) > len(findings[j].Sites)
		}
		if findings[i].Layer != findings[j].Layer {
			return findings[i].Layer < findings[j].Layer
		}
		return findings[i].ID < findings[j].ID
	})
}
