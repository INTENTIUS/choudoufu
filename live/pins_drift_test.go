// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/pins"
)

// TestMeasurementArtifactsShareTheProviderPin is issue #117's check: the two
// committed measurement artifacts must record the release
// pins.AWSProviderVersion names, so the instrument that ranks admission
// failures (the corpus) and the artifacts that define admission (the
// survey) can never again silently describe different providers. A pin
// bump fails here until both are regenerated:
//
//	go run ./tools/survey-gen        (and its downstream, see the tool's doc)
//	just corpus                      (after just corpus-fetch)
func TestMeasurementArtifactsShareTheProviderPin(t *testing.T) {
	var survey struct {
		ProviderVersion string `json:"provider_version"`
	}
	decodeInto(t, "survey-full.json", &survey)
	if survey.ProviderVersion != pins.AWSProviderVersion {
		t.Errorf("live/survey-full.json records provider %q; pins.AWSProviderVersion is %q - regenerate the survey or fix the pin",
			survey.ProviderVersion, pins.AWSProviderVersion)
	}

	// #211: the artifact now records one row per provider any corpus entry
	// actually needed, not a single global provider - so the pin check
	// looks for the one row marked "pinned" (see
	// tools/corpus-gen's schemaAcquirer and its package doc comment for why
	// exactly one provider, matched by -provider-source/-provider-version
	// rather than by name in any control flow, still keeps an exact pin)
	// instead of reading a single top-level version string.
	var corpus struct {
		Schemas struct {
			Providers []struct {
				Provider string `json:"provider"`
				Version  string `json:"version"`
				Pinned   bool   `json:"pinned"`
			} `json:"providers"`
		} `json:"schemas"`
	}
	decodeInto(t, "corpus-refusals.json", &corpus)
	var pinnedVersion string
	var found bool
	for _, p := range corpus.Schemas.Providers {
		if p.Pinned {
			pinnedVersion = p.Version
			found = true
			break
		}
	}
	if !found {
		t.Errorf("live/corpus-refusals.json records no pinned provider - rerun `just corpus`")
	} else if pinnedVersion != pins.AWSProviderVersion {
		t.Errorf("live/corpus-refusals.json records the pinned provider at %q; pins.AWSProviderVersion is %q - rerun `just corpus`",
			pinnedVersion, pins.AWSProviderVersion)
	}
}

// providerRow mirrors tools/corpus-gen's ProviderSchemaResult, decoding
// only the fields this test needs.
type providerRow struct {
	Provider   string `json:"provider"`
	Constraint string `json:"constraint"`
	Version    string `json:"version"`
	Available  bool   `json:"available"`
	Error      string `json:"error"`
	Locked     bool   `json:"locked"`
}

// providerPinRow mirrors tools/corpus-gen's providerPin.
type providerPinRow struct {
	Provider   string `json:"provider"`
	Constraint string `json:"constraint"`
	Available  bool   `json:"available"`
	Version    string `json:"version"`
	Error      string `json:"error"`
}

func pinRowKey(provider, constraint string) string {
	return provider + "@" + constraint
}

// TestEveryAcquiredProviderIsLocked is issue #222's check. #211 gave every
// corpus entry schemas for its own declared or implied providers instead of
// a single global hashicorp/aws map, which was the right fix at that layer,
// but left every provider except the one flag-pinned by
// -provider-source/-provider-version free to float to whatever "latest"
// (or a ranged constraint's matching release) resolved to on the day of the
// run: live/corpus-refusals.json could move with zero code change and zero
// test failure. TestMeasurementArtifactsShareTheProviderPin above only ever
// checked the one row marked Pinned - by construction exactly one provider.
//
// The external anchor here is live/corpus-provider-pins.json
// (tools/corpus-gen's providerPin/[loadProviderPins]): a checked-in file a
// regeneration run reads BEFORE resolving any unlocked requirement, so an
// available row's version, or an unavailable row's error text, are not
// being compared against a copy of themselves produced by the same run -
// they are compared against what a PRIOR, independently committed run
// recorded, and [schemaAcquirer.acquire] is what makes that comparison
// meaningful: it forces the exact locked version onto every already-seen
// (provider, constraint) pair, so agreement here means the lock actually
// took effect, not merely that nothing changed by chance.
//
// Every row in the artifact's schema block is checked, not only the flag
// pin: a provider absent from the pins file entirely fails loudly instead
// of being skipped (the shape of bug #222 itself - a completeness check
// that can only see what it already expects). A row with Available false
// (the corpus's 8 platform-incompatible providers) is checked too, against
// the pin's recorded error text rather than a version it never resolved,
// so "skip what has no version" cannot quietly recreate the same hole one
// field over.
func TestEveryAcquiredProviderIsLocked(t *testing.T) {
	var corpus struct {
		Schemas struct {
			Providers []providerRow `json:"providers"`
		} `json:"schemas"`
	}
	decodeInto(t, "corpus-refusals.json", &corpus)

	var pins map[string]providerPinRow
	decodeInto(t, "corpus-provider-pins.json", &pins)

	seen := make(map[string]bool, len(pins))

	for _, row := range corpus.Schemas.Providers {
		key := pinRowKey(row.Provider, row.Constraint)
		pin, ok := pins[key]
		if !ok {
			t.Errorf("corpus-refusals.json: %s (constraint %q) has no entry in live/corpus-provider-pins.json - rerun `just corpus` to lock it, then commit both files",
				row.Provider, row.Constraint)
			continue
		}
		seen[key] = true

		if pin.Available != row.Available {
			t.Errorf("%s (constraint %q): corpus-refusals.json records available=%v, corpus-provider-pins.json records available=%v - the lock did not hold; rerun `just corpus` and inspect the diff before committing",
				row.Provider, row.Constraint, row.Available, pin.Available)
			continue
		}

		if row.Available {
			if row.Version != pin.Version {
				t.Errorf("%s (constraint %q) drifted from %q (locked in corpus-provider-pins.json) to %q (corpus-refusals.json) - a provider version pin moved with no deliberate edit to the pins file",
					row.Provider, row.Constraint, pin.Version, row.Version)
			}
		} else {
			if row.Error != pin.Error {
				t.Errorf("%s (constraint %q): unavailable, but the recorded failure changed.\nlocked:\n%s\nnow:\n%s",
					row.Provider, row.Constraint, pin.Error, row.Error)
			}
		}
	}

	for key, pin := range pins {
		if !seen[key] {
			t.Errorf("corpus-provider-pins.json: %s (constraint %q) is locked but no row in corpus-refusals.json matches it - prune it, or the manifest entry that once needed it is gone",
				pin.Provider, pin.Constraint)
		}
	}
}

// gauntletCrossingScriptPattern matches e2e/<estate>/run.sh, the shape
// every gauntlet estate's crossing script lives at (tools/gauntlet/manifest.go's
// Estate.ScriptPath).
var gauntletCrossingScriptPattern = regexp.MustCompile(`^e2e/[^/]+/run\.sh$`)

// speaksGauntletProtocol, copiesACorpusModule and declaresHashicorpAWS are
// issue #1041's discovery criteria for "a crossing script that inits a
// corpus module": #1034 named five scripts by hand, and the next script
// someone wrote (corpus-alb-complete, named in #1041 itself) proved a hand
// -written list cannot be trusted to stay complete. Scanning every
// e2e/*/run.sh by what it actually contains, instead of a maintained list,
// is the guard that a NEW script cannot slip past by omission.
//
//   - speaksGauntletProtocol: calls gauntlet_begin, the one call every
//     script in the actual gauntlet (as opposed to a pre-protocol legacy
//     demo, per live/e2e/lib/gauntlet.sh's own doc comment) makes. The 20
//     legacy demo scripts under e2e/corpus-*/run.sh that predate the
//     protocol (corpus-cloudfront, corpus-crossing and others - never
//     registered in live/gauntlet/estates.json, never run by `tools/gauntlet
//     run`) do not, and are out of scope here the same way they are out of
//     scope for the artifact.
//   - copiesACorpusModule: references a real .corpus/<something> path -
//     the pristine corpus checkout `just corpus-fetch` populates. This is
//     what separates an actual corpus crossing from a script like
//     reference-ec2-vpc, which says so directly ("No corpus, no .corpus
//     dependency") and hand-authors its own versions.tf with its own
//     already-exact pin - a real but DIFFERENT defect (tracked separately;
//     seed #1041's PR body).
//   - declaresHashicorpAWS: the module it copies actually requires
//     hashicorp/aws at all. corpus-quickpizza copies out of .corpus and
//     speaks the protocol but its copied module carries no cloud provider
//     (kubernetes + helm + random only) - there is no AWS provider version
//     for a mirror lag to disagree about, and requiring a pin call there
//     would be a check with nothing to check.
var (
	speaksGauntletProtocol = regexp.MustCompile(`(?m)^gauntlet_begin\b`)
	copiesACorpusModule    = regexp.MustCompile(`\.corpus/`)
	declaresHashicorpAWS   = regexp.MustCompile(`hashicorp/aws`)
)

// gauntletCrossingScriptsThatDeclareAWS lists, relative to live/, every
// e2e/*/run.sh matching the three criteria above - the set issue #1041's
// guard actually checks. Fails the test outright (via t.Fatalf, not
// t.Errorf) if it cannot walk the directory, since a guard that silently
// checks zero scripts is worse than no guard.
func gauntletCrossingScriptsThatDeclareAWS(t *testing.T) []string {
	t.Helper()
	var matches []string
	entries, err := filepath.Glob("e2e/*/run.sh")
	if err != nil {
		t.Fatalf("globbing e2e/*/run.sh: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("e2e/*/run.sh matched nothing - the guard would silently check zero scripts")
	}
	for _, rel := range entries {
		if !gauntletCrossingScriptPattern.MatchString(rel) {
			continue
		}
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("reading live/%s: %v", rel, err)
		}
		src := string(data)
		if !speaksGauntletProtocol.MatchString(src) {
			continue
		}
		if !copiesACorpusModule.MatchString(src) {
			continue
		}
		if !declaresHashicorpAWS.MatchString(src) {
			continue
		}
		matches = append(matches, rel)
	}
	sort.Strings(matches)
	return matches
}

// awsProviderPinPattern matches live/oracle-versions.json's own
// aws_provider_version field: a bare semver, no leading "v", no
// pre-release/build suffix - the same shape a real hashicorp/aws release
// tag takes.
var awsProviderPinPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// TestGauntletCrossingScriptsPinOneAWSProvider is issue #1034's guard,
// widened by #1041 to every crossing script rather than the five #1034
// happened to find broken first: one crossing reads one hashicorp/aws
// version from one place - live/oracle-versions.json's
// aws_provider_version, applied through live/e2e/lib/gauntlet.sh's
// gauntlet_pin_aws_provider - rather than letting a bare lower-bound
// constraint float to whatever registry.terraform.io serves the morning
// stage 1 runs while choudoufu's own init resolves the same bare source
// against registry.opentofu.org.
//
// A real network call to both registries is deliberately NOT made here (a
// `go test` in this package must not depend on the network); that check is
// documented in live/e2e/lib/gauntlet.sh's own comment as a manual step a
// human runs before bumping the pin. What this test CAN verify without a
// network call: the pin exists and looks like a release, the shared
// function exists and reads the pin file rather than a hardcoded literal,
// and that gauntletCrossingScriptsThatDeclareAWS (every crossing script
// that actually declares hashicorp/aws, discovered by content rather than
// a hand-maintained list - see its own doc comment) calls the pin helper
// and never reintroduces a float via -upgrade before it has pinned.
//
// The -upgrade check is ordering-sensitive rather than a blanket ban: an
// -upgrade on an init that runs AFTER the script's own first
// gauntlet_pin_aws_provider call is resolving an already-exact `= X`
// constraint, which -upgrade cannot move outside of - several of the 22
// (corpus-s3-bucket-complete among them) pass -upgrade on a later stock
// oracle's re-init of a tree copied FROM an already-pinned directory, and
// that is not the bug #1034 named. An -upgrade appearing in the script
// BEFORE its first pin call is the real danger (the copy is still on its
// original bare lower bound at that point), so that ordering is what
// fails here.
func TestGauntletCrossingScriptsPinOneAWSProvider(t *testing.T) {
	var oracle struct {
		AWSProviderVersion string `json:"aws_provider_version"`
	}
	decodeInto(t, "oracle-versions.json", &oracle)
	if !awsProviderPinPattern.MatchString(oracle.AWSProviderVersion) {
		t.Fatalf("live/oracle-versions.json's aws_provider_version is %q, which is not a bare X.Y.Z release - a human must hand-edit this field the way terraform_version/tofu_version are maintained",
			oracle.AWSProviderVersion)
	}

	lib, err := os.ReadFile("e2e/lib/gauntlet.sh")
	if err != nil {
		t.Fatalf("reading live/e2e/lib/gauntlet.sh: %v", err)
	}
	libSrc := string(lib)
	if !strings.Contains(libSrc, "gauntlet_pin_aws_provider()") {
		t.Fatalf("live/e2e/lib/gauntlet.sh no longer defines gauntlet_pin_aws_provider - issue #1034's single pin point is gone")
	}
	if !strings.Contains(libSrc, "oracle-versions.json") {
		t.Fatalf("live/e2e/lib/gauntlet.sh's gauntlet_pin_aws_provider no longer reads live/oracle-versions.json - it must read the ONE place the pin lives, never a literal it carries itself")
	}
	if strings.Contains(libSrc, `"6.`) {
		t.Fatalf("live/e2e/lib/gauntlet.sh appears to carry a literal hashicorp/aws version (a quoted \"6.*\" string) - the pin must be read from live/oracle-versions.json, not hardcoded in the shell function")
	}

	scripts := gauntletCrossingScriptsThatDeclareAWS(t)
	t.Logf("checking %d crossing script(s) that copy a corpus module and declare hashicorp/aws: %v", len(scripts), scripts)

	for _, rel := range scripts {
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Errorf("reading live/%s: %v", rel, err)
			continue
		}
		// Whole-line "# ..." comments are dropped before either substring
		// search: this script's own doc comments (this PR's included) name
		// gauntlet_pin_aws_provider and -upgrade in prose - corpus-xancloud-
		// iac's header even quotes another project's README using
		// `tofu init -upgrade` verbatim - and neither occurrence calls
		// anything. A trailing "code # comment" on an otherwise real line
		// is left alone (several scripts rely on it, e.g. version = "= X"
		// # DELTA 2), since only a FULL comment line risks being mistaken
		// for a call or a flag here.
		src := codeOnlyLines(string(data))
		pinAt := strings.Index(src, "gauntlet_pin_aws_provider")
		if pinAt < 0 {
			t.Errorf("live/%s copies a corpus module that declares hashicorp/aws but never calls gauntlet_pin_aws_provider - its requirement floats to whatever registry.terraform.io serves the morning stage 1 runs while choudoufu's own init resolves the same bare source against registry.opentofu.org (issue #1041)",
				rel)
			continue
		}
		if upgradeAt := strings.Index(src, "-upgrade"); upgradeAt >= 0 && upgradeAt < pinAt {
			t.Errorf("live/%s passes -upgrade to a terraform/tofu init before its first gauntlet_pin_aws_provider call - that init still resolves the corpus module's original bare lower bound, re-floating the version the pin call would otherwise have fixed (issue #1041)",
				rel)
		}
	}
}

// codeOnlyLines drops every line whose first non-whitespace character is
// "#" (a shell comment occupying the whole line), joining what remains
// back with newlines. A trailing "real code # comment" is left intact -
// this only removes lines that are comments in their entirety, which is
// the sole shape this file's own prose ever takes.
func codeOnlyLines(src string) string {
	lines := strings.Split(src, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func decodeInto(t *testing.T, rel string, v any) {
	t.Helper()
	data, err := os.ReadFile(rel) //nolint:gosec // fixed paths inside the checkout
	if err != nil {
		t.Fatalf("reading live/%s: %v", rel, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decoding live/%s: %v", rel, err)
	}
}

// exactVersionPinLiteral matches an HCL `version = "= X.Y.Z"` assignment -
// the exact-constraint shape gauntlet_pin_aws_provider itself writes, and
// the only shape a hashicorp/aws requirement takes anywhere in the
// gauntlet. It is deliberately NOT a bare X.Y.Z: corpus-eks-basic rewrites
// a terraform-aws-eks MODULE version (`version = "6.6.1"`, no "=") as part
// of its own reduction, and a module version is a real literal that must
// stay one - gauntlet_pin_aws_provider never touches it, so it cannot
// drift from the provider pin.
//
// The optional backslashes are what let one pattern read both shapes a
// script writes this in: the HCL text of a heredoc, and the perl -0pi
// search pattern of a live-block delta, where every dot and sometimes
// every quote is escaped (`version = \"= 6\.59\.0\"`). Callers strip
// backslashes before matching, so both collapse to the same string.
var exactVersionPinLiteral = regexp.MustCompile(`version\s*=\s*"\s*=\s*[0-9]+\.[0-9]+\.[0-9]+\s*"`)

// TestGauntletPinCallersCarryNoVersionLiteral is issue #1207's guard, and
// it exists because #1041's guard above cannot see this bug by
// construction. #1041 checks that a crossing script CALLS
// gauntlet_pin_aws_provider. Both scripts #1207 names call it - six times
// and twice - and then contradict the call: they spell the provider
// release out a second time, as a literal, and use that literal either as
// the search pattern of the perl rewrite that inserts their live block, or
// as the requirement of a stock oracle's own working directory.
//
// The first shape is fatal the moment the pin moves. gauntlet_pin_aws_
// provider rewrites the copied versions.tf to
// live/oracle-versions.json's aws_provider_version one step earlier, so a
// pattern anchored on the OLD release matches nothing, the delta is never
// applied, and the script's own `grep -q estate=` fails:
// corpus-security-group-complete and corpus-autoscaling-complete both died
// this way when the pin went 6.59.0 -> 6.63.0, nine and eleven stages
// never run, while the board still read their five-day-old clear=true.
//
// The second shape is quieter and was the reason not to fix only the two
// loud ones. A stock oracle's heredoc-authored main.tf that is passed
// through gauntlet_pin_aws_provider has its literal overwritten, so the
// digits are dead text - harmless today, and the next reader's evidence
// that the two shapes are the same bug (corpus-vpc-complete carried
// "= 6.59.0" and passed 11/11 on the day the other two could not reach
// stage 1). One that is NOT passed through it is neither dead nor
// harmless: corpus-iam-policy's day2_count oracle really did init at
// 6.58.0 and corpus-iam-read-only-policy's at 6.59.0, each standing in as
// "what stock does" for an estate running 6.63.0 - the two-halves-two-
// versions split issue #1034 exists to prevent, arrived from inside one
// script instead of from two registries.
//
// So the rule is not "call the helper" but "do not also carry the answer":
// a script that calls gauntlet_pin_aws_provider must not spell an exact
// provider version anywhere in its own code. A placeholder that is not a
// valid constraint (the scripts use "PINNED-BY-GAUNTLET") is what the
// heredocs write instead, so that dropping the pin call fails `init`
// loudly rather than silently measuring against a stale release.
//
// Scope, and what this deliberately does not cover:
//
//   - Scripts that never call the helper are not checked here. The ~15
//     pre-protocol legacy demos under e2e/corpus-*/run.sh (corpus-
//     cloudfront, corpus-crossing, corpus-message-queue and siblings) each
//     rewrite a corpus module's own constraint to a literal they choose,
//     read that same literal back, and are self-consistent; they are not
//     registered in live/gauntlet/estates.json, `tools/gauntlet run` never
//     runs them, and they are out of scope here the same way they are out
//     of scope for #1041's guard and for the artifact.
//   - e2e/reference-ec2-vpc/run.sh is the one registered estate that
//     speaks the protocol, carries 19 exact literals, and still is not
//     checked: it copies no corpus module and hand-authors its own
//     versions.tf, which is the separate defect #1041's own PR body seeds.
//     Widening this guard to it would be a real fix, but a different one.
func TestGauntletPinCallersCarryNoVersionLiteral(t *testing.T) {
	entries, err := filepath.Glob("e2e/*/run.sh")
	if err != nil {
		t.Fatalf("globbing e2e/*/run.sh: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("e2e/*/run.sh matched nothing - the guard would silently check zero scripts")
	}

	checked := 0
	for _, rel := range entries {
		if !gauntletCrossingScriptPattern.MatchString(rel) {
			continue
		}
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Errorf("reading live/%s: %v", rel, err)
			continue
		}
		lines := strings.Split(string(data), "\n")

		// Whole-line comments are dropped exactly as #1041's guard drops
		// them, and for the same reason: the prose above and the prose in
		// these scripts names both the helper and the releases it replaced.
		callsPin := false
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if strings.Contains(line, "gauntlet_pin_aws_provider") {
				callsPin = true
				break
			}
		}
		if !callsPin {
			continue
		}
		checked++

		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			// A perl search pattern escapes the dots, and a double-quoted
			// perl -e escapes the quotes too; collapsing backslashes makes
			// both read as the HCL they match against.
			if lit := exactVersionPinLiteral.FindString(strings.ReplaceAll(line, `\`, "")); lit != "" {
				t.Errorf("live/%s:%d calls gauntlet_pin_aws_provider and then spells an exact provider version out itself (%s) - that literal is a second copy of live/oracle-versions.json's aws_provider_version and stops agreeing with it at the next bump. Match the version field by shape (version = \"[^\"]*\") in a rewrite, or write the placeholder \"PINNED-BY-GAUNTLET\" in a heredoc and let the pin call fill it in (issue #1207)",
					rel, i+1, lit)
			}
		}
	}

	if checked == 0 {
		t.Fatalf("no e2e/*/run.sh calls gauntlet_pin_aws_provider - this guard checked nothing, which is worse than not existing")
	}
	t.Logf("checked %d crossing script(s) that call gauntlet_pin_aws_provider", checked)
}

// --- issue #1139: coverage, not presence -----------------------------------
//
// TestGauntletCrossingScriptsPinOneAWSProvider above (issue #1041) asks only
// whether a script calls gauntlet_pin_aws_provider ANYWHERE. One occurrence
// anywhere in the file satisfies it, so a script that copies several
// independent trees out of the corpus (PLAIN / GREEN / ADOPTED / an oracle,
// each its own directory, each its own `terraform/tofu init`) and pins only
// one of them passes exactly like a script that pins all of them.
// corpus-leynos-monitoring is the live example this issue names: it copies
// its monitoring module fresh into four separate trees (copy_module called
// for $PLAIN, $ESTATE, $GREEN and $ORACLE_GREEN) and never pins any of
// them - the three gauntlet_pin_aws_provider calls the presence guard finds
// all target an unrelated synthetic day2_count oracle
// ($COUNT_ORACLE_DIR/main.tf) built from a heredoc, not a corpus copy.
//
// analyzeCorpusCopyCoverage below counts, instead of merely detecting:
//
//   - a "copy point" is a place the script establishes a FRESH tree from a
//     .corpus/ source - either a call to a helper function whose body itself
//     copies from a corpus-derived variable (copy_tree/copy_estate/
//     copy_module and the like: this repo's dominant shape, one call per
//     tree), or, for a script with no such helper, a bare `cp`/`rsync` line
//     at the top level that reads from one. Consecutive bare-copy lines
//     (within 2 lines of each other) are one copy point, not one per
//     statement: several scripts assemble a single tree with more than one
//     `cp` (module files, then the example directory). A bare copy whose
//     only destination(s) sit under a "/modules/" path is not counted at
//     all - every script observed here follows the convention documented in
//     e2e/lib/gauntlet.sh's gauntlet_pin_aws_provider doc comment, that a
//     referenced child module's own required_providers (if it has one) is
//     satisfied by intersection with the root's exact pin and is never
//     itself an init target, so copying one fresh needs no pin of its own.
//   - a "pin point" is a DISTINCT DESTINATION named by a direct top-level
//     gauntlet_pin_aws_provider call, or by a call to a wrapper function
//     whose body calls it (apply_deltas, write_root, version_pin and the
//     like - one call per tree is this repo's dominant shape). The
//     destination is the call site's own argument list, taken verbatim
//     before any `|| fail ...` tail: N calls naming the same argument are
//     one tree pinned, not N. corpus-leynos-monitoring is why - its three
//     gauntlet_pin_aws_provider calls all name the same
//     "$COUNT_ORACLE_DIR/main.tf", one unrelated synthetic file rewritten
//     three times as the day2_count oracle re-runs, and counting them as
//     three covered trees is what let four floating copy points read as a
//     surplus. A wrapper call with a distinct argument is still a distinct
//     destination, which is the shape every other crossing script uses.
//
// Coverage is copy points <= pin points + any stated exemption (see
// gauntletPinCoverageFloatMarker below). It is still a count rather than a
// per-tree match - nothing here resolves "$EST" to the directory a copy
// point wrote, so a script that pins tree A twice under two spellings while
// tree B floats would read as covered. Deduping destinations narrows that
// to spellings rather than repetitions, and is what makes
// corpus-leynos-monitoring read as the one-pin, four-copy script it
// actually is; every one of the other 24 scripts
// gauntletCrossingScriptsThatDeclareAWS finds today already satisfies the
// deduped count by the pattern they already use.
//
// gauntletPinCoverageFloatMarkerPattern: a script may deliberately leave a
// copy floating - corpus-leynos-monitoring's root does, on purpose, because
// it is a control measuring whether hashicorp/aws's old, stable
// aws_cloudwatch_metric_alarm/aws_cloudwatch_dashboard schemas hold across
// the 5.x/6.x boundary, and pinning it to an exact 6.x release would
// collapse the thing it exists to measure (see that script's own "THE OTHER
// SCOPING DECISION" comment). A script records that decision with
//
//	# GAUNTLET_PIN_COVERAGE_FLOAT(n): <reason>
//
// where n is the number of copy points the exemption covers and reason is
// not empty. Both are required and checked: a marker with no reason, an
// empty reason, or a non-positive n is reported as an error in its own
// right, on the theory this issue states directly - "an exemption without a
// stated reason is how a coverage guard becomes a presence guard again."
// There is deliberately no blanket "exempt this whole script" flag: the
// count must be SPENT, exactly, against the actual deficit. An exemption
// larger than copy points - pin points is itself an error, not a harmless
// margin - unspent credit is pre-approval for the next float, and a copy
// point added tomorrow to an already-exempted script would be silently
// covered by someone else's old, unrelated reason. That is what the
// over-exemption check below refuses; the first version of this guard
// promised it in this paragraph and did not do it, which let
// corpus-leynos-monitoring carry three spare exemptions against three
// misdirected pins.
var (
	corpusVarAssignPattern     = regexp.MustCompile(`^\s*(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	heredocStartPattern        = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)
	shellFuncDefPattern        = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(\)\s*\{(.*)$`)
	shellTopLevelCallPattern   = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(.*)$`)
	quotedShellArgPattern      = regexp.MustCompile(`"([^"]*)"`)
	gauntletPinCoverageFloatRe = regexp.MustCompile(`GAUNTLET_PIN_COVERAGE_FLOAT\(([^)]*)\)\s*:\s*(\S.*)$`)
)

// heredocLineMask marks every physical line of lines that lies inside a
// <<EOF-style heredoc body, including the line carrying the closing
// delimiter itself. Those lines are payload - frequently literal HCL,
// which can carry a bare "}" - and every scan below must skip them or risk
// mistaking a heredoc's own content for shell structure (a real bug caught
// while building this guard: write_root()-style functions in several
// scripts write a `}` as part of the HCL they emit, which ended a naive
// brace-counted function body several hundred lines early).
func heredocLineMask(lines []string) []bool {
	n := len(lines)
	mask := make([]bool, n)
	i := 0
	for i < n {
		if strings.Contains(lines[i], "<<") {
			if m := heredocStartPattern.FindStringSubmatch(lines[i]); m != nil {
				delim := m[1]
				j := i + 1
				for j < n && strings.TrimSpace(lines[j]) != delim {
					mask[j] = true
					j++
				}
				if j < n {
					mask[j] = true
				}
				i = j + 1
				continue
			}
		}
		i++
	}
	return mask
}

// corpusCodeLine returns lines[i] unless it is heredoc payload or a
// whole-line shell comment (the same convention codeOnlyLines uses above),
// in which case it returns "" - present in the slice so line numbers still
// line up, absent from every content match.
func corpusCodeLine(lines []string, inHeredoc []bool, i int) string {
	if inHeredoc[i] {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
		return ""
	}
	return lines[i]
}

// corpusSourceVars finds every shell variable assigned, directly or
// transitively, from a path naming ".corpus" - a fixed point over ordinary
// NAME=value assignments, since a script commonly assigns one corpus-
// derived variable (CORPUS_DIR) and then several more (SRC, SRC_MODULE,
// SRC_EXAMPLE...) from it.
func corpusSourceVars(lines []string, inHeredoc []bool) map[string]bool {
	assigns := map[string][]string{}
	for i := range lines {
		cl := corpusCodeLine(lines, inHeredoc, i)
		if m := corpusVarAssignPattern.FindStringSubmatch(cl); m != nil {
			assigns[m[1]] = append(assigns[m[1]], m[2])
		}
	}
	corpusVars := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, rhss := range assigns {
			if corpusVars[name] {
				continue
			}
			for _, rhs := range rhss {
				if strings.Contains(rhs, ".corpus") {
					corpusVars[name], changed = true, true
					break
				}
				matched := false
				for cv := range corpusVars {
					if strings.Contains(rhs, "$"+cv) {
						corpusVars[name], changed, matched = true, true, true
						break
					}
				}
				if matched {
					break
				}
			}
		}
	}
	return corpusVars
}

// shellFuncBody is a function definition's extent: defLine is the `name()
// {` line itself, bodyStart/bodyEnd (inclusive) the lines between the
// braces. defLine is tracked separately from the body because a
// multi-line function's OWN declaration line is not part of [bodyStart,
// bodyEnd] - and, unguarded, textually matches shellTopLevelCallPattern
// exactly like a real call to that function would (`copy_tree() {` and
// `copy_tree "$PLAIN"` both start with the identifier "copy_tree"). Every
// range check below must treat defLine and [bodyStart, bodyEnd] as one
// combined exclusion, or a script's own function definitions inflate its
// copy/pin counts by one call each - found while proving this guard
// against a script with two separate pin-wrapper functions, where it
// silently hid a real one-copy-point deficit.
type shellFuncBody struct{ defLine, bodyStart, bodyEnd int }

func (fb shellFuncBody) contains(i int) bool {
	return i == fb.defLine || (i >= fb.bodyStart && i <= fb.bodyEnd)
}

// shellFunctionBodies finds every `name() { ... }` definition, using only
// non-heredoc, non-comment lines to find the closing brace.
func shellFunctionBodies(lines []string, inHeredoc []bool) map[string]shellFuncBody {
	n := len(lines)
	bodies := map[string]shellFuncBody{}
	i := 0
	for i < n {
		if inHeredoc[i] {
			i++
			continue
		}
		m := shellFuncDefPattern.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		name, rest := m[1], m[2]
		if strings.HasSuffix(strings.TrimSpace(rest), "}") {
			bodies[name] = shellFuncBody{defLine: i, bodyStart: i, bodyEnd: i}
			i++
			continue
		}
		bodyStart := i + 1
		j := bodyStart
		for j < n && !(!inHeredoc[j] && strings.TrimSpace(lines[j]) == "}") {
			j++
		}
		end := j - 1
		if j >= n {
			end = n - 1
		}
		bodies[name] = shellFuncBody{defLine: i, bodyStart: bodyStart, bodyEnd: end}
		i = j + 1
	}
	return bodies
}

func shellFuncBodyCode(lines []string, inHeredoc []bool, fb shellFuncBody) string {
	var b strings.Builder
	for i := fb.bodyStart; i <= fb.bodyEnd && i < len(lines); i++ {
		b.WriteString(corpusCodeLine(lines, inHeredoc, i))
		b.WriteByte('\n')
	}
	return b.String()
}

func insideAnyShellFuncBody(bodies map[string]shellFuncBody, i int) bool {
	for _, fb := range bodies {
		if fb.contains(i) {
			return true
		}
	}
	return false
}

// corpusCopyPattern builds the "this line copies from the corpus" matcher
// for one script's own set of corpus-source variables (issue #1041's
// scripts use SRC, SRC_MODULE, SRC_EXAMPLE, SRC_AWS... - the name varies,
// what they share is being assigned from a .corpus path). Matches both
// `cp` (every script but one) and `rsync` (corpus-sumaform-aws, the one
// script that copies its corpus tree with rsync instead of cp -R).
func corpusCopyPattern(corpusVars map[string]bool) *regexp.Regexp {
	if len(corpusVars) == 0 {
		return regexp.MustCompile(`\x00never-matches\x00`)
	}
	names := make([]string, 0, len(corpusVars))
	for v := range corpusVars {
		names = append(names, regexp.QuoteMeta(v))
	}
	sort.Strings(names)
	return regexp.MustCompile(`\b(?:cp|rsync)\b.*\$\{?(` + strings.Join(names, "|") + `)\b`)
}

// corpusCopyCoverage is analyzeCorpusCopyCoverage's verdict for one script.
type corpusCopyCoverage struct {
	copyPoints      int
	copyPointLines  []int // 1-based source line of each copy point, for the failure message
	pinPoints       int   // DISTINCT destinations named by pin call sites, not call sites
	pinCallSites    int   // every call site, before deduplication - for the failure message
	pinDestinations []string
	exempted        int
	exemptions      []string // "n: reason", one per valid GAUNTLET_PIN_COVERAGE_FLOAT marker
	malformed       []string // marker text that failed to state a positive count and a reason
}

// uncovered is the number of copy points neither pinned nor exempted.
func (c corpusCopyCoverage) uncovered() int { return c.copyPoints - c.pinPoints - c.exempted }

// deficit is how many copy points an exemption would have to cover for this
// script to be clean - the number an exemption may spend, floored at zero.
func (c corpusCopyCoverage) deficit() int {
	if d := c.copyPoints - c.pinPoints; d > 0 {
		return d
	}
	return 0
}

// pinDestinationKey identifies WHICH tree one pin call site pins, so that N
// calls naming the same destination count as one tree pinned rather than N
// trees covered (issue #1139's second instance: corpus-leynos-monitoring
// pins "$COUNT_ORACLE_DIR/main.tf" three times and no corpus copy at all).
//
// The key is the call's own argument list, taken verbatim - `$EST` and
// `${EST}` are different keys, deliberately, because nothing here expands a
// shell variable and guessing would be worse than over-counting. Anything
// after a `|| fail "..."` is dropped first: every direct call in this repo
// repeats its destination inside that message, and several repeat it with a
// different trailing explanation, so the tail is noise that would defeat the
// deduplication it is inside. A call with no quoted argument at all falls
// back to the raw text after the function name, and a call with nothing
// after it at all to the function name itself.
func pinDestinationKey(fname, codeLine string) string {
	line := codeLine
	if idx := strings.Index(line, "||"); idx >= 0 {
		line = line[:idx]
	}
	var args []string
	for _, m := range quotedShellArgPattern.FindAllStringSubmatch(line, -1) {
		args = append(args, m[1])
	}
	if len(args) > 0 {
		return "args:" + strings.Join(args, "\x1f")
	}
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fname))
	if rest != "" {
		return "raw:" + rest
	}
	return "func:" + fname
}

// analyzeCorpusCopyCoverage is this issue's coverage count - see the
// package-level doc comment above for what a "copy point" and a "pin
// point" are and why counting each this way is a real, stated limit
// rather than a guarantee that every specific tree is covered.
func analyzeCorpusCopyCoverage(src string) corpusCopyCoverage {
	lines := strings.Split(src, "\n")
	inHeredoc := heredocLineMask(lines)
	corpusVars := corpusSourceVars(lines, inHeredoc)
	copyPattern := corpusCopyPattern(corpusVars)

	bodies := shellFunctionBodies(lines, inHeredoc)
	copyFuncs := map[string]bool{}
	pinFuncs := map[string]bool{}
	for name, fb := range bodies {
		bc := shellFuncBodyCode(lines, inHeredoc, fb)
		if copyPattern.MatchString(bc) {
			copyFuncs[name] = true
		}
		if strings.Contains(bc, "gauntlet_pin_aws_provider") {
			pinFuncs[name] = true
		}
	}

	var copyPoints, pinCallSites int
	var copyPointLines []int
	var directCopyLines []int
	pinDests := map[string]bool{}

	for i := range lines {
		if insideAnyShellFuncBody(bodies, i) {
			continue
		}
		cl := corpusCodeLine(lines, inHeredoc, i)
		if strings.TrimSpace(cl) == "" {
			continue
		}
		fname := ""
		if m := shellTopLevelCallPattern.FindStringSubmatch(cl); m != nil {
			fname = m[1]
			if copyFuncs[fname] {
				copyPoints++
				copyPointLines = append(copyPointLines, i+1)
			}
			if fname == "gauntlet_pin_aws_provider" || pinFuncs[fname] {
				pinCallSites++
				pinDests[pinDestinationKey(fname, cl)] = true
			}
		}
		if copyPattern.MatchString(cl) && !copyFuncs[fname] {
			args := []string{}
			for _, m := range quotedShellArgPattern.FindAllStringSubmatch(cl, -1) {
				args = append(args, m[1])
			}
			excludeAsChildModule := false
			if len(args) > 0 {
				check := args
				if len(args) > 1 {
					check = args[1:]
				}
				excludeAsChildModule = true
				for _, a := range check {
					if !strings.Contains(a, "/modules/") && !strings.HasSuffix(strings.TrimRight(a, "/"), "/modules") {
						excludeAsChildModule = false
						break
					}
				}
			}
			if !excludeAsChildModule {
				directCopyLines = append(directCopyLines, i)
			}
		}
	}

	// Consecutive bare-copy lines (gap <= 2) assemble ONE tree, not one
	// copy point each - see the doc comment above.
	sort.Ints(directCopyLines)
	var run []int
	flush := func() {
		if len(run) > 0 {
			copyPoints++
			copyPointLines = append(copyPointLines, run[0]+1)
			run = nil
		}
	}
	for _, ln := range directCopyLines {
		if len(run) > 0 && ln-run[len(run)-1] <= 2 {
			run = append(run, ln)
		} else {
			flush()
			run = []int{ln}
		}
	}
	flush()

	sort.Ints(copyPointLines)
	dests := make([]string, 0, len(pinDests))
	for d := range pinDests {
		dests = append(dests, strings.TrimPrefix(strings.TrimPrefix(d, "args:"), "raw:"))
	}
	sort.Strings(dests)
	cov := corpusCopyCoverage{
		copyPoints:      copyPoints,
		copyPointLines:  copyPointLines,
		pinPoints:       len(pinDests),
		pinCallSites:    pinCallSites,
		pinDestinations: dests,
	}
	for i, raw := range lines {
		if inHeredoc[i] || !strings.Contains(raw, "GAUNTLET_PIN_COVERAGE_FLOAT") {
			continue
		}
		m := gauntletPinCoverageFloatRe.FindStringSubmatch(raw)
		if m == nil {
			cov.malformed = append(cov.malformed, fmt.Sprintf("line %d: GAUNTLET_PIN_COVERAGE_FLOAT marker does not match the required GAUNTLET_PIN_COVERAGE_FLOAT(N): <reason> shape", i+1))
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(m[1]))
		reason := strings.TrimSpace(m[2])
		if err != nil || n <= 0 || reason == "" {
			cov.malformed = append(cov.malformed, fmt.Sprintf("line %d: GAUNTLET_PIN_COVERAGE_FLOAT(%s) must name a positive count and a non-empty reason - a bare or zero exemption is how a coverage guard becomes a presence guard again", i+1, strings.TrimSpace(m[1])))
			continue
		}
		cov.exempted += n
		cov.exemptions = append(cov.exemptions, fmt.Sprintf("%d: %s", n, reason))
	}
	return cov
}

// corpusCopyCoverageViolation is one rule a script's coverage breaks, with
// the kind separated from the prose so a test can assert WHICH rule fired
// without matching on message text.
type corpusCopyCoverageViolation struct {
	kind    string
	message string
}

const (
	coverageViolationMalformedMarker = "malformed-marker"
	coverageViolationUncovered       = "uncovered-copy-point"
	coverageViolationOverExempted    = "over-exempted"
)

// corpusCopyCoverageViolations is the whole verdict of issue #1139's guard,
// factored out of the test loop so that
// TestCorpusCopyCoverageIsRedOnTheRealLeynosScript proves the real guard red
// rather than a paraphrase of it that could drift away from what CI runs.
func corpusCopyCoverageViolations(rel string, cov corpusCopyCoverage) []corpusCopyCoverageViolation {
	var out []corpusCopyCoverageViolation
	for _, bad := range cov.malformed {
		out = append(out, corpusCopyCoverageViolation{
			kind:    coverageViolationMalformedMarker,
			message: fmt.Sprintf("live/%s: %s", rel, bad),
		})
	}
	if uncovered := cov.uncovered(); uncovered > 0 {
		out = append(out, corpusCopyCoverageViolation{
			kind: coverageViolationUncovered,
			message: fmt.Sprintf("live/%s establishes a fresh tree from the corpus at %d place(s) (lines %v) but pins only %d distinct destination(s) %v (across %d gauntlet_pin_aws_provider call site(s), directly or through a wrapper function called once per tree), with %d exempted by a stated GAUNTLET_PIN_COVERAGE_FLOAT marker - at least %d of the listed place(s) are left uncovered and will float to whatever registry.terraform.io serves the morning stage 1 runs (issue #1139, widening #1041's presence-only check; this is a count over destinations, not a per-tree match, so it cannot say WHICH of the listed lines are the uncovered ones, only that not all of them can be)",
				rel, cov.copyPoints, cov.copyPointLines, cov.pinPoints, cov.pinDestinations, cov.pinCallSites, cov.exempted, uncovered),
		})
	}
	// Over-exemption is its own error, not a harmless margin: unspent
	// exemption credit silently covers the NEXT floating copy point somebody
	// adds, under a reason written for an unrelated one. The stated count has
	// to be spent, exactly, against the deficit it is written for.
	if cov.exempted > cov.deficit() {
		out = append(out, corpusCopyCoverageViolation{
			kind: coverageViolationOverExempted,
			message: fmt.Sprintf("live/%s states GAUNTLET_PIN_COVERAGE_FLOAT exemptions totalling %d %v but its actual deficit is only %d (%d copy point(s) at lines %v, %d distinct pinned destination(s) %v) - %d of that credit is unspent, and unspent credit is cover for the next floating copy point added to this script under a reason written for a different one; state exactly the deficit (issue #1139)",
				rel, cov.exempted, cov.exemptions, cov.deficit(), cov.copyPoints, cov.copyPointLines, cov.pinPoints, cov.pinDestinations, cov.exempted-cov.deficit()),
		})
	}
	return out
}

// TestGauntletCrossingScriptsCoverEveryCorpusCopy is issue #1139's guard: it
// widens TestGauntletCrossingScriptsPinOneAWSProvider's presence check
// (does the script call gauntlet_pin_aws_provider at all) to a coverage
// count (does it pin - directly, or through a wrapper called once per tree -
// as many DISTINCT destinations as it establishes fresh trees from the
// corpus, with any shortfall spent exactly by a stated
// GAUNTLET_PIN_COVERAGE_FLOAT exemption). See the package-level doc comment
// above analyzeCorpusCopyCoverage for the exact rule and its stated limit,
// and corpusCopyCoverageViolations for the three ways a script fails it.
func TestGauntletCrossingScriptsCoverEveryCorpusCopy(t *testing.T) {
	scripts := gauntletCrossingScriptsThatDeclareAWS(t)
	checked := 0
	for _, rel := range scripts {
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Errorf("reading live/%s: %v", rel, err)
			continue
		}
		checked++
		cov := analyzeCorpusCopyCoverage(string(data))
		if len(cov.exemptions) > 0 {
			t.Logf("live/%s: %d copy point(s) exempted: %v", rel, cov.exempted, cov.exemptions)
		}
		for _, v := range corpusCopyCoverageViolations(rel, cov) {
			t.Errorf("%s", v.message)
		}
	}
	if checked == 0 {
		t.Fatalf("no crossing script checked - this guard checked nothing, which is worse than not existing")
	}
	t.Logf("checked coverage for %d crossing script(s)", checked)
}

// leynosCoverageScript is the real, on-disk corpus-leynos-monitoring script -
// the one live example issue #1139 names. The proof below runs against it
// rather than a manufactured copy of its shape, because the manufactured
// copies in TestAnalyzeCorpusCopyCoverage cannot notice that the real
// script's three gauntlet_pin_aws_provider calls all name one destination.
func leynosCoverageScript(t *testing.T) string {
	t.Helper()
	const rel = "e2e/corpus-leynos-monitoring/run.sh"
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("reading live/%s: %v", rel, err)
	}
	return string(data)
}

// mustReplaceOnce is substitution that cannot silently no-op. A proof that
// edits a real file's text has to fail loudly when the text it edits has
// moved, or it goes on "passing" while testing the unedited original.
func mustReplaceOnce(t *testing.T, src, old, new string) string {
	t.Helper()
	if n := strings.Count(src, old); n != 1 {
		t.Fatalf("expected exactly one occurrence of %q in live/e2e/corpus-leynos-monitoring/run.sh, found %d - this proof edits the real script's text, so it must fail rather than silently test the unedited original", old, n)
	}
	return strings.Replace(src, old, new, 1)
}

// TestCorpusCopyCoverageIsRedOnTheRealLeynosScript is this guard's own
// red-before-green proof, run on every `go test` rather than once by hand.
//
// The first version of the coverage guard (issue #1139's first pass) promised
// in prose that "the count must be spent against the actual deficit, so a
// future copy point added to an already-exempted script is not silently
// covered by someone else's old, unrelated reason" - and did not do it. On
// the real script it read copyPoints=4, pinPoints=3, exempted=4: three spare
// exemption credits, so a fifth, sixth or seventh floating copy_module could
// be added and the guard would still have been green. Two things were wrong:
// the three pin calls all rewrite the same "$COUNT_ORACLE_DIR/main.tf" and
// were counted as three covered trees, and an exemption bigger than the
// deficit was accepted as a harmless margin.
//
// Case (b) is the one that matters: it is the regression the first version
// did not catch, and it is written against what the guard promises rather
// than against how it is implemented.
func TestCorpusCopyCoverageIsRedOnTheRealLeynosScript(t *testing.T) {
	const rel = "e2e/corpus-leynos-monitoring/run.sh"
	const statedMarker = "GAUNTLET_PIN_COVERAGE_FLOAT(3)"

	real := leynosCoverageScript(t)

	// The arithmetic the whole proof rests on, asserted by value: four copy
	// points, ONE distinct pinned destination (three call sites naming it),
	// so a deficit of exactly three.
	base := analyzeCorpusCopyCoverage(real)
	if base.copyPoints != 4 || base.pinPoints != 1 || base.pinCallSites != 3 || base.exempted != 3 {
		t.Fatalf("live/%s: copyPoints=%d pinPoints=%d pinCallSites=%d exempted=%d, want 4/1/3/3 - the rest of this proof is written against those numbers (copy lines %v, pinned destinations %v)",
			rel, base.copyPoints, base.pinPoints, base.pinCallSites, base.exempted, base.copyPointLines, base.pinDestinations)
	}
	if base.deficit() != 3 {
		t.Fatalf("live/%s: deficit=%d, want 3", rel, base.deficit())
	}

	tests := []struct {
		name     string
		src      string
		wantKind string // "" means the guard must be silent
	}{
		{
			// (a) the marker as #1229 shipped it: four stated against a
			// deficit of three. Green under the first version of the guard.
			name:     "the shipped exemption of four is one credit more than the deficit",
			src:      mustReplaceOnce(t, real, statedMarker, "GAUNTLET_PIN_COVERAGE_FLOAT(4)"),
			wantKind: coverageViolationOverExempted,
		},
		{
			// (b) the regression. With the corrected marker there is no
			// spare credit, so one more floating copy point is uncovered.
			// Under the first version of the guard (exempted 4, pinPoints 3)
			// this was green, and stayed green for two more copy points
			// after it.
			name:     "one more floating copy point is uncovered once the credit is spent exactly",
			src:      real + "\ncopy_module \"$SOME_NEW_TREE\"\n",
			wantKind: coverageViolationUncovered,
		},
		{
			// (c) the script as it stands.
			name: "the corrected marker spends the deficit exactly and is clean",
			src:  real,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cov := analyzeCorpusCopyCoverage(tc.src)
			got := corpusCopyCoverageViolations(rel, cov)
			if tc.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("want no violation, got %d: %v (copyPoints=%d pinPoints=%d exempted=%d)",
						len(got), got, cov.copyPoints, cov.pinPoints, cov.exempted)
				}
				return
			}
			if len(got) != 1 || got[0].kind != tc.wantKind {
				t.Fatalf("want exactly one %s violation, got %v (copyPoints=%d pinPoints=%d pinCallSites=%d exempted=%d deficit=%d uncovered=%d)",
					tc.wantKind, got, cov.copyPoints, cov.pinPoints, cov.pinCallSites, cov.exempted, cov.deficit(), cov.uncovered())
			}
			t.Logf("red as required: %s", got[0].message)
		})
	}
}

// TestAnalyzeCorpusCopyCoverage exercises analyzeCorpusCopyCoverage directly
// against manufactured scripts, so the coverage rule's red and green paths
// - and the exemption's own validation - are proven independent of what
// today's 25 real scripts happen to look like. The first case reproduces
// corpus-leynos-monitoring's actual shape (a copy helper called four times,
// a pin helper called for an unrelated synthetic root) as a second,
// disposable instance of the bug this issue names.
func TestAnalyzeCorpusCopyCoverage(t *testing.T) {
	const fourTreesOnePinned = `#!/usr/bin/env bash
SRC="$ROOT/.corpus/widget/module"
copy_module() { mkdir -p "$1"; cp -R "$SRC" "$1/module"; }

copy_module "$PLAIN"
copy_module "$GREEN"
copy_module "$ADOPTED"
copy_module "$ORACLE"

# unrelated synthetic root, never a corpus copy - same shape as
# corpus-leynos-monitoring's COUNT_ORACLE_DIR.
mkdir -p "$COUNT_ORACLE_DIR"
gauntlet_pin_aws_provider "$COUNT_ORACLE_DIR/main.tf"
`

	const fourTreesFullyPinned = `#!/usr/bin/env bash
SRC="$ROOT/.corpus/widget/module"
copy_tree() { mkdir -p "$1"; cp -R "$SRC" "$1/module"; }
apply_deltas() {
  local est="$1"
  gauntlet_pin_aws_provider "$est/versions.tf"
}

copy_tree "$PLAIN"
apply_deltas "$PLAIN"
copy_tree "$GREEN"
apply_deltas "$GREEN"
copy_tree "$ADOPTED"
apply_deltas "$ADOPTED"
copy_tree "$ORACLE"
apply_deltas "$ORACLE"
`

	// Three pin calls naming one destination, the way
	// corpus-leynos-monitoring rewrites its day2_count oracle root three
	// times: one tree pinned, not three.
	const fourTreesOneDestinationPinnedThrice = `#!/usr/bin/env bash
SRC="$ROOT/.corpus/widget/module"
copy_module() { mkdir -p "$1"; cp -R "$SRC" "$1/module"; }

copy_module "$PLAIN"
copy_module "$GREEN"
copy_module "$ADOPTED"
copy_module "$ORACLE"

mkdir -p "$COUNT_ORACLE_DIR"
gauntlet_pin_aws_provider "$COUNT_ORACLE_DIR/main.tf" || fail "gauntlet_pin_aws_provider failed for $COUNT_ORACLE_DIR/main.tf"
gauntlet_pin_aws_provider "$COUNT_ORACLE_DIR/main.tf" || fail "gauntlet_pin_aws_provider failed after the second write"
gauntlet_pin_aws_provider "$COUNT_ORACLE_DIR/main.tf" || fail "gauntlet_pin_aws_provider failed after the third write"
`

	exempted4 := "\n# GAUNTLET_PIN_COVERAGE_FLOAT(4): deliberate - a manufactured control, see the test that names this string.\n"
	exempted3 := "\n# GAUNTLET_PIN_COVERAGE_FLOAT(3): deliberate - spends this manufactured script's deficit exactly.\n"
	exempted1 := "\n# GAUNTLET_PIN_COVERAGE_FLOAT(1): deliberate - covers only one of the four, on purpose, for this test.\n"
	exemptedNoReason := "\n# GAUNTLET_PIN_COVERAGE_FLOAT(4):   \n"
	exemptedZero := "\n# GAUNTLET_PIN_COVERAGE_FLOAT(0): deliberate.\n"

	tests := []struct {
		name         string
		src          string
		wantCopy     int
		wantPin      int
		wantSites    int // pin CALL SITES, before deduplication by destination
		wantExempted int
		wantKinds    []string // the violations corpusCopyCoverageViolations must report, in order
	}{
		{
			name:      "four copies one misdirected pin is a deficit - corpus-leynos-monitoring's actual shape",
			src:       fourTreesOnePinned,
			wantCopy:  4,
			wantPin:   1,
			wantSites: 1,
			wantKinds: []string{coverageViolationUncovered},
		},
		{
			name:      "three calls naming one destination are one tree pinned, not three",
			src:       fourTreesOneDestinationPinnedThrice,
			wantCopy:  4,
			wantPin:   1,
			wantSites: 3,
			wantKinds: []string{coverageViolationUncovered},
		},
		{
			name:      "four copies through a wrapper that pins once per call is fully covered",
			src:       fourTreesFullyPinned,
			wantCopy:  4,
			wantPin:   4,
			wantSites: 4,
		},
		{
			name:         "an exemption spending the deficit exactly clears it",
			src:          fourTreesOnePinned + exempted3,
			wantCopy:     4,
			wantPin:      1,
			wantSites:    1,
			wantExempted: 3,
		},
		{
			name:         "an exemption larger than the deficit is refused, not banked",
			src:          fourTreesOnePinned + exempted4,
			wantCopy:     4,
			wantPin:      1,
			wantSites:    1,
			wantExempted: 4,
			wantKinds:    []string{coverageViolationOverExempted},
		},
		{
			name:         "a partial exemption leaves the remainder uncovered",
			src:          fourTreesOnePinned + exempted1,
			wantCopy:     4,
			wantPin:      1,
			wantSites:    1,
			wantExempted: 1,
			wantKinds:    []string{coverageViolationUncovered},
		},
		{
			name:      "an exemption with no reason is rejected outright, not silently accepted",
			src:       fourTreesOnePinned + exemptedNoReason,
			wantCopy:  4,
			wantPin:   1,
			wantSites: 1,
			wantKinds: []string{coverageViolationMalformedMarker, coverageViolationUncovered},
		},
		{
			name:      "an exemption for zero copy points is rejected outright",
			src:       fourTreesOnePinned + exemptedZero,
			wantCopy:  4,
			wantPin:   1,
			wantSites: 1,
			wantKinds: []string{coverageViolationMalformedMarker, coverageViolationUncovered},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cov := analyzeCorpusCopyCoverage(tc.src)
			if cov.copyPoints != tc.wantCopy {
				t.Errorf("copyPoints = %d, want %d (lines %v)", cov.copyPoints, tc.wantCopy, cov.copyPointLines)
			}
			if cov.pinPoints != tc.wantPin {
				t.Errorf("pinPoints = %d, want %d (destinations %v)", cov.pinPoints, tc.wantPin, cov.pinDestinations)
			}
			if cov.pinCallSites != tc.wantSites {
				t.Errorf("pinCallSites = %d, want %d", cov.pinCallSites, tc.wantSites)
			}
			if cov.exempted != tc.wantExempted {
				t.Errorf("exempted = %d, want %d", cov.exempted, tc.wantExempted)
			}
			var gotKinds []string
			for _, v := range corpusCopyCoverageViolations("manufactured/run.sh", cov) {
				gotKinds = append(gotKinds, v.kind)
			}
			if !reflect.DeepEqual(gotKinds, tc.wantKinds) {
				t.Errorf("violations = %v, want %v (copyPoints=%d pinPoints=%d exempted=%d deficit=%d uncovered=%d)",
					gotKinds, tc.wantKinds, cov.copyPoints, cov.pinPoints, cov.exempted, cov.deficit(), cov.uncovered())
			}
		})
	}
}
