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
// exactly three places to put one (a tag, a record_store entry, a receipt),
// and every refusal here is a statement that none of them applies yet.
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
	"Not an identity attribute": {ActionAdmit,
		"the type HAS a row and the argument the config identifies it by is not the one the row names. A row correction, not an analysis fix."},
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
	"count-index": {ActionRule,
		"count.index reaching an identity is refused on purpose: the index is positional, so inserting an element renames every marker after it."},
	"child-module": {ActionRule,
		"a live-markers configuration block in a child module. Refused so an estate has one place that declares it."},
	"module-providers": {ActionRule,
		"a module call passing providers explicitly, which breaks the provider scope the marker path relies on."},
}

type sweep struct {
	Commit  string `json:"commit"`
	Entries []struct {
		Name     string         `json:"name"`
		Origin   string         `json:"origin"`
		Blocked  bool           `json:"blocked"`
		Refusals map[string]int `json:"refusals"`
		Sites    int            `json:"sites"`
		Modules  int            `json:"unresolved_modules"`
	} `json:"entries"`
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
		for _, bl := range e.Blockers {
			fmt.Printf("      [%-6s] %-3d  %s\n", bl.Action, bl.Sites, bl.ID)
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
}

type estate struct {
	Name     string
	Sites    int
	Modules  int
	Blockers []blocker // count toward the ordering
	Informs  []blocker // printed, but not blockers
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
			est.Blockers = append(est.Blockers, b)
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
	sole := map[string]int{}
	oneAway := 0
	for _, e := range plan {
		if len(e.Blockers) == 1 {
			oneAway++
			sole[e.Blockers[0].ID]++
		}
	}
	type kv struct {
		id string
		n  int
	}
	var rows []kv
	for id, n := range sole {
		rows = append(rows, kv{id, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].id < rows[j].id
	})

	var b strings.Builder
	fmt.Fprintf(&b, "%d estates are ONE blocker from clean:\n", oneAway)
	for _, r := range rows {
		a := blockerAction[r.id]
		fmt.Fprintf(&b, "  %3d  [%-6s] %s\n", r.n, a.Action, r.id)
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
