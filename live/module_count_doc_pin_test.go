// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// This file is GitHub issue #656's documentation pin, and it is an EXTERNAL
// test package rather than package residue because internal/live/lint
// imports live (lint.go's residue alias). A file in package residue cannot
// import lint back, and lint is the only thing in the tree that measures the
// word the documents got wrong - "refused". The import direction chose the
// package, not style.
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

// Issue #656: three documents said a `count` key on a `module.` segment is a
// shape this fork never writes, and one of them - live/MARKERS.md - says of
// itself that it is "the only integration surface external tools rely on:
// this document is a contract". An external tool built to that contract would
// not parse `module.x[0]` out of a tofu-address, and this fork stamps exactly
// that: issue #195 admitted a statically-evaluable count on a module call,
// and issue #378 made the multi-instance case a marker rather than a refusal,
// written through tofu.marker_module_prefix.
//
// Four claim sites had drifted when this was written, on three pages:
// live/MARKERS.md's "Grammar vs. current builds" paragraph and its examples
// list, site/content/docs/model/identity.md's "refused permanently"
// paragraph, and two places on site/content/docs/use/compatibility.md. Those
// pages are owned by different people and drifted at different times, which
// is the argument for a pin over an edit: an edit fixes the four sentences
// that are wrong today, and a pin fails on the fifth.
//
// The oracle is the code, in three legs, none of which reads a document:
//
//   - identity.ChildModuleCountKeys enumerates a count = 2 module call into
//     IntKey(0) and IntKey(1). That function is what resolver.walkModule
//     recurses on, so its keys are literally the "[0]"/"[1]" segments a
//     tofu-address carries.
//   - lint.CheckContext files no RuleChildModule issue over the same
//     configuration. This is the leg that measures "refused", and it is the
//     reason this file is an external test package.
//   - internal/live/check/testdata/identity-golden.txt, the rendered output
//     of the real resolver over the in-repo fixtures, carries such addresses
//     - at index >= 1, which is the case #378 closed rather than the count =
//     1 case #195 opened.
//
// Only then is any document read. Every paragraph or bullet on the three
// pages that talks about a module-level count is required not to claim the
// construct is permanently refused.
//
// PROVING IT RED. Restore any one of the four original sentences - for
// instance "count on a module call is refused permanently" on identity.md,
// or "(spec-only: a count-expanded module is refused permanently, see
// above)" in MARKERS.md's examples list - and TestModuleCountDocsMatchTheCode
// fails naming the file, the line and the phrase. Deleting the claim
// altogether does not make it green either: each page must still carry at
// least one chunk on the subject.
//
// WHAT THIS DELIBERATELY DOES NOT COVER.
//
//   - The rest of live/MARKERS.md. The escaping rule, the EBNF, the
//     continuation tags and tofu-slot are not read here; this pin is about
//     one verdict.
//   - live/LIMITATIONS.md and CHANGELOG.md. LIMITATIONS.md's refusal
//     sections are generated and its "child-module" narrative is currently
//     correct on this point; CHANGELOG.md's line records what shipped in a
//     released version and was true when written. Neither is a live claim to
//     an operator about what runs today.
//   - Any claim about a for_each'd module call, about the hand-written
//     marker idiom (live/e2e/estate-module-keyed), or about which module
//     shapes live-mv still refuses. A page that gets those wrong passes
//     this test.
//   - The exact counts in the golden. Only "at least one, at index >= 1" is
//     required, so renaming or retiring a fixture does not fail here.
//     live/identity_golden_pin_test.go is what pins the counts.
//   - Whether the documents' positive statement is itself accurate. The
//     check is one-directional: it can catch a page saying the construct is
//     permanently refused, not a page describing the admission rule wrongly.

// moduleCountDocs are the pages that state a verdict on a module-level
// count to an operator. compatibility.md is on the list although this
// unit did not write its correction: a pin's value is that it covers a
// page its author does not own.
var moduleCountDocs = []string{
	"MARKERS.md",
	"../site/content/docs/model/identity.md",
	"../site/content/docs/use/compatibility.md",
}

// moduleCountTopic decides which chunks are about a module-level count.
// Every alternative requires the word "module" next to the word "count", or
// a rendered address carrying an integer key on a module segment, so a
// sentence about a RESOURCE's own count - of which these pages have several,
// all of them correct - is not swept in.
//
// Every gap is \s+ rather than a space, and that is not decoration: these
// files are hard-wrapped at 76 columns, so the phrase this pin was written
// for ("a `count`\ninstance key on a `module.` segment") is split across two
// lines in the source. A first draft using literal spaces missed two of the
// six drifted chunks and looked like a passing check on those two.
var moduleCountTopic = regexp.MustCompile(`(?i)` + strings.Join([]string{
	"`?count`?\\s+(?:on|for)\\s+a\\s+module\\s+(?:call|block)",
	`module\s*\{\s*` + "`?count",
	"`?count`?-expanded\\s+module",
	"`?count`?\\s+(?:instance\\s+)?key\\s+on\\s+a\\s+`?module",
	"module\\s+(?:block|call)\\s+expanded\\s+with\\s+`?count",
	`module\.[a-z0-9_-]+\[[0-9]+\]`,
}, "|"))

// permanentRefusalPhrases are the ways the four drifted sentences said the
// construct is refused and always will be. "permanent" alone is enough
// inside a chunk this topical: the pages use it in other bullets
// (random_password, terraform_remote_state) that moduleCountTopic does not
// match.
var permanentRefusalPhrases = []*regexp.Regexp{
	regexp.MustCompile(`(?i)permanent`),
	regexp.MustCompile(`(?i)no build ever produces`),
	regexp.MustCompile(`(?i)spec-only`),
	regexp.MustCompile(`(?i)no future work closes`),
	regexp.MustCompile(`(?i)refused outright`),
}

func TestModuleCountDocsMatchTheCode(t *testing.T) {
	keys, produced := moduleCountOracle(t)

	for _, path := range moduleCountDocs {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		topical := 0
		for _, chunk := range docChunks(raw) {
			if !moduleCountTopic.MatchString(chunk.text) {
				continue
			}
			topical++
			for _, phrase := range permanentRefusalPhrases {
				hit := phrase.FindString(chunk.text)
				if hit == "" {
					continue
				}
				t.Errorf("%s:%d says a module-level count is permanently refused (%q), and it is not.\n"+
					"identity.ChildModuleCountKeys expands count = 2 on a module call to %v with no "+
					"diagnostic, lint files no %s issue over it, and %d addresses in "+
					"internal/live/check/testdata/identity-golden.txt carry an integer key on a module "+
					"segment. Issue #195 admitted the call; issue #378 made the multi-instance case a "+
					"marker written through tofu.marker_module_prefix rather than a refusal. Restate the "+
					"sentence as what is actually refused - a count expression this fork cannot evaluate "+
					"before a provider runs, or a module call using count.index in a shape lint cannot "+
					"prove distinct per instance - rather than the construct.\n\nThe chunk:\n%s",
					path, chunk.line, hit, keys, lint.RuleChildModule, produced, chunk.text)
			}
		}
		if topical == 0 {
			t.Errorf("%s no longer says anything about a module-level count.\n"+
				"This pin exists because that claim was wrong on this page and could go wrong again, "+
				"so a page that drops the subject entirely makes it vacuous. Either restore the "+
				"guidance or, if the subject genuinely moved off this page, remove the path from "+
				"moduleCountDocs here and say in the commit message where it went.", path)
		}
	}
}

// moduleCountOracle is the code side of the claim, and it answers before any
// document is opened. It returns the instance keys the resolver expands a
// count = 2 module call into, and how many addresses in the identity golden
// carry an integer key on a module segment at index >= 1.
//
// It fails rather than adapting if the code stops producing these addresses.
// A pin whose expectation follows the code it pins proves nothing, and this
// one has a direction: the documents were wrong in the "refused" direction,
// so the day the answer flips back is the day a human has to rewrite both
// this file and the pages, deliberately.
func moduleCountOracle(t *testing.T) ([]addrs.InstanceKey, int) {
	t.Helper()

	cfg := loadModuleCountFixture(t)

	keys, diag := identity.ChildModuleCountKeys(t.Context(), cfg.Module, `module "counted"`, cfg.Module.ModuleCalls["counted"].Count)
	if diag != nil {
		t.Fatalf("identity.ChildModuleCountKeys refused count = 2 on a module call: %s\n"+
			"This fork's answer to a module-level count has changed. The documents this test pins "+
			"describe it as produced (issues #195 and #378); rewrite this test and them together, "+
			"rather than deleting either.", diag.Detail)
	}
	if len(keys) != 2 || keys[0] != addrs.IntKey(0) || keys[1] != addrs.IntKey(1) {
		t.Fatalf("identity.ChildModuleCountKeys expanded count = 2 to %v, want [0 1]; "+
			"the module-instance keys that become tofu-address segments are not what this pin assumes", keys)
	}

	for _, issue := range lint.CheckContext(t.Context(), cfg) {
		if issue.Rule == lint.RuleChildModule {
			t.Fatalf("lint refuses a count = 2 module call with %s: %s\n"+
				"The construct is refused again, so the pages this test holds to the code may be right "+
				"and this test is now wrong. Rewrite both deliberately.", issue.Rule, issue.Detail)
		}
	}

	produced := goldenModuleCountAddresses(t)
	if produced == 0 {
		t.Fatal("internal/live/check/testdata/identity-golden.txt renders no address with an integer key " +
			"on a module segment at index >= 1. The fixtures that carried them (for instance " +
			"internal/live/discovery/testdata/count-module-walk) have gone, so the strongest evidence " +
			"behind this pin is no longer in the tree; restore a fixture or say why in the commit message.")
	}
	return keys, produced
}

// goldenAddressColumn is the second tab-separated column of the identity
// golden, which is the resolved address. Reading the column rather than the
// whole line keeps an identity VALUE that happens to look like an address
// out of the count.
var goldenAddressColumn = regexp.MustCompile(`(?m)^[^\t\n]*\t([^\t\n]*)`)

// goldenModuleCountIndex matches an integer instance key at index >= 1 on a
// module segment. Index 0 is deliberately excluded: count = 1 is the case
// issue #195 opened and stamping has addressed since, while an index above
// zero can only come from a call with several instances sharing one body,
// which is the case issue #378 closed and the one MARKERS.md called
// impossible.
var goldenModuleCountIndex = regexp.MustCompile(`module\.[A-Za-z0-9_-]+\[[1-9][0-9]*\]`)

func goldenModuleCountAddresses(t *testing.T) int {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "internal", "live", "check", "testdata", "identity-golden.txt"))
	if err != nil {
		t.Fatalf("read internal/live/check/testdata/identity-golden.txt: %v", err)
	}
	n := 0
	for _, m := range goldenAddressColumn.FindAllSubmatch(raw, -1) {
		if goldenModuleCountIndex.Match(m[1]) {
			n++
		}
	}
	return n
}

// loadModuleCountFixture builds the smallest configuration that puts the
// question: one module call expanded with a literal count of 2, and one
// taggable resource inside it. The tree is loaded through one configs.Parser
// for root and child, the way internal/live/check.Load does it, because two
// instances of one call sharing a parsed body is the whole difficulty #378
// answered.
func loadModuleCountFixture(t *testing.T) *configs.Config {
	t.Helper()

	dir := t.TempDir()
	files := map[string]string{
		"main.tf": `
module "counted" {
  source = "./child"
  count  = 2
}
`,
		"child/main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
`,
	}
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
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
			childDir := filepath.Join(req.Parent.Module.SourceDir, req.SourceAddr.String())
			mod, modDiags := parser.LoadConfigDir(childDir, req.Call)
			return mod, nil, modDiags
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config: %s", cfgDiags.Error())
	}
	return cfg
}

// docChunk is one paragraph or one list item, with the line it starts on so
// a failure names a place an editor can go to.
type docChunk struct {
	line int
	text string
}

var bulletStart = regexp.MustCompile(`^\s*(?:[-*+]|[0-9]+\.)\s`)

// docChunks splits a markdown file at blank lines and at the start of every
// list item. Attribution matters here: "Permanent." sits in a bullet list
// whose other bullets are permanent for good reasons, and a whole-file grep
// would either miss the topic or convict the neighbours.
func docChunks(raw []byte) []docChunk {
	var out []docChunk
	var cur []string
	start := 0

	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, docChunk{line: start, text: strings.Join(cur, "\n")})
		cur = nil
	}

	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if bulletStart.MatchString(line) {
			flush()
		}
		if len(cur) == 0 {
			start = i + 1
		}
		cur = append(cur, line)
	}
	flush()
	return out
}
