// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is issue #65's endgame stage: PROPOSE. row-gen's ordinary report
// (render.go) prints a pastable block for every proposal in the mapped set,
// admitted or not, and leaves every ratification decision to a human batch.
// -propose narrows that to the one slice small enough, and evidenced enough,
// that the human's job can shrink to a spot-check: a classification rule
// whose every historical instance - re-run today against
// internal/live/identity.DefaultTable - reproduced exactly what a human
// independently ratified, with zero exceptions, over a sample too large to
// be a lucky streak, AND whose candidate is not itself recorded in the
// rejection ledger. See proposeContractHeader below for the contract this
// stage states in its own output, and live/COVERAGE.md for the docs mirror.
//
// Two measurements, both over data already in the checkout:
//
//  1. ruleAdoption groups every admitted type's fresh convergence comparison
//     (buildConvergence's own compareOne, unmodified) by rule class - the
//     bucket plus the exact classify.go/importprecedence.go rule string that
//     produced it - and counts how many of that rule's historical instances
//     matched the ratified entry byte-for-byte. This reuses the exact
//     Matched semantics live/rowgen-convergence.json already commits to, so
//     PROPOSE's confidence claim is the same one that artifact's own ratchet
//     test polices, not a second, looser measure.
//  2. loadRejectedTypes is a second, independent, deliberately
//     over-inclusive safety net: a rule class can reach a spotless matched
//     record among the types that WERE admitted while still having a
//     rejected sibling under the same rule - a type a batch looked at by
//     name and declined, which is invisible to (1) because a rejected type
//     is never in DefaultTable and so never enters that comparison at all
//     (the Lambda batch's aws_lambda_alias and aws_lambda_layer_version_
//     permission are exactly this shape, though a later demotion pass moved
//     both out of the server-assigned bucket entirely - see
//     applyImportGrammarDemotions). Any type recorded in
//     tools/row-gen/rejected.json is excluded from candidacy outright,
//     regardless of what its rule class's numbers say.
//
// A rule class must clear both to produce a proposal, and even then only for
// types not already admitted.

// proposeMinSample is the smallest number of historical, already-ratified
// instances a rule class needs before its 100% match record is trusted
// enough to auto-propose from. Five is a deliberate floor, not a tuned
// constant: it is roughly an ordinary ratification batch's own smallest
// size (the Lambda pilot batch's own five ratified rows, DynamoDB's own
// small slice), large enough that a clean record is not one or two
// coincidences, small enough that a genuinely narrow but clean rule variant
// (a precedence-pass correction that only fires for one CFN service, say)
// can still qualify as its own history accumulates, rather than being
// permanently starved by a threshold sized for the big base rules. Lower it
// only with the same reviewed-reason discipline
// unannotatedMismatchRatchetMax's own doc comment asks for; raising it needs
// no ceremony, since a stricter bar only ever proposes less.
const proposeMinSample = 5

// ruleKey identifies one classification rule class: the bucket it produces
// plus the exact prose rule string that fired (classify.go's or
// importprecedence.go's own Rule assignments) - the same granularity
// convergenceRow.ProposedRule already records, so two proposals in the same
// bucket but reached by different evidence (the base registry-only rule vs.
// an import-grammar precedence correction) are counted as different rule
// classes with independent track records, never pooled together.
type ruleKey struct {
	Bucket bucket
	Rule   string
}

// ruleStats is one rule class's historical record: how many already-admitted
// types today's classifyAll would put under this rule (Compared), and how
// many of those exactly match what a human independently ratified
// (Matched) - the same Matched compareOne (convergence.go) computes.
type ruleStats struct {
	Compared int
	Matched  int
}

func (s ruleStats) pct() float64 {
	if s.Compared == 0 {
		return 0
	}
	return 100 * float64(s.Matched) / float64(s.Compared)
}

// qualifies is the auto-propose bar: every historical instance matched
// (Matched == Compared, so a single unannotated correction anywhere in this
// rule's history disqualifies it permanently until it is outnumbered by
// clean instances, which - by construction - can never happen once a
// mismatch is on the books, since Compared only grows), and there are enough
// instances that 100% is not two coin flips landing the same way.
func (s ruleStats) qualifies() bool {
	return s.Compared >= proposeMinSample && s.Matched == s.Compared
}

// pastableBucket reports whether b is one of the four buckets render.go
// ever prints a "--- paste into ... ---" block for (renderProposal's own
// switch). bucketFoldChild and bucketNeedsHandSeparator are proposable in
// row-gen's ordinary report sense - they carry real evidence - but never
// pastable: the separator or the child's own composite shape is a human
// call no registry evidence supplies, so no rule class built from either
// bucket could ever have a paste-ready output for PROPOSE to emit.
// bucketEvidenceOnly is, by definition, unconfident evidence.
func pastableBucket(b bucket) bool {
	switch b {
	case bucketServerAssigned, bucketClientNamed, bucketComposite, bucketAssembled:
		return true
	default:
		return false
	}
}

// ruleAdoption groups a fresh convergence comparison's rows (buildConvergence's
// own Types slice - one row per type internal/live/identity.DefaultTable
// already admits) by rule class, counting Compared/Matched per class.
// Restricted to the pastable buckets: a rule class built from
// bucketFoldChild or bucketNeedsHandSeparator could never produce a pastable
// candidate, so its Matched rate (which is always 0 - see convergence.go's
// proposedFields default case) would only ever read as a permanent, and
// misleading, disqualification rather than the "not applicable" it actually
// is.
func ruleAdoption(rows []convergenceRow) map[ruleKey]ruleStats {
	out := map[ruleKey]ruleStats{}
	for _, r := range rows {
		b := bucket(r.ProposedBucket)
		if !pastableBucket(b) {
			continue
		}
		k := ruleKey{Bucket: b, Rule: r.ProposedRule}
		s := out[k]
		s.Compared++
		if r.Matched {
			s.Matched++
		}
		out[k] = s
	}
	return out
}

// qualifyingRules filters a rule-adoption ledger down to the classes
// ruleStats.qualifies accepts.
func qualifyingRules(stats map[ruleKey]ruleStats) map[ruleKey]ruleStats {
	out := map[ruleKey]ruleStats{}
	for k, s := range stats {
		if s.qualifies() {
			out[k] = s
		}
	}
	return out
}

// sortedRuleKeys is the deterministic print order every rendering and test
// in this file uses: bucket, then rule text.
func sortedRuleKeys[V any](m map[ruleKey]V) []ruleKey {
	out := make([]ruleKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bucket != out[j].Bucket {
			return out[i].Bucket < out[j].Bucket
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// rejectedTypesJSONRel is the ledger of types a ratification batch considered
// and did not admit. It replaces a scan that grepped the identity and
// admission tables' own Go comment prose for the word "Rejected" and harvested
// every type token near it: those tables are generated in full now (issue
// #96), so there is no prose there to grep, and a generator taking a human's
// comment wording as an input was the coupling that made the tables
// hand-maintained in the first place. A rejection is a ruling, and a ruling
// belongs in a machine-readable ledger.
const rejectedTypesJSONRel = "tools/row-gen/rejected.json"

// rejectedTypesArtifact is rejected.json's shape. Only the key set is read;
// the per-type object carries provenance for a human tracing a ruling back
// through git history, not anything PROPOSE branches on.
type rejectedTypesArtifact struct {
	Note     string                     `json:"note"`
	Rejected map[string]rejectedTypeRow `json:"rejected"`
}

type rejectedTypeRow struct {
	RecoveredFrom []string `json:"recovered_from,omitempty"`

	// Reason carries a fresh, measured rejection's own ground - entries
	// recovered from deleted prose trace provenance via RecoveredFrom
	// instead. Either field alone is a valid row.
	Reason string `json:"reason,omitempty"`
}

// loadRejectedTypes is PROPOSE's second safety net: the veto set of types
// already ruled out by name. Never used as positive evidence, only ever to
// veto a candidate ruleAdoption's numbers alone would have allowed.
//
// A missing or empty ledger is an error rather than an empty veto set, the
// same discipline the old scan held itself to: silently vetoing nothing would
// let PROPOSE re-propose every type a batch had already rejected.
func loadRejectedTypes(root string) (map[string]bool, error) {
	path := filepath.Join(root, rejectedTypesJSONRel)
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, fmt.Errorf("reading %s for recorded rejections: %w", rejectedTypesJSONRel, err)
	}
	var art rejectedTypesArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", rejectedTypesJSONRel, err)
	}
	if len(art.Rejected) == 0 {
		return nil, fmt.Errorf("%s records no rejections; an empty veto set would let PROPOSE re-propose types a batch already ruled out", rejectedTypesJSONRel)
	}
	out := make(map[string]bool, len(art.Rejected))
	for tf := range art.Rejected {
		out[tf] = true
	}
	return out, nil
}

// proposeCandidate is one not-yet-admitted proposal PROPOSE selected: the
// proposal itself, the rule class it was selected under, and that class's
// track record, so the rendered block can cite its own evidence rather than
// asserting confidence as an adjective.
type proposeCandidate struct {
	Proposal proposal
	Rule     ruleKey
	Stats    ruleStats
}

// selectProposeCandidates is the full filter: pastable bucket, not already
// admitted, not named in a recorded rejection, and classified under a
// qualifying rule class. Sorted by TF type for a deterministic report.
func selectProposeCandidates(proposals []proposal, admitted, rejected, vetoed map[string]bool, qualifying map[ruleKey]ruleStats) []proposeCandidate {
	var out []proposeCandidate
	for _, p := range proposals {
		if !pastableBucket(p.Bucket) {
			continue
		}
		if admitted[p.TFType] {
			continue // nothing to propose - already ratified
		}
		if rejected[p.TFType] {
			continue // a batch already looked at this type by name; the rule's clean record elsewhere does not overrule a specific recorded decision
		}
		if vetoed[p.TFType] {
			continue // markerless.go's rule; see its file comment for why no classification confidence can overrule it
		}
		k := ruleKey{Bucket: p.Bucket, Rule: p.Rule}
		stats, ok := qualifying[k]
		if !ok {
			continue
		}
		out = append(out, proposeCandidate{Proposal: p, Rule: k, Stats: stats})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Proposal.TFType < out[j].Proposal.TFType })
	return out
}

// buildProposeReport is -propose's whole pipeline: load the mapped set,
// compare it against what is already admitted (the same classifyAll and
// buildConvergence every other mode already uses), scan for recorded
// rejections, select candidates, and render both the report (stdout) and the
// one-line summary (stderr) admission-pipeline's REPORT stage greps for -
// the same shape run()'s and runConvergence's own summary lines already
// take.
func buildProposeReport(root string) (report, summary string, err error) {
	proposals, err := loadProposals(root)
	if err != nil {
		return "", "", err
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", annotationsJSONRel, err)
	}
	art := buildConvergence(proposals, annotations)
	stats := ruleAdoption(art.Types)
	qualifying := qualifyingRules(stats)

	rejected, err := loadRejectedTypes(root)
	if err != nil {
		return "", "", err
	}

	admitted := map[string]bool{}
	for _, t := range identity.AdmittedTypes() {
		admitted[t] = true
	}

	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", surveyJSONRel, err)
	}
	vetoed := setOf(markerlessRoster(survey, proposals))

	candidates := selectProposeCandidates(proposals, admitted, rejected, vetoed, qualifying)

	report = renderProposeReport(stats, qualifying, candidates)
	summary = fmt.Sprintf(
		"row-gen -propose: %d rule class(es) considered (min sample %d), %d qualify at 100%% historical adoption, %d new logical type(s) proposed for auto-admission",
		len(stats), proposeMinSample, len(qualifying), len(candidates))
	return report, summary, nil
}

// proposeContractHeader is printed once, before anything else - the same
// stance render.go's reportHeader takes (the boundary is stated in the
// output itself, not only in a doc comment a reader of the report never
// sees). This is the spot-check contract the task that built this stage
// asked to be precise, not hand-wavy: what PROPOSE claims, what it does not,
// and exactly what a human approving the containing PR is being asked to
// verify. live/COVERAGE.md's "PROPOSE" section mirrors this in prose for
// readers who never run the tool.
var proposeContractHeader = `row-gen -propose: automatic high-confidence admission proposals (issue #65)

PROPOSE only ever emits a block for a classification rule class (a bucket -
server-assigned, client-named or composite - plus the exact rule that fired)
whose every historical instance, re-run against today's classifier,
reproduced byte-for-byte what a human independently ratified into
internal/live/identity.DefaultTable: a 100% match, over at least
` + fmt.Sprint(proposeMinSample) + ` instances, and with the candidate itself not recorded in
` + rejectedTypesJSONRel + `. Below that bar, the type prints in the
ordinary report (go run ./tools/row-gen) like any other proposal and waits
for an ordinary batch.

WHAT THIS DOES NOT CLAIM:

  - It does not claim live/registry.json's or live/import-grammar.json's
    evidence is correct FOR THIS SPECIFIC TYPE - only that the rule which
    turns that evidence into an entry has never once needed a human
    correction, for every other type it has been checked against so far.
    That is an inductive claim about the rule, not a proof about the type.
  - It does not run the type against floci or a live AWS account. No
    emulator or live proof exists for anything below; every cohort estate a
    batch has ever shipped still needed its own fixture and floci run, and
    this one is no different.
  - It does not evaluate SURVEY.md's credential-material exclusion (the rule
    aws_iam_access_key and aws_iot_certificate are excluded by): nothing
    here reads whether a type mints or exports a secret.
  - It does not re-verify live/mapping.json's own CFN mapping for the type -
    that join is mapping-gen's job, upstream of this stage entirely.

THE SPOT-CHECK CONTRACT - four things, per proposed type, nothing more:

  1. Open the provider's own documentation page for the type (the Import
     section, or the Identity Schema block where the pasted comment says
     the entry came from precedence evidence) and confirm the argument or
     attribute the pasted entry names is what that section documents. This
     is reading one section, not re-deriving the classification.
  2. Confirm the type does not mint or return credential material. If it
     does, do not paste it - record the rejection by name in
     ` + rejectedTypesJSONRel + `, with a "reason", so this stage
     never proposes it again. Do NOT write it into table.go or
     admission.go: those files are generated in full (issue #96) and the
     next -emit run would overwrite the ruling.
  3. Paste the two blocks exactly as printed, unedited, into
     admittedTypesV0 and DefaultTable. Editing the pasted text defeats the
     point of generating it: nothing about it should be hand-transcribed.
  4. Build the cohort estate, run the suites, and get a floci probe the way
     every prior ratification batch has. PROPOSE shortens the
     classification decision; it does not shorten the verification a batch
     always did after making one.

What is being trusted, stated plainly: that a classification rule with a
spotless record over its past N instances will also be right on instance
N+1. That is inductive, not deductive - it is exactly as strong as the
sample behind it, which is why every block below states its own rule
class's N, and why proposeMinSample floors what counts as a sample at all.
`

// spotCheckReminder is the short, per-candidate restatement of the contract
// above - the header states it once in full; this keeps it in view next to
// each block without forcing a reader back to the top every time.
const spotCheckReminder = "\nspot-check: read the provider's Import/Identity-Schema doc cited above, confirm no credential material, paste unedited, then build + test + floci as usual.\n"

// renderProposeReport is PROPOSE's whole printed output: the contract
// header, the rule-class ledger (every pastable bucket's rule and its
// Compared/Matched/pct/qualifies record, so a reader sees the near-misses
// too, not only the winners), and then either an explicit zero-candidates
// statement or each candidate's block - reusing renderProposal
// (render.go) unchanged, so the pasted text PROPOSE prints is byte-for-byte
// what the ordinary report would have printed for the same proposal.
func renderProposeReport(stats map[ruleKey]ruleStats, qualifying map[ruleKey]ruleStats, candidates []proposeCandidate) string {
	var b strings.Builder
	b.WriteString(proposeContractHeader)

	b.WriteString("\nrule-class ledger (every pastable bucket's classification rule and its track record against internal/live/identity.DefaultTable):\n\n")
	b.WriteString("bucket | rule | compared | matched | pct | qualifies\n")
	b.WriteString("--- | --- | --- | --- | --- | ---\n")
	for _, k := range sortedRuleKeys(stats) {
		s := stats[k]
		_, ok := qualifying[k]
		fmt.Fprintf(&b, "%s | %s | %d | %d | %.1f%% | %v\n", k.Bucket, k.Rule, s.Compared, s.Matched, s.pct(), ok)
	}

	b.WriteString("\n================================================================\n")
	if len(candidates) == 0 {
		b.WriteString("0 logical types proposed this run.\n")
		if len(qualifying) == 0 {
			fmt.Fprintf(&b, "No rule class currently clears the bar (>=%d historical instances, 100%% matched, no recorded rejection) - see the ledger above for the closest classes.\n", proposeMinSample)
		} else {
			b.WriteString("Every currently-mapped type under a qualifying rule class is already admitted or already recorded as rejected; nothing new to propose this run.\n")
		}
		return b.String()
	}

	fmt.Fprintf(&b, "%d logical type(s) proposed, one rule class's block each:\n", len(candidates))
	for _, c := range candidates {
		b.WriteString("\n----------------------------------------------------------------\n")
		fmt.Fprintf(&b, "rule class track record: %d/%d (100%%) admitted unchanged against internal/live/identity.DefaultTable; not recorded in "+rejectedTypesJSONRel+"\n", c.Stats.Matched, c.Stats.Compared)
		b.WriteString(renderProposal(c.Proposal))
		b.WriteString(spotCheckReminder)
	}
	return b.String()
}
