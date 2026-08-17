// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command estate-plan turns a refusal-probe sweep into an ordered work plan,
// one estate at a time.
//
// # Why this exists
//
// The campaign spent a long day assigning work by refusal CLASS - pick the
// class with the most sites, clear it across every estate that carries it.
// That moved 1570 sites and it moved the onboarding ladder by zero. It could
// not have done anything else: the median blocked estate carries about two
// blocking classes, so clearing one class across forty estates leaves forty
// estates still blocked.
//
// An estate onboards when its LAST blocker clears. So the unit of progress is
// an estate, and the unit of assignment has to be an estate too.
//
// # What it prints
//
//	go run ./tools/estate-plan -in sweep.json
//
// The blocked rate-capable deployments, fewest blocking classes first, each
// with its blockers and the action class each blocker implies. The first line
// is the estate to work next.
//
// # Population
//
// Only "published deployment" entries count. The corpus also holds in-repo
// fixtures and terraform-aws-modules examples, which read as ranking signal
// and not as a rate - onboarding a module example is not onboarding anybody's
// infrastructure. Ranking against the wrong denominator is the single most
// repeated measurement error in this project.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/lint"
)

// Action is what a blocker demands of whoever picks it up. The estate's own
// verdict is the worst action among its blockers, because an estate onboards
// only when every one of them clears.
type Action string

const (
	// ActionDerive: the value IS in the configuration and the analysis does
	// not reach it. Extending the analysis is the whole fix, and the estate
	// needs nothing from the cloud.
	ActionDerive Action = "DERIVE"

	// ActionAdmit: the resource type has no identity table row. The fix is in
	// the generators, not in the analysis.
	ActionAdmit Action = "ADMIT"

	// ActionDefer: the identity does not exist at plan time at all. No
	// analysis can produce it; something has to read it, record it, or wait
	// for it. This is the expensive column and the one carrying most estates.
	ActionDefer Action = "DEFER"

	// ActionRule: refused on purpose. The estate cannot onboard until a
	// maintainer changes the ruling, so it is not driveable work.
	ActionRule Action = "RULE"

	// ActionParity: stock OpenTofu refuses the same configuration for the
	// same reason. Not a defect, and not ours to fix.
	ActionParity Action = "PARITY"

	// ActionRead is not a blocker and does not count toward an estate's
	// blocker total.
	//
	// internal/live/dataread's SummaryEligibleRead says so in its own
	// declaration - "not a refusal: it is live-check's finding for a site the
	// phase will resolve at plan time with a read" - and ClassifyOnboarding
	// agrees, landing such an estate on the data-read-eligible rung rather
	// than language-blocked.
	//
	// The first version of this tool counted it, because Report.Blocked() is
	// len(Findings) > 0 and an informational finding is a finding. That
	// inflated the plan from 90 blocked estates to 118 and the one-away count
	// from 44 to 56, and it put a class that no fix removes at the top of the
	// board. An audit caught it the same day the tool was written.
	//
	// It is still printed, because the estate is not finished: the read has to
	// actually succeed against a cloud, and offline analysis cannot say
	// whether it will. It just does not order the queue.
	ActionRead Action = "READ"
)

// blockerAction maps each refusal that has ever blocked a published
// deployment to what it demands. Both directions are checked by
// TestEveryFiredRefusalHasAnAction: a refusal with no entry fails, and an
// entry naming a refusal the catalog does not define fails too.
//
// The Reason is the deliverable, not the row. It has to say WHERE the
// identity is, because that is what decides the action - the product has
// three places to CARRY one (a tag, a record_store entry, a receipt), and a
// refusal is usually a statement that none of them applies yet.
//
// Usually, not always, and the exception is the one this map got wrong twice.
// A fourth answer is that the identity needs no carrier, because it re-derives
// from the declaration on every run - which is what the client-named,
// parent-derived and account-derived survey paths mean, and what an
// association's identity always is. A marker is delete permission, not
// identity, so "nothing to tag" is not "nothing identifies it". See
// HANDOFF.md's "Two questions, not one".
//
// The ID alone also does not always settle the action. [refine] adjusts it
// from the refusal's causes, and its two rules are where the scope ruling and
// the pre-onboarding artifacts are applied.
var blockerAction = map[string]struct {
	Action Action
	Reason string
}{
	// ---- identity layer: the value is in the config, we cannot see it ----
	"Non-static count expression": {ActionDerive,
		"count is set from something the static evaluator will not fold. The instance keys are determined at plan time and the address is knowable; the analysis is what falls short."},
	"Non-static for_each expression": {ActionDerive,
		"same shape as count, one dimension wider: the key SET is the thing we cannot fold, and a wrong key set is a wrong marker rather than a missing one."},
	"Non-static identity argument": {ActionDerive,
		"the identity attribute itself is an expression we cannot fold. The value exists in the configuration."},
	"Unable to compute static value": {ActionDerive,
		"a general fold failure upstream of any identity question. Usually a function or traversal the static evaluator does not decompose."},
	"Dynamic value in static context": {ActionDerive,
		"a repetition symbol (each.key/each.value/count.index) reached a place the static scope does not bind it."},
	"Module output not supported in static context": {ActionDerive,
		"a module output feeds an identity. The output's own expression is in the configuration, so this is a reach problem."},
	"Unresolvable identity": {ActionDerive,
		"the catch-all for an identity the resolver gave up on. Needs bucketing before it can be assigned - it is not one shape."},
	"Identity not resolvable from configuration": {ActionDerive,
		"narrower sibling of the above, raised where the resolver knows which argument defeated it."},
	"Invalid operand": {ActionDerive,
		"an expression the evaluator rejected outright. Check parity first: if stock accepts it, this is a defect in our evaluator."},
	"Ambiguous list-valued identity argument": {ActionDerive,
		"the identity argument is a list and nothing says which element identifies the resource. May need a ruling rather than a derivation."},
	"Null identity argument": {ActionDerive,
		"the identity value is in the configuration, in a sibling of the same alternation component. firstPresent (resolve.go) picks an alternate by syntactic presence, so a body writing every one of cidr_blocks/ipv6_cidr_blocks/prefix_list_ids/source_security_group_id as try(..., null) gets a null one chosen while another holds the name the tag would carry. Selecting by value rather than by presence reaches it - the same shape as #190's <name>_prefix peek, one layer wider."},
	"moved-block": {ActionDerive,
		"the identity is the tofu-address tag already on the live resource, and a keyed rename vacates an instance address that tag carries, so the plan can rewrite it in place - which is what lint.go's own design comment says moved blocks do under markers. declaresSubject (moved.go) collapses an AbsResourceInstance to its resource before asking whether the from-address is still declared, so every this[\"old\"] -> this[\"new\"] migration terraform-aws-modules ships reads as un-vacated. Honourable's two genuinely deliberate clauses - a count-keyed module step, endpoints of different types - fire nowhere in the corpus."},

	// ---- admission: the type has no row ----
	"unadmitted-type": {ActionAdmit,
		"the resource type is not in the identity table. Either row-gen can reach it from the provider's own documentation and schema, or a ruling says it cannot - and non-AWS types are refused by ruling, not by gap."},
	"Resource type outside the live-markers subset": {ActionAdmit,
		"the identity layer's spelling of the same gap, raised after admission rather than at lint."},
	"Not an identity attribute": {ActionDerive,
		"an identity argument reads a computed attribute of a SIBLING - most often a client-named parent's arn - that is neither part of that sibling's identity nor a literal the sibling's own block wrote. The value is in the configuration's reach: the parent resolves concrete, and internal/live/projection materialises every concrete resolution before any derived one renders, so a promise to read the attribute later is renderable. It is an analysis fix in resolve.go's parentPart, not a row correction. DO NOT 'correct' the parent's row by adding the attribute to IdentityAttrs: for a non-server-assigned parent that reaches resolve.go's concrete shortcut and silently renders the parent's IMPORT ID in place of the attribute - aws_eks_access_entry would get cluster:release-assumed instead of cluster:arn:aws:iam::...:role/release-assumed, green."},
	"Identity argument not set": {ActionAdmit,
		"the row names an argument the configuration leaves unset. Frequently parity - stock also cannot plan without it - so check before assigning."},

	// ---- deferral: the identity does not exist yet ----
	"Resolves at plan time via a data-source read": {ActionRead,
		"the identity comes from a data source and the plan phase will read it. Explicitly not a refusal - it is what the data-read-eligible rung means - so it does not count as a blocker. It still has to succeed against a real cloud, which is step 6's job and not the corpus's."},
	"Data source not readable before resolution": {ActionDefer,
		"the data source itself depends on something unresolved, so even deferring the read does not help without an ordering."},
	"Data source provider not configurable": {ActionDefer,
		"the read cannot even be attempted offline because the provider needs configuration we do not have."},
	"markerless-type": {ActionDefer,
		"the type is server-assigned and untaggable, so there is nowhere to put a marker. Needs a record_store entry or a deferred read, not an analysis fix."},
	"logical-resource": {ActionDefer,
		"a resource with no cloud object behind it. Its identity is not in AWS at all, so the marker mechanism does not apply and it needs a forwarding address."},
	"Unmarked apply of a marker-only resource": {ActionDefer,
		"the resource would be applied before anything could mark it. An ordering problem, not a value problem."},
	"provisioner": {ActionDefer,
		"a provisioner is an effect that leaves nothing to read back, which is what receipts exist for."},

	// ---- deliberate ----
	"Two resources with the same identity": {ActionRule,
		"the identity is in the configuration twice: two declarations resolve to one cloud object, so one marker would displace the other. Refusing is the deliberate answer - a wrong marker outranks a missing one - and no analysis change is the fix, because the analysis is right. The configuration has to disambiguate. The one corpus instance is govuk-infrastructure/deployments/chat, whose bedrock_logging_dublin and bedrock_logging_london declare a per-region singleton twice without setting `provider`, so both land in the default region and the second overwrites the first on apply. Stock accepts that silently; this is one of the few places the marker model says something true stock does not."},
	"count-index": {ActionRule,
		"count.index reaching an identity is refused on purpose: the index is positional, so inserting an element renames every marker after it."},
	"child-module": {ActionRule,
		"a live-markers configuration block in a child module. Refused so an estate has one place that declares it."},
	"module-providers": {ActionRule,
		"a module call passing providers explicitly, which breaks the provider scope the marker path relies on."},
}

type sweep struct {
	Commit  string       `json:"commit"`
	Entries []sweepEntry `json:"entries"`
}

// sweepEntry is one measured configuration directory. It is a named type
// rather than an anonymous struct so a test can build one without restating
// every field and tag, which is what made adding a field here a three-file
// edit before.
type sweepEntry struct {
	Name     string         `json:"name"`
	Origin   string         `json:"origin"`
	Blocked  bool           `json:"blocked"`
	Refusals map[string]int `json:"refusals"`
	Sites    int            `json:"sites"`
	Modules  int            `json:"unresolved_modules"`

	// Causes is the probe's per-refusal breakdown, keyed by refusal ID
	// then by cause ("type:aws_s3_bucket", "reference:data_source").
	// [refine] reads it because a refusal ID alone does not say what
	// kind of work a blocker is.
	Causes map[string]map[string]int `json:"causes"`

	// UnsetVarSites is how many refused sites read a required variable
	// with no value. The probe's own summary hedges that these "may be
	// artifacts of the missing value", and the marking is textual
	// reachability rather than a causal claim, so this annotates an
	// estate and never reclassifies one.
	UnsetVarSites int `json:"unset_var_sites"`
}

// ratePopulation is the only origin that counts as progress. See the package
// doc: a module example onboarding is not somebody's infrastructure
// onboarding.
const ratePopulation = "published deployment"

func main() {
	in := flag.String("in", "", "refusal-probe -out JSON to plan from (required)")
	top := flag.Int("top", 15, "how many estates to print")
	all := flag.Bool("all", false, "print every blocked estate")
	flag.Parse()

	if *in == "" {
		fmt.Fprintln(os.Stderr, "estate-plan: -in is required\n\n  go run ./tools/refusal-probe -schemas -out sweep.json\n  go run ./tools/estate-plan -in sweep.json")
		os.Exit(2)
	}

	b, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "estate-plan: %s\n", err)
		os.Exit(1)
	}
	var s sweep
	if err := json.Unmarshal(b, &s); err != nil {
		fmt.Fprintf(os.Stderr, "estate-plan: %s\n", err)
		os.Exit(1)
	}

	plan, unknown := buildPlan(s)
	if len(unknown) > 0 {
		// Not fatal: a new refusal should not stop the plan being read. But
		// it is printed first, because an unmapped blocker is a blocker
		// nobody has decided how to act on.
		fmt.Printf("UNMAPPED REFUSALS (%d) - add them to blockerAction with a reason:\n", len(unknown))
		for _, id := range unknown {
			fmt.Printf("  %s\n", id)
		}
		fmt.Println()
	}

	n := *top
	if *all || n > len(plan) {
		n = len(plan)
	}

	fmt.Printf("estate plan at %s: %d blocked of %d rate-capable deployments\n\n",
		shortCommit(s.Commit), len(plan), rateTotal(s))
	fmt.Printf("%s\n\n", summarise(plan))

	for i, e := range plan[:n] {
		marker := "  "
		if i == 0 {
			marker = "->"
		}
		fmt.Printf("%s %d blocker(s), %d site(s)%s  %s\n", marker, len(e.Blockers), e.Sites, moduleNote(e.Modules), e.Name)
		if e.Note != "" {
			fmt.Printf("      NOTE: %s\n", e.Note)
		}
		for _, bl := range e.Blockers {
			fmt.Printf("      [%-6s] %-3d  %s\n", bl.Action, bl.Sites, bl.ID)
			if bl.Note != "" {
				fmt.Printf("             ^ %s\n", bl.Note)
			}
		}
		for _, bl := range e.Informs {
			fmt.Printf("      [%-6s] %-3d  %s (not a blocker; must still succeed at plan time)\n", bl.Action, bl.Sites, bl.ID)
		}
		fmt.Println()
	}
}

type blocker struct {
	ID     string
	Sites  int
	Action Action
	// Note is why this blocker's action is not the one blockerAction gives
	// its ID, or what an assignee has to know before taking it. Empty for
	// the common case where the ID settles it.
	Note string
}

// refine adjusts a blocker from its causes.
//
// The refusal ID says what the analyzer could not do. The cause says what
// about the configuration defeated it, and for two refusals that difference
// changes what kind of work the blocker is - or whether it is work at all.
// Classifying by ID alone put an estate no fix can clear at the top of this
// board twice: once for the data-read finding (see [ActionRead]) and once for
// the two below.
//
// Both rules key on a property of the cause, never on a list of type names. A
// list would buy the estates in front of it and leave the next cohort to be
// rediscovered, which is the standing bar's first rule.
func refine(id string, causes map[string]int, action Action) (Action, string) {
	types := causeTypes(causes)
	if len(types) == 0 {
		return action, ""
	}

	switch id {
	case "unadmitted-type":
		// A type outside the one provider this fork markers. The maintainer
		// ruling is #5 ("choudoufu is AWS only for now, and no second cloud
		// is on the roadmap"), reaffirmed 2026-08-16, so these estates are
		// out of scope rather than unbuilt - not driveable work, and they
		// should not sit in front of estates that are.
		//
		// Note what this does NOT claim. The admission path contains no
		// provider gate: internal/live/lint/admission.go decides on the
		// generated table, the markerless veto and a schema-only
		// derivation, and the schema fallback admits non-AWS types whose
		// provider publishes an identity schema. So this is a SCOPE ruling
		// about which estates are worth a slot, not a description of why
		// the code refused. The two were conflated in this map's own
		// wording before ("non-AWS types are refused by ruling, not by
		// gap"), which is false about the code.
		//
		// A logical type never reaches this refusal - internal/live/lint's
		// resource loop classifies logical types and `continue`s before the
		// admission check - so the effects vocabulary (null_resource,
		// terraform_data, time_*, random_*) cannot be caught by the prefix
		// test below.
		if foreign := nonAWS(types); len(foreign) > 0 && !anyAWS(types) {
			return ActionRule, "every unadmitted type here belongs to another provider (" +
				strings.Join(foreign, ", ") + "); AWS-only is a maintainer ruling (#5), so this estate is out of scope rather than unbuilt"
		}

	case "logical-resource":
		// An effect whose record is admitted the moment the estate declares
		// a record_store - internal/live/lint's resource loop returns early
		// for a ClassRecordAdmitted type when recordStoreConfigured.
		//
		// No corpus entry declares one, because every corpus entry still
		// carries the backend block it was published with, and a module may
		// declare a live block or a backend but not both. So this refusal
		// is the estate's PRE-ONBOARDING state rather than a language wall,
		// in exactly the way an unset required variable is. Verified on
		// .corpus/k8s-io/.../registry-sandbox-k8s-io-image-layers: swap the
		// backend for a live block with a record_store and the probe reads
		// blocked=0, sites=0, 13 instances.
		//
		// Secret-bearing logical types (random_password, local_sensitive_*)
		// are a different class and are genuinely refused, so the rule
		// holds only when every cause is record-admitted.
		if recordAdmittedAll(types) {
			return action, "every type here is an effect the record store admits; the estate clears this by declaring a record_store in its live block, which no corpus entry does because each still carries its published backend"
		}
	}

	return action, ""
}

// cascadeRefusals are the two refusals raised when an identity could not be
// built because something it depends on already failed. They carry no
// independent claim about the configuration: the dependency's own refusal is
// the finding, and this is its shadow.
//
// That matters for [unsetVarArtifact], because a cascade site does not itself
// mention the variable that defeated it. `bucket = aws_s3_bucket.x.id` reads
// no var at all; it fails because `bucket = "prefix-${var.env}-suffix"` on the
// resource above it did.
var cascadeRefusals = map[string]bool{
	"Unresolvable identity":                      true,
	"Identity not resolvable from configuration": true,
}

func cascadeSites(blockers []blocker) int {
	n := 0
	for _, bl := range blockers {
		if cascadeRefusals[bl.ID] {
			n += bl.Sites
		}
	}
	return n
}

// unsetVarArtifact reports whether every blocking site on an estate is an
// artifact of a required variable nobody supplied a value for - counting the
// sites that fail only because such a site failed first.
//
// The equality alone was the rule, and it under-reported. refusal-probe marks
// a site by whether its own source text reaches an unset variable, which is
// textual reachability; a cascade site is defeated by the variable without
// ever naming it. So an estate carrying one read as partially-blocked and
// stayed in the queue as analysis work that no analysis can do.
//
// Validated rather than reasoned. Every one of the 28 blocked rate-capable
// estates with any marked site was tested by supplying type-appropriate
// values for its required root variables and re-probing: 13 go to zero
// blockers, 15 keep real ones. This predicate flags exactly those 13, with no
// false positive and no miss. The 6 it adds over the equality are the cascade
// shape - app-elasticsearch6 (7 marked of 9), app-related-links (2 of 5),
// infra-assets (12 of 14), infra-athena-query-results (1 of 2),
// infra-database-backups-bucket (4 of 14) and
// infra-datagovuk-organogram-bucket (1 of 5).
//
// One trap that measurement had to avoid, recorded because it would silently
// fake a clear: giving a map-typed variable `{}` makes a for_each over it
// yield no instances at all, so the resource vanishes and sites fall to zero
// for a reason that has nothing to do with resolution. The values used gave
// every map one key, and instance counts ROSE or held on all 13 - none
// cleared by disappearing.
//
// This still annotates and never reclassifies. #183's ruling is that these
// estates stay language-blocked honestly, and they stay in the blocked count.
func unsetVarArtifact(unsetVarSites, blockingSites, cascade int) bool {
	if unsetVarSites <= 0 || blockingSites <= 0 || unsetVarSites > blockingSites {
		return false
	}
	// Every blocking site either names an unset variable itself, or is the
	// shadow of one that does. Cascade sites are only ever counted toward
	// the remainder, so an estate whose non-cascade blockers exceed the
	// marked sites is not an artifact.
	return blockingSites-unsetVarSites <= cascade
}

// causeTypes pulls the resource types out of a refusal's cause map.
// tools/refusal-probe writes them as "type:<name>"; other cause kinds
// (reference:, discovery:) are not type-shaped and are ignored.
func causeTypes(causes map[string]int) []string {
	var out []string
	for cause := range causes {
		if name, ok := strings.CutPrefix(cause, "type:"); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// nonAWS returns the distinct provider prefixes among types this fork does
// not marker. live/survey-full.json describes exactly one provider, and the
// prefix is that provider's, so a type whose prefix is not "aws" is served by
// something else.
func nonAWS(types []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range types {
		prefix, _, ok := strings.Cut(t, "_")
		if !ok || prefix == "aws" {
			continue
		}
		if !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix+"_*")
		}
	}
	sort.Strings(out)
	return out
}

// anyAWS reports whether any type is served by the one provider this fork
// markers. It is the guard on the scope ruling: an estate with even one
// unadmitted aws_ type still has in-scope admission work, so the ruling about
// the others must not take the whole blocker out of the queue.
func anyAWS(types []string) bool {
	for _, t := range types {
		if prefix, _, ok := strings.Cut(t, "_"); ok && prefix == "aws" {
			return true
		}
	}
	return false
}

// recordAdmittedAll reports whether every type is one the record store
// admits. It asks internal/live/lint rather than carrying a list, so the
// answer moves with the classification instead of drifting from it.
func recordAdmittedAll(types []string) bool {
	for _, t := range types {
		lt, isLogical := lint.ClassifyLogicalType(t)
		if !isLogical || lt.Class != lint.ClassRecordAdmitted {
			return false
		}
	}
	return len(types) > 0
}

type estate struct {
	Name    string
	Sites   int
	Modules int
	// Note is what an assignee has to know about this estate before taking
	// it, when the fact is a property of the estate rather than of any one
	// blocker. Empty for the common case.
	Note     string
	Blockers []blocker // count toward the ordering
	Informs  []blocker // printed, but not blockers
}

// driveable reports whether work in this repository can move this estate.
//
// Two shapes cannot be, and both used to sort to the front on blocker count
// alone:
//
//   - every blocker is a maintainer ruling, which HANDOFF.md's loop already
//     says to skip rather than re-prove;
//   - every blocking site reads a required variable nobody supplied, which
//     only the operator's own tfvars changes.
//
// Not driveable is not "not blocked". These estates stay in the plan, stay in
// the blocked count, and keep their reason printed. This decides queue
// position only.
func (e estate) driveable() bool {
	if e.Note != "" {
		return false
	}
	for _, bl := range e.Blockers {
		if bl.Action != ActionRule {
			return true
		}
	}
	return len(e.Blockers) == 0
}

// buildPlan orders blocked rate-capable estates by how many distinct classes
// block them, then by total sites, then by name so the order is stable across
// runs and two people planning from one sweep see the same first line.
func buildPlan(s sweep) ([]estate, []string) {
	var out []estate
	unknownSet := map[string]bool{}

	for _, e := range s.Entries {
		if e.Origin != ratePopulation || !e.Blocked {
			continue
		}
		est := estate{Name: e.Name, Sites: e.Sites, Modules: e.Modules}
		blockingSites := 0
		for id, sites := range e.Refusals {
			a, ok := blockerAction[id]
			if !ok {
				unknownSet[id] = true
			}
			act := a.Action
			if !ok {
				act = "?"
			}
			b := blocker{ID: id, Sites: sites, Action: act}
			if act == ActionRead {
				est.Informs = append(est.Informs, b)
				continue
			}
			b.Action, b.Note = refine(id, e.Causes[id], act)
			blockingSites += sites
			est.Blockers = append(est.Blockers, b)
		}

		// Every refused site on this estate reads a required variable nobody
		// supplied a value for. Stock OpenTofu refuses the same configuration
		// for the same reason, so no analysis change clears it; only the
		// operator's own tfvars does.
		//
		// The estate is deliberately NOT reclassified or dropped. The ruling
		// in live/corpus-manifest.json (#183, reworked to the parity ruling
		// on 2026-08-15) is that an estate whose repository ships no tfvars
		// "stays language-blocked on unset_var_only refusals, honestly", and
		// inferring parity from the marking would reverse that. It is
		// annotated so a slot is not spent extending an evaluator that
		// already folds the expression, which is what happened here once.
		//
		// The comparison is against BLOCKING sites, not e.Sites: the latter
		// counts the informational data-read finding too, and an estate
		// carrying one would never satisfy an equality against it however
		// complete the marking.
		if unsetVarArtifact(e.UnsetVarSites, blockingSites, cascadeSites(est.Blockers)) {
			est.Note = "every blocking site reads an unset required variable, or fails only because one that does failed first; stock refuses this identically, and #183 rules that an estate shipping no tfvars stays blocked rather than being papered over - not analysis work"
		}
		bySites := func(s []blocker) {
			sort.Slice(s, func(i, j int) bool {
				if s[i].Sites != s[j].Sites {
					return s[i].Sites > s[j].Sites
				}
				return s[i].ID < s[j].ID
			})
		}
		bySites(est.Blockers)
		bySites(est.Informs)
		// An estate with only informational findings is not blocked. It is
		// on the data-read-eligible rung and its remaining work is step 6.
		if len(est.Blockers) == 0 {
			continue
		}
		out = append(out, est)
	}

	sort.Slice(out, func(i, j int) bool {
		// Driveable estates first, and only then fewest blockers. An estate
		// nothing in this repository can change is not the next assignment
		// however few blockers it carries, and it headed this board for a
		// whole session precisely because blocker count alone put it there.
		//
		// They are still listed, still blocked, and still in the denominator.
		// Dropping them would overstate the rate, and for the unset-variable
		// cohort it would also reverse #183's ruling that such an estate
		// stays language-blocked rather than being papered over. The order
		// changes; the accounting does not.
		if di, dj := out[i].driveable(), out[j].driveable(); di != dj {
			return di
		}
		if len(out[i].Blockers) != len(out[j].Blockers) {
			return len(out[i].Blockers) < len(out[j].Blockers)
		}
		if out[i].Sites != out[j].Sites {
			return out[i].Sites < out[j].Sites
		}
		return out[i].Name < out[j].Name
	})

	unknown := make([]string, 0, len(unknownSet))
	for id := range unknownSet {
		unknown = append(unknown, id)
	}
	sort.Strings(unknown)
	return out, unknown
}

// summarise prints the two numbers that decide whether estate-first is worth
// running at all: how many estates are one blocker from clean, and which
// classes those single blockers are.
func summarise(plan []estate) string {
	// Keyed by the REFINED action and the refusal ID together, and counted
	// over driveable estates only. Both matter. Reading the action off
	// blockerAction here while the body below printed the refined one made
	// this block disagree with the list underneath it, and counting
	// non-driveable estates in "one blocker from clean" is the same error
	// the data-read finding caused: a number that reads as available work
	// when no work in this repository moves it.
	type key struct {
		action Action
		id     string
	}
	sole := map[key]int{}
	oneAway, parked := 0, 0
	for _, e := range plan {
		if len(e.Blockers) != 1 {
			continue
		}
		if !e.driveable() {
			parked++
			continue
		}
		oneAway++
		sole[key{e.Blockers[0].Action, e.Blockers[0].ID}]++
	}
	type kv struct {
		key key
		n   int
	}
	var rows []kv
	for k, n := range sole {
		rows = append(rows, kv{k, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].key.id < rows[j].key.id
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d driveable estates are ONE blocker from clean:\n", oneAway)
	for _, r := range rows {
		fmt.Fprintf(&b, "  %3d  [%-6s] %s\n", r.n, r.key.action, r.key.id)
	}
	if parked > 0 {
		fmt.Fprintf(&b, "  (%d more carry a single blocker that no work here moves - a maintainer "+
			"ruling, or a required variable only the operator supplies. They stay blocked and "+
			"counted; they sort to the back.)\n", parked)
	}
	return b.String()
}

func rateTotal(s sweep) int {
	n := 0
	for _, e := range s.Entries {
		if e.Origin == ratePopulation {
			n++
		}
	}
	return n
}

func moduleNote(n int) string {
	if n == 0 {
		return ""
	}
	// An entry with unresolved module calls is measuring a fraction of its
	// own refusal surface, so its position in this plan is a floor. Saying so
	// inline stops somebody picking a "1 blocker" estate that has five
	// uninstalled modules hiding the rest.
	return fmt.Sprintf(", %d UNRESOLVED MODULE(S) - this estate's blockers are a floor", n)
}

func shortCommit(c string) string {
	if len(c) > 10 {
		return c[:10]
	}
	if c == "" {
		return "(unknown commit)"
	}
	return c
}
