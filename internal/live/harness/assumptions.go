// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package harness

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// Assumption is a load-bearing claim the project relies on while it
// measures anything.
//
// The field that matters is [Assumption.Consequence]. "This is true" is
// worth very little on its own: the four defects found by adversarial
// reading on 2026-08-16 were all green, and what made each of them
// expensive was that nobody had written down what depended on the claim.
// An assumption whose consequence is "nothing" does not need a check.
type Assumption struct {
	// ID is stable and is what the rendered document and failure messages
	// agree on.
	ID string

	// Claim is the assumption in one sentence, stated so it can be false.
	Claim string

	// Consequence is what it would mean if the claim stopped holding: what
	// number becomes wrong, what conclusion has to be withdrawn, who needs
	// telling.
	Consequence string

	// Evidence names where the claim came from, so a reader can tell a
	// derived fact from a maintainer ruling.
	Evidence string

	// Tracker is the issue, or the reason there is none.
	Tracker string

	// Recorded is the authored set the claim is about - the four
	// non-blocking refusal IDs, the four sanctioned exclusions - so the
	// rendered document names them rather than saying "four". Authored,
	// not measured: the check holds the code to it, so it does not move
	// when a measurement does.
	Recorded []string

	// Check proves the claim. It returns a one-line detail describing what
	// it found, which the failure message and nothing else quotes: the
	// rendered document must not carry a measured number, or it churns on
	// every regeneration for reasons unrelated to the claim.
	Check func(*Repo) (detail string, err error)
}

// Assumptions is every load-bearing claim with a check behind it, ordered
// by ID.
//
// Seeded on 2026-08-16 from claims that were prose only and are known to
// have been wrong or to have been re-derived by hand more than once.
func Assumptions() []Assumption {
	return []Assumption{
		checkedLayersAreFour(),
		credentialExclusionsAreFour(),
		artifactsAreCommitDated(),
		onboardingNonBlockingIDs(),
	}
}

// nonBlockingRefusalIDs is the recorded set: which refusal IDs
// [check.ClassifyOnboarding] does not treat as language-blocked, and which
// rung each of them lands an estate on.
//
// This is authored. The check below does not read it as truth - it drives
// ClassifyOnboarding over every refusal the live path can produce and
// compares the answer to this map, so a fourth non-blocking ID appearing in
// the classifier fails here rather than silently moving the ladder.
var nonBlockingRefusalIDs = map[string]check.OnboardingClass{
	string(lint.RuleStateBackend):    check.OnboardingBackendOnly,
	string(lint.RuleUnadmittedType):  check.OnboardingAdmissionsOnly,
	string(lint.RuleLogicalResource): check.OnboardingAdmissionsOnly,
	dataread.SummaryEligibleRead:     check.OnboardingDataReadEligible,
}

func onboardingNonBlockingIDs() Assumption {
	return Assumption{
		ID: "onboarding-non-blocking-ids",
		Claim: "check.ClassifyOnboarding treats exactly four refusal IDs as something other than " +
			"language-blocked - the state backend, an unadmitted type, a logical resource and an " +
			"eligible pre-plan data read - and every other refusal the live path can produce puts an " +
			"estate on the language-blocked rung.",
		Consequence: "Every ranking of which configurations are close to working under live markers is " +
			"computed from this classification. A fifth non-blocking ID moves estates up the ladder " +
			"with no configuration becoming any more applyable, which is exactly what the markerless " +
			"retraction had to avoid; a missing one buries work that is nearly done. Three agents " +
			"re-implemented this classifier in Python on separate days to check it, which is the cost " +
			"of it living in a switch statement nothing asserts.",
		Evidence: "internal/live/check/ladder.go's own switch, driven rather than read.",
		Tracker:  "#179 for the data-read rung; #102 for the ladder.",
		Recorded: recordedNonBlocking(),
		Check: func(*Repo) (string, error) {
			got := map[string]check.OnboardingClass{}
			for _, r := range check.AllRefusals() {
				cls := check.ClassifyOnboarding(true, []string{r.ID})
				if cls == check.OnboardingLanguageBlocked {
					continue
				}
				if prev, seen := got[r.ID]; seen && prev != cls {
					return "", fmt.Errorf("refusal ID %q classifies as both %q and %q; two layers share an ID",
						r.ID, prev, cls)
				}
				got[r.ID] = cls
			}
			var wrong []string
			for id, cls := range got {
				want, recorded := nonBlockingRefusalIDs[id]
				switch {
				case !recorded:
					wrong = append(wrong, fmt.Sprintf("%s is non-blocking (%s) and is not in the recorded set", id, cls))
				case want != cls:
					wrong = append(wrong, fmt.Sprintf("%s lands on %s, recorded as %s", id, cls, want))
				}
			}
			for id, want := range nonBlockingRefusalIDs {
				if _, ok := got[id]; !ok {
					wrong = append(wrong, fmt.Sprintf("%s is recorded as non-blocking (%s) but classifies as %s or is not in check.AllRefusals()",
						id, want, check.OnboardingLanguageBlocked))
				}
			}
			if len(wrong) > 0 {
				sort.Strings(wrong)
				return "", fmt.Errorf("the classifier and the recorded non-blocking set disagree on %d ID(s): %s",
					len(wrong), strings.Join(wrong, "; "))
			}
			return fmt.Sprintf("%d refusals driven through the classifier, %d non-blocking, all recorded",
				len(check.AllRefusals()), len(got)), nil
		},
	}
}

func checkedLayersAreFour() Assumption {
	return Assumption{
		ID: "checked-layers-are-lint-identity-dataread-stamp",
		Claim: "Everything an offline report says is derived from four fully checked analysis passes - lint, " +
			"identity, dataread and stamp - plus projection, which is checked only where it needs no cloud, " +
			"and discovery, which is not checked at all. All three lists are named rather than omitted.",
		Consequence: "A clean verdict is a narrow claim, and how narrow is exactly this list. A pass " +
			"added to internal/live and joined to neither list makes every clean count overstate, " +
			"silently, in the direction that looks like progress. This is the shape that has appeared " +
			"three times (#156, #164, #171): a check whose unit does not match the unit of the thing " +
			"it guards.",
		Evidence: "internal/live/check/catalog.go's CheckedLayers, PartiallyCheckedLayers and " +
			"UncheckedLayers, cross-checked against the committed corpus artifact's own header. " +
			"internal/live/check's TestLayersClassifyEveryLivePackage is what forbids a new package " +
			"joining no list; this holds the three lists themselves to their recorded contents. " +
			"Projection moved from unchecked to partial when #224's two exported provider-free entry " +
			"points finally got a caller; discovery is 4-of-25 checkable and wiring it is #261.",
		Tracker:  "#102",
		Recorded: []string{"checked: lint, identity, dataread, stamp", "partial: projection (2 of 27 refusals)", "unchecked: discovery"},
		Check: func(r *Repo) (string, error) {
			wantChecked := []string{"lint", "identity", "dataread", "stamp"}
			wantPartial := []string{"projection"}
			wantPartialRefusals := map[string]int{"projection": 2}
			wantUnchecked := []string{"discovery"}

			gotChecked := layerStrings(check.CheckedLayers())
			partial := check.PartiallyCheckedLayers()
			gotPartial := make([]string, 0, len(partial))
			for _, pl := range partial {
				gotPartial = append(gotPartial, string(pl.Layer))
			}
			gotUnchecked := layerStrings(check.UncheckedLayers())
			if !sameSlice(gotChecked, wantChecked) {
				return "", fmt.Errorf("check.CheckedLayers() is %v, recorded as %v", gotChecked, wantChecked)
			}
			if !sameSlice(gotPartial, wantPartial) {
				return "", fmt.Errorf("check.PartiallyCheckedLayers() is %v, recorded as %v", gotPartial, wantPartial)
			}
			// The share, not just the name. "Partly checked" is a claim about
			// how much, and a stage quietly going from 2 of 27 to 1 of 27
			// narrows every clean verdict without moving any list.
			for _, pl := range partial {
				if got, want := len(pl.Refusals), wantPartialRefusals[string(pl.Layer)]; got != want {
					return "", fmt.Errorf("%s checks %d of its %d refusals offline, recorded as %d; "+
						"the layer is still partly checked but by a different amount, so every clean verdict moved with it",
						pl.Layer, got, pl.Total, want)
				}
			}
			if !sameSlice(gotUnchecked, wantUnchecked) {
				return "", fmt.Errorf("check.UncheckedLayers() is %v, recorded as %v", gotUnchecked, wantUnchecked)
			}
			c, err := r.Corpus()
			if err != nil {
				return "", err
			}
			if !sameSlice(c.CheckedLayers, wantChecked) || !sameSlice(c.UncheckedLayers, wantUnchecked) {
				return "", fmt.Errorf(
					"%s says it checked %v and skipped %v, which is not what the code does now (%v / %v); "+
						"every number in that artifact was measured over a different set of passes than the "+
						"one a run would use today",
					CorpusJSON, c.CheckedLayers, c.UncheckedLayers, gotChecked, gotUnchecked)
			}
			return fmt.Sprintf("code and %s agree on %d checked, %d partially checked (%d of %d refusals) and %d unchecked layers",
				CorpusJSON, len(gotChecked), len(gotPartial),
				len(partial[0].Refusals), partial[0].Total, len(gotUnchecked)), nil
		},
	}
}

// sanctionedCredentialExclusions is CLAUDE.md's list, which the maintainer's
// 2026-08-15 parity ruling says does not grow: client-supplied or minted
// secret material that would persist in configuration or state.
var sanctionedCredentialExclusions = []string{
	"aws_appstream_directory_config",
	"aws_iam_access_key",
	"aws_iot_certificate",
	"aws_ivs_playback_key_pair",
}

// credentialReason recognises a veto reasoned on credential material.
//
// Matching free text is the weakest part of this check and it is worth
// saying so rather than hiding it: the ledger carries no structured field
// for the ruling, and the two entries that do cite it spell the phrase
// differently ("credential material" and "credential-material"). The first
// version of this check looked for the spaced spelling alone; falsifying the
// assumption to watch it fail is what showed it silently missed one of the
// two types it exists to be about. Hyphens are folded before the match for
// that reason. A ledger entry that avoids the word entirely is outside what
// this can see - the other leg, that all four sanctioned types are vetoed
// and none admitted, does not depend on text at all.
func credentialReason(reason string) bool {
	return strings.Contains(strings.ReplaceAll(strings.ToLower(reason), "-", " "), "credential material")
}

func credentialExclusionsAreFour() Assumption {
	return Assumption{
		ID: "credential-exclusions-are-exactly-four",
		Claim: "Exactly four provider types are excluded from admission on credential-material grounds, " +
			"they are all in the hand veto ledger, and none of them is admitted.",
		Consequence: "Type parity is the bar, and the credential exclusion is its one sanctioned hole. " +
			"A fifth type vetoed on credential grounds is admission debt wearing policy's clothes, and " +
			"it shrinks the parity denominator without anybody deciding to. This has already drifted " +
			"once in the other direction: aws_secretsmanager_secret_version sat on tools/survey-gen's " +
			"ops-excluded list reading \"credential\" until the 2026-08-16 ruling that the marker goes " +
			"into a tag and never into the secret.",
		Evidence: "CLAUDE.md's sanctioned list, checked against tools/row-gen/rejected.json's own " +
			"reason text and against internal/live/identity.DefaultTable. See credentialReason for " +
			"what the text half of this cannot see.",
		Tracker:  "the parity ruling; no issue - the list is a standing exclusion, not work.",
		Recorded: sanctionedCredentialExclusions,
		Check: func(r *Repo) (string, error) {
			rj, err := r.Rejected()
			if err != nil {
				return "", err
			}
			sanctioned := map[string]bool{}
			for _, t := range sanctionedCredentialExclusions {
				sanctioned[t] = true
			}
			var problems []string
			for _, t := range sanctionedCredentialExclusions {
				if _, ok := rj.Rejected[t]; !ok {
					problems = append(problems, t+" is not in "+RejectedJSON)
				}
				if _, ok := identity.DefaultTable[t]; ok {
					problems = append(problems, t+" is admitted by internal/live/identity.DefaultTable")
				}
			}
			// The other half: nothing outside the four may cite credential
			// material as its reason. A veto reasoned that way is either a
			// fifth exclusion, or a type whose real obstacle is something
			// else described in borrowed language.
			var extra []string
			for t, e := range rj.Rejected {
				if sanctioned[t] {
					continue
				}
				if credentialReason(e.Reason) {
					extra = append(extra, t)
				}
			}
			if len(extra) > 0 {
				sort.Strings(extra)
				problems = append(problems, fmt.Sprintf(
					"%d veto entr(y/ies) outside the sanctioned four cite credential material: %s",
					len(extra), strings.Join(extra, " ")))
			}
			if len(problems) > 0 {
				sort.Strings(problems)
				return "", fmt.Errorf("%s", strings.Join(problems, "; "))
			}
			return fmt.Sprintf("%d sanctioned exclusions, all vetoed, none admitted, and no fifth veto cites credential material",
				len(sanctionedCredentialExclusions)), nil
		},
	}
}

// datedArtifacts are the committed measurement artifacts whose numbers get
// quoted. Each must be traceable to a commit.
var datedArtifacts = []string{
	CorpusJSON,
	ConvergenceJSON,
	MappingJSON,
	SurveyFullJSON,
	"live/cohort-acceptance.json",
	"live/identity-sources.json",
}

func artifactsAreCommitDated() Assumption {
	return Assumption{
		ID: "measurement-artifacts-are-commit-dated",
		Claim: "Every committed measurement artifact whose numbers get quoted is tracked and reachable " +
			"from HEAD, so any figure taken from one can be dated to a commit.",
		Consequence: "A number that cannot be dated outlives the tree it describes. A site total " +
			"measured on a branch was propagated into three committed files and was wrong by exactly " +
			"the size of a class a later merge had emptied; the one copy that survived contact was the " +
			"one that named its commit. An artifact regenerated and quoted but not committed is the " +
			"same failure with nothing at all to point at.",
		Evidence: "git ls-tree over HEAD. None of these artifacts carries a commit field of its own, " +
			"so the tree is the only date available - which is itself worth knowing, and is why the " +
			"quoting rule is to name the commit rather than the file.",
		Tracker:  "no issue; this is the measuring-the-wall skill's first instruction made executable.",
		Recorded: datedArtifacts,
		Check: func(r *Repo) (string, error) {
			out, err := exec.Command("git", "-C", r.Root, "ls-tree", "-r", "--name-only", "HEAD").Output()
			if err != nil {
				// A checkout without git history cannot answer this. Say so
				// rather than pass: a check that silently succeeds when it
				// could not run is how a registry scanner reported
				// everything registered because it was blind.
				return "", fmt.Errorf("git ls-tree over HEAD failed (%v); this check cannot run in a "+
					"checkout without history, and it did not pass", err)
			}
			tracked := map[string]bool{}
			for _, line := range strings.Split(string(out), "\n") {
				tracked[strings.TrimSpace(line)] = true
			}
			var missing []string
			for _, a := range datedArtifacts {
				if !tracked[a] {
					missing = append(missing, a)
				}
			}
			if len(missing) > 0 {
				return "", fmt.Errorf("%d measurement artifact(s) are not in HEAD's tree: %s - a number "+
					"quoted from one of these names no commit anybody else can check out",
					len(missing), strings.Join(missing, " "))
			}
			return fmt.Sprintf("%d measurement artifacts, all reachable from HEAD", len(datedArtifacts)), nil
		},
	}
}

// ValidateAssumptions checks the assumptions registry's own shape.
func ValidateAssumptions(as []Assumption) []error {
	var errs []error
	seen := map[string]bool{}
	prev := ""
	for i, a := range as {
		where := fmt.Sprintf("assumption %d (%q)", i, a.ID)
		switch {
		case a.ID == "":
			errs = append(errs, fmt.Errorf("%s: no ID", where))
		case seen[a.ID]:
			errs = append(errs, fmt.Errorf("%s: duplicate ID", where))
		case a.ID < prev:
			errs = append(errs, fmt.Errorf("%s: out of order, %q precedes it", where, prev))
		}
		seen[a.ID] = true
		prev = a.ID

		if a.Claim == "" {
			errs = append(errs, fmt.Errorf("%s: no Claim", where))
		}
		if a.Consequence == "" {
			errs = append(errs, fmt.Errorf(
				"%s: no Consequence. \"This is true\" is worth much less than \"if this stops being true, "+
					"here is what becomes wrong and who needs telling\", and the consequence is the whole "+
					"reason this registry is not a list of tests", where))
		}
		if a.Evidence == "" {
			errs = append(errs, fmt.Errorf("%s: no Evidence", where))
		}
		if a.Tracker == "" {
			errs = append(errs, fmt.Errorf("%s: no Tracker, and no statement that there is none", where))
		}
		if a.Check == nil {
			errs = append(errs, fmt.Errorf("%s: no Check; an assumption nothing proves is the prose this registry replaces", where))
		}
	}
	return errs
}

// recordedNonBlocking renders the non-blocking map for the document, in a
// stable order.
func recordedNonBlocking() []string {
	out := make([]string, 0, len(nonBlockingRefusalIDs))
	for id, cls := range nonBlockingRefusalIDs {
		out = append(out, fmt.Sprintf("%s lands on %s", id, cls))
	}
	sort.Strings(out)
	return out
}

func layerStrings(ls []check.Layer) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, string(l))
	}
	return out
}

func sameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
