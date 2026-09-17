// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
