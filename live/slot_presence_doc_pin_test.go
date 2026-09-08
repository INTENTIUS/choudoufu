// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// GitHub issue #969: three pages said, in three wordings, that every count
// instance carries a tofu-slot marker. live/MARKERS.md's tag table gave the
// key's "Present on" as "`count` instances only"; site/content/docs/claims.md
// said choudoufu "names each member with a tofu-slot marker" and that the
// lint boundary "forbids any argument from reading count.index";
// internal/live/doc.go said "Each instance carries a tofu-slot marker" and
// that "count.index is banned from identity-bearing resource arguments".
// MARKERS.md is the one that matters most, because it says of itself that it
// is the entire contract an external tool implements.
//
// The writer mints a slot only for a count block whose instances resolve
// ClassNeedsDiscovery. A block whose members the configuration itself names -
// which, for a set of more than one, means an injective count.index shape
// lint ADMITS - resolves ClassConcrete per instance, is never indexed as a
// count set by discovery, and carries tofu-estate and tofu-address alone.
// The operator who filed #969 read the table, applied two count-expanded
// aws_cloudwatch_log_groups with names built from count.index, and found
// what the table said could not happen.
//
// The oracle is the code, in three legs, none of which opens a document:
//
//   - lint.CheckContext files no RuleCountIndex issue over bucket =
//     "shard-${count.index}". This is the leg that measures "forbids any
//     argument from reading count.index", and it is why this file lives in
//     an external test package: internal/live/lint imports live.
//   - identity.Resolve over the same configuration answers ClassConcrete for
//     both instances of that block and ClassNeedsDiscovery for both
//     instances of a plain aws_eip count block. Two kinds in one
//     configuration, so no page can honestly describe only one.
//   - internal/live/check/testdata/identity-golden.txt, the real resolver's
//     rendered output over the in-repo fixtures, carries resource-level
//     count instances of BOTH kinds. The claim is not about a corner case:
//     when this was written the golden held 162 CONCRETE against 119
//     NEEDS_DISCOVERY.
//
// Only then is any document read, and the check is one-directional: it
// catches a page claiming the slot is on every count instance, or that
// count.index is banned outright. It cannot catch a page that describes the
// two kinds inaccurately in some new wording.
//
// PROVING IT RED. Restore any one of the five original phrases - the table
// cell "`count` instances only", claims.md's "names each member with a
// `tofu-slot` marker" or "forbids any argument from reading `count.index`",
// doc.go's "Each instance carries a tofu-slot marker" or "count.index is
// banned" - and TestSlotPresenceDocsMatchTheCode fails naming the file, the
// line and the phrase. Deleting the subject from a page instead does not
// pass either: each page must still carry a chunk about slot presence.
func TestSlotPresenceDocsMatchTheCode(t *testing.T) {
	named, fungible, goldenConcrete, goldenDiscovery := slotPresenceOracle(t)

	for _, path := range slotPresenceDocs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		topical := 0
		for _, chunk := range docChunks(slotPresenceParagraphs(path, raw)) {
			if !slotPresenceTopic.MatchString(chunk.text) {
				continue
			}
			topical++
			for _, phrase := range everyCountInstancePhrases {
				hit := phrase.FindString(chunk.text)
				if hit == "" {
					continue
				}
				t.Errorf("%s:%d says every count instance carries a tofu-slot, or that count.index is refused outright (%q), and neither is so.\n"+
					"lint files no %s issue over bucket = \"shard-${count.index}\"; identity resolution answers %s for both instances of that block and %s for both instances of aws_eip.pool; and %d of the resource-level count instances in internal/live/check/testdata/identity-golden.txt resolve CONCRETE against %d NEEDS_DISCOVERY. A CONCRETE count instance is never indexed as a count set by discovery, so no slot is minted for it and none is written - GitHub issue #969. State which count instances carry one, per live/MARKERS.md's \"Which count instances carry one\".\n\nThe chunk:\n%s",
					path, chunk.line, hit, lint.RuleCountIndex, named, fungible, goldenConcrete, goldenDiscovery, chunk.text)
			}
		}
		if topical == 0 {
			t.Errorf("%s no longer says anything about which resources carry a tofu-slot.\n"+
				"This pin exists because that claim was wrong on this page and could go wrong again, "+
				"so a page that drops the subject entirely makes it vacuous. Either restore the "+
				"guidance or, if the subject genuinely moved off this page, remove the path from "+
				"slotPresenceDocs here and say in the commit message where it went.", path)
		}
	}

	// MARKERS.md is the contract, so it does not merely have to avoid the
	// old claim: it has to answer the question #969 asked.
	markers, err := os.ReadFile(slotPresenceDocs[0])
	if err != nil {
		t.Fatalf("read %s: %v", slotPresenceDocs[0], err)
	}
	if !strings.Contains(string(markers), "### Which count instances carry one") {
		t.Errorf("%s no longer has the \"Which count instances carry one\" section.\n"+
			"That section is what an implementer of the contract reads to learn that a count set "+
			"either carries slots on every member or on none, and which kind it is looking at. "+
			"Rename it here and there together, deliberately.", slotPresenceDocs[0])
	}
}

// slotPresenceDocs are the pages that tell a reader which resources carry a
// tofu-slot. MARKERS.md is first because the test reads it again by name.
var slotPresenceDocs = []string{
	"MARKERS.md",
	"../site/content/docs/claims.md",
	"../internal/live/doc.go",
}

// slotPresenceTopic decides which chunks are about slot presence: the tag
// key itself, or a sentence about count.index being off limits in a
// resource argument (the rule whose narrowing created the slotless class in
// the first place). Gaps are \s+ rather than spaces because these files are
// hard-wrapped and a phrase can straddle two lines.
var slotPresenceTopic = regexp.MustCompile(`(?i)` + strings.Join([]string{
	"tofu-slot",
	`count\.index`,
	`\bslot\b`,
}, "|"))

// everyCountInstancePhrases are the five wordings the pages carried, each
// tolerant of a line break where the original was wrapped. They are written
// as the historical sentences rather than as a general "slot" pattern on
// purpose: the corrected pages talk about slots constantly, and a pattern
// broad enough to catch any claim about presence would convict them.
var everyCountInstancePhrases = []*regexp.Regexp{
	regexp.MustCompile("(?i)`?count`?\\s+instances\\s+only"),
	regexp.MustCompile("(?i)names\\s+each\\s+member\\s+with\\s+a\\s+`?tofu-slot"),
	regexp.MustCompile("(?i)each\\s+instance\\s+carries\\s+a\\s+`?tofu-slot"),
	regexp.MustCompile("(?i)every\\s+(?:count\\s+)?instance\\s+carries\\s+a\\s+`?tofu-slot"),
	regexp.MustCompile("(?i)count\\.index`?\\s+is\\s+banned"),
	regexp.MustCompile("(?i)forbids\\s+any\\s+argument\\s+from\\s+reading\\s+`?count\\.index"),
}

// slotPresenceOracle is the code side of the claim, answered before any
// document is opened. It returns the class identity resolution gives a
// count block the configuration names, the class it gives a fungible one,
// and the two golden populations.
//
// It fails rather than adapting if the code stops producing both kinds. A
// pin whose expectation follows the code it pins proves nothing, and this
// one has a direction: the pages were wrong in the "every count instance"
// direction, so the day a named count instance starts carrying a slot is
// the day a human rewrites this file and the pages together.
func slotPresenceOracle(t *testing.T) (identity.Class, identity.Class, int, int) {
	t.Helper()

	cfg := loadSlotPresenceFixture(t)

	for _, issue := range lint.CheckContext(t.Context(), cfg) {
		if issue.Rule == lint.RuleCountIndex {
			t.Fatalf("lint refuses bucket = \"shard-${count.index}\" with %s: %s\n"+
				"count.index in an identity-bearing argument is refused again, so the pages this test "+
				"holds to the code may be right and this test is now wrong. Rewrite both deliberately.",
				issue.Rule, issue.Detail)
		}
	}

	res, diags := identity.Resolve(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity resolution failed: %s", diags.Err())
	}
	named := slotPresenceClassAt(t, res, `aws_s3_bucket.shard[0]`)
	if other := slotPresenceClassAt(t, res, `aws_s3_bucket.shard[1]`); other != named {
		t.Fatalf("the two instances of aws_s3_bucket.shard resolve %s and %s; this pin assumes a "+
			"count block's instances share a class, which is what makes \"slots on every member or "+
			"none\" true of a whole set", named, other)
	}
	fungible := slotPresenceClassAt(t, res, `aws_eip.pool[0]`)
	if named != identity.ClassConcrete {
		t.Fatalf("aws_s3_bucket.shard[0] resolves %s, want %s; a count block the configuration names "+
			"is the population this pin is about", named, identity.ClassConcrete)
	}
	if fungible != identity.ClassNeedsDiscovery {
		t.Fatalf("aws_eip.pool[0] resolves %s, want %s; the fungible half of the claim has moved",
			fungible, identity.ClassNeedsDiscovery)
	}

	concrete, discovery := goldenCountInstanceClasses(t)
	if concrete == 0 || discovery == 0 {
		t.Fatalf("internal/live/check/testdata/identity-golden.txt renders %d CONCRETE and %d "+
			"NEEDS_DISCOVERY resource-level count instances; this pin needs both kinds in the tree, "+
			"since a page describing only one would then be right. Restore a fixture or say why in "+
			"the commit message.", concrete, discovery)
	}
	return named, fungible, concrete, discovery
}

func slotPresenceClassAt(t *testing.T, res *identity.Result, addr string) identity.Class {
	t.Helper()

	parsed, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		t.Fatalf("parsing %s: %s", addr, diags.Err())
	}
	r, ok := res.Get(parsed)
	if !ok {
		t.Fatalf("%s did not resolve at all; the fixture no longer declares it", addr)
	}
	return r.Class
}

// goldenCountInstanceClasses counts the identity golden's RESOURCE-level
// count instances by class: how many carry no slot (CONCRETE) against how
// many do (NEEDS_DISCOVERY). The instance key has to be at the end of the
// address, or a module-level count key ("module.app[1].aws_vpc.main")
// would be counted as a resource's own.
func goldenCountInstanceClasses(t *testing.T) (concrete, discovery int) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "internal", "live", "check", "testdata", "identity-golden.txt"))
	if err != nil {
		t.Fatalf("read internal/live/check/testdata/identity-golden.txt: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || !goldenResourceCountIndex.MatchString(fields[1]) {
			continue
		}
		switch fields[2] {
		case "CONCRETE":
			concrete++
		case "NEEDS_DISCOVERY":
			discovery++
		}
	}
	return concrete, discovery
}

var goldenResourceCountIndex = regexp.MustCompile(`\[[0-9]+\]$`)

// slotPresenceParagraphs makes a Go file chunk the way a markdown file
// does. [docChunks] splits on blank lines, and a Go doc comment has none -
// its paragraph breaks are lines holding "//" and nothing else - so without
// this, internal/live/doc.go is one chunk and a failure prints the whole
// package comment instead of the paragraph that is wrong. The line count is
// preserved, so the line a failure names is still the line to open.
func slotPresenceParagraphs(path string, raw []byte) []byte {
	if filepath.Ext(path) != ".go" {
		return raw
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "//" {
			lines[i] = ""
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// loadSlotPresenceFixture builds the smallest configuration that holds both
// kinds of count block at once: a fungible one, and one whose members the
// configuration names through the count.index shape lint admits.
func loadSlotPresenceFixture(t *testing.T) *configs.Config {
	t.Helper()

	dir := t.TempDir()
	src := `
resource "aws_eip" "pool" {
  count = 2

  domain = "vpc"
}

resource "aws_s3_bucket" "shard" {
  count = 2

  bucket = "shard-${count.index}"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing main.tf: %v", err)
	}

	parser := configs.NewParser(nil)
	rootCall := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	rootMod, diags := parser.LoadConfigDir(dir, rootCall)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(t.Context(), rootMod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config: %s", cfgDiags.Error())
	}
	return cfg
}
