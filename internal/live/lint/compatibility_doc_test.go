// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markerkey"
)

// site/content/docs/use/compatibility.md is the page a prospect reads to
// decide whether their configuration will run here, and until this file
// existed nothing checked a word of it.
//
// On 2026-08-30 twenty-one claims on it were checked against source. Twenty
// were stale, and every one of the twenty was stale in the same direction:
// it described a refusal the code had stopped making, telling a reader their
// configuration would be rejected when it would not. Eight of them had been
// fixed once before and lost to a tree reset, which is part of how the page
// got to twenty; the rest had simply never been checked by anything.
//
// live/LIMITATIONS.md does not drift, because four tests fail when it does.
// live/readiness.json does not drift, because it is generated. This page is
// hand-written prose about the same facts, and prose is not a thing a
// generator can produce here - the page's job is to explain, not to
// enumerate. So it is pinned instead, at the two seams where a claim can be
// reduced to something a machine can check:
//
//   - [TestCompatibilityDocMarkerKeyCharsMatchTheRule] holds the character
//     sets the page prints against the constants that enforce them. This is
//     the seam the old page failed at: it printed "+ - = _ / @" while
//     markerkey.Extras had said "+-=_/@.:" since issue #178.
//   - [TestCompatibilityDocAdmittedConstructsAreAdmitted] runs the linter
//     over one configuration per construct the page says is admitted, and
//     refuses to let the page claim an admission the linter does not make -
//     or, in the one row that goes the other way, a refusal it does not.
//     This is the seam all six "refused outright" entries failed at.
//
// Neither guard can prove a paragraph of prose correct. What they can do is
// fail when the specific, checkable claim underneath the prose stops being
// true, which is what happened twenty times and what nothing noticed.

// compatibilityDocPath is the page, relative to this package's directory.
// Go runs a test with its own package directory as the working directory,
// so this needs no repository-root discovery and no os.Getwd.
const compatibilityDocPath = "../../../site/content/docs/use/compatibility.md"

func readCompatibilityDoc(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.FromSlash(compatibilityDocPath))
	if err != nil {
		t.Fatalf("reading the compatibility page: %s", err)
	}
	return string(body)
}

// markerKeySpan matches one <!-- markerkey:NAME --> ... <!-- /markerkey:NAME -->
// region. The page carries the "extras" span twice on purpose - once for the
// characters that need no escaping, and once for the narrower set issue #227
// falls back to - and this test requires every occurrence to agree, so a
// half-updated page fails rather than passing on the copy that happened to be
// checked.
var markerKeySpan = regexp.MustCompile(`(?s)<!-- markerkey:([a-z]+) -->(.*?)<!-- /markerkey:[a-z]+ -->`)

// backtickedRune matches a single rune wrapped in backticks, which is how the
// page prints one character of a set.
var backtickedRune = regexp.MustCompile("`(.)`")

// TestCompatibilityDocMarkerKeyCharsMatchTheRule holds the for_each key
// character sets the page prints against [markerkey.Extras] and
// [markerkey.Excluded], the constants both enforcement points read.
//
// Proving it red: revert the page's "for_each keys" section to its
// pre-2026-08-30 wording (letters, numbers, space and "+ - = _ / @", with "."
// and ":" described as excluded) and this fails on both sets at once - the
// extras span is missing "." and ":", and there is no excluded span at all.
// Changing either constant without editing the page fails it the same way.
func TestCompatibilityDocMarkerKeyCharsMatchTheRule(t *testing.T) {
	doc := readCompatibilityDoc(t)

	want := map[string]string{
		"extras":   markerkey.Extras,
		"excluded": markerkey.Excluded,
	}

	seen := map[string]int{}
	for _, m := range markerKeySpan.FindAllStringSubmatch(doc, -1) {
		name, span := m[1], m[2]
		wantSet, known := want[name]
		if !known {
			t.Errorf("the page marks a span %q that this test does not know about; add it to want or fix the spelling", name)
			continue
		}
		seen[name]++

		var got []string
		for _, r := range backtickedRune.FindAllStringSubmatch(span, -1) {
			got = append(got, r[1])
		}
		if diff := runeSetDiff(strings.Join(got, ""), wantSet); diff != "" {
			t.Errorf("markerkey:%s span %d in the page disagrees with the constant:\n%s", name, seen[name], diff)
		}
	}

	for name := range want {
		if seen[name] == 0 {
			t.Errorf("the page carries no <!-- markerkey:%s --> span; the character set it states is unchecked", name)
		}
	}
}

// runeSetDiff compares two strings as sets of runes and renders the
// difference, or "" when they hold the same runes. Order and repetition are
// deliberately ignored: the page groups these characters for reading, and
// the constants group them for the code, and neither ordering is a claim.
func runeSetDiff(got, want string) string {
	gotSet := map[rune]bool{}
	for _, r := range got {
		gotSet[r] = true
	}
	wantSet := map[rune]bool{}
	for _, r := range want {
		wantSet[r] = true
	}

	var missing, extra []string
	for r := range wantSet {
		if !gotSet[r] {
			missing = append(missing, string(r))
		}
	}
	for r := range gotSet {
		if !wantSet[r] {
			extra = append(extra, string(r))
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	sort.Strings(missing)
	sort.Strings(extra)

	var b strings.Builder
	b.WriteString("  page states: " + strconv.Quote(got) + "\n")
	b.WriteString("  constant is: " + strconv.Quote(want) + "\n")
	if len(missing) > 0 {
		b.WriteString("  the page is missing: " + strings.Join(missing, " ") + "\n")
	}
	if len(extra) > 0 {
		b.WriteString("  the page adds, and the constant does not have: " + strings.Join(extra, " ") + "\n")
	}
	return b.String()
}

// constructCase is one bullet in the page's "Constructs this page used to
// refuse, and no longer does" section, paired with a configuration that
// contains exactly that construct.
//
// bullet must be the bold label the page opens the bullet with, with runs of
// whitespace collapsed to single spaces because the page hard-wraps and a
// label may span two lines. That is the binding:
// [TestCompatibilityDocAdmittedConstructsAreAdmitted] compares the set of
// labels on the page against the set of cases here, so a bullet added to the
// page with no case fails, and a case whose bullet has been reworded or
// deleted fails too. Without it this would be an ordinary lint test that the
// page could drift away from freely.
type constructCase struct {
	bullet string
	// config is a whole configuration, live block included. Every case
	// declares one, because a live block implies a local record store
	// (internal/configs.impliedRecordStore, issue #364) and that implication
	// is what admits several of these - a case written without one would
	// measure the unadopted-configuration path instead, which is not what the
	// page's reader is asking about.
	config string
	// refused inverts the assertion for the one construct the section names
	// as still refused. It is what stops this test from passing by way of a
	// linter that admits everything: if the gate it guards were removed,
	// this case fails while every other case still passes.
	refused bool
	// rule, when set, narrows the assertion to one Rule. Empty means no
	// fatal issue at all may be raised.
	rule Rule
}

const compatLiveBlock = `
terraform {
  live {
    estate = "compat-doc"
  }
}
`

// compatibilityConstructCases is one case per bullet in the page's section.
// The configurations are the smallest thing that carries the construct; none
// of them is trying to be a realistic estate.
var compatibilityConstructCases = []constructCase{
	{
		bullet: "`provisioner \"local-exec\"`, `\"remote-exec\"` and `\"file\"`, and resource-level `connection` blocks.",
		rule:   RuleProvisioner,
		config: compatLiveBlock + `
resource "aws_instance" "web" {
  ami           = "ami-1234"
  instance_type = "t3.micro"

  connection {
    type = "ssh"
    host = "example.invalid"
  }

  provisioner "local-exec" {
    command = "echo hello"
  }

  provisioner "remote-exec" {
    inline = ["echo hello"]
  }

  provisioner "file" {
    source      = "conf/app.conf"
    destination = "/etc/app.conf"
  }
}
`,
	},
	{
		bullet: "`data \"terraform_remote_state\"`.",
		config: compatLiveBlock + `
data "terraform_remote_state" "net" {
  backend = "local"

  config = {
    path = "../net/terraform.tfstate"
  }
}

resource "aws_s3_bucket" "app" {
  bucket = "compat-doc-app"
}
`,
	},
	{
		bullet: "`moved` blocks.",
		config: compatLiveBlock + `
resource "aws_s3_bucket" "new" {
  bucket = "compat-doc-moved"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
`,
	},
	{
		bullet: "`random_password`, `random_bytes` and the `tls_*` family.",
		rule:   RuleLogicalResource,
		config: compatLiveBlock + `
resource "random_password" "db" {
  length = 32
}

resource "random_bytes" "salt" {
  length = 16
}

resource "tls_private_key" "signing" {
  algorithm = "RSA"
}
`,
	},
	{
		bullet: "`local_file` and `local_sensitive_file`.",
		rule:   RuleLogicalResource,
		config: compatLiveBlock + `
resource "local_file" "note" {
  filename = "note.txt"
  content  = "hello"
}

resource "local_sensitive_file" "secret" {
  filename = "secret.txt"
  content  = "shhh"
}
`,
	},
	{
		bullet: "`module { count = ... }`.",
		rule:   RuleChildModule,
		config: compatLiveBlock + `
variable "sites" {
  type    = number
  default = 3
}

module "site" {
  source = "./mod"
  count  = var.sites
  name   = "fixed"
}
`,
	},
}

// compatibilityRefusedCase is the section's closing sentence, which names the
// one construct that genuinely is refused outright. It is asserted the
// opposite way round from every case above, so a linter that stopped
// refusing anything fails here rather than turning this whole file green.
// It is the child-side `configuration_aliases` shape specifically. The
// parent-side one, `providers = { aws = aws.useast1 }`, was refused until
// issue #188 taught internal/live/providerscope to walk the mapping, and is
// admitted now - which is why this case names the other one. Getting that
// backwards is how the page was wrong before this file existed.
var compatibilityRefusedCase = constructCase{
	bullet:  "child-side `providers` mapping",
	refused: true,
	rule:    RuleModuleProviders,
	config: compatLiveBlock + `
provider "aws" {
  region = "us-east-1"
}

module "site" {
  source = "./alias"

  providers = {
    aws.primary = aws
  }
}
`,
}

// compatChildModule is the child a module case calls. It declares a resource
// so the call is not empty and a variable so the call's own argument has
// somewhere to land.
const compatChildModule = `
variable "name" {
  type = string
}

resource "aws_s3_bucket" "child" {
  bucket = var.name
}
`

// compatAliasModule is the child [compatibilityRefusedCase] calls: one that
// declares a configuration alias and uses it, which is the whole construct.
const compatAliasModule = `
terraform {
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      configuration_aliases = [aws.primary]
    }
  }
}

resource "aws_s3_bucket" "child" {
  provider = aws.primary
  bucket   = "compat-doc-alias"
}
`

// TestCompatibilityDocAdmittedConstructsAreAdmitted runs the linter over one
// configuration per construct the page's "Constructs this page used to
// refuse" section lists, and fails if the linter refuses what the page says
// it admits.
//
// Proving it red, two ways, both tried:
//
//   - Set refused: true on any of the six admitted cases and it fails,
//     reporting that the construct was admitted. That is the substantive
//     proof: it is the assertion the OLD page made about all six, and the
//     linter contradicts every one of them.
//   - Add a bullet to the page's section, or reword one, without touching
//     this file, and the label comparison fails naming the bullet it could
//     not pair.
func TestCompatibilityDocAdmittedConstructsAreAdmitted(t *testing.T) {
	doc := readCompatibilityDoc(t)

	section := compatibilitySection(t, doc, "## Constructs this page used to refuse, and no longer does")
	if !strings.Contains(section, compatibilityRefusedCase.bullet) {
		t.Errorf("the section no longer names %q as the construct that IS refused; the one case holding this test honest has nothing to bind to",
			compatibilityRefusedCase.bullet)
	}

	wantBullets := map[string]bool{}
	for _, m := range compatBulletLabel.FindAllStringSubmatch(section, -1) {
		wantBullets[collapseSpace(m[1])] = false
	}
	if len(wantBullets) == 0 {
		t.Fatalf("no bulleted constructs found in the section; the page's shape changed and this test would otherwise pass by measuring nothing")
	}

	cases := append([]constructCase(nil), compatibilityConstructCases...)
	for _, tc := range cases {
		label := collapseSpace(tc.bullet)
		if _, known := wantBullets[label]; !known {
			t.Errorf("case %q pairs with no bullet in the page's section; the page was reworded and this test was not", label)
			continue
		}
		wantBullets[label] = true
	}
	for bullet, paired := range wantBullets {
		if !paired {
			t.Errorf("the page's section claims %q is admitted, and no case here proves it", bullet)
		}
	}

	for _, tc := range append(cases, compatibilityRefusedCase) {
		t.Run(compatCaseName(tc.bullet), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tc.config), 0o600); err != nil {
				t.Fatalf("writing the fixture: %s", err)
			}
			for name, body := range map[string]string{"mod": compatChildModule, "alias": compatAliasModule} {
				if !strings.Contains(tc.config, `"./`+name+`"`) {
					continue
				}
				child := filepath.Join(dir, name)
				if err := os.MkdirAll(child, 0o755); err != nil {
					t.Fatalf("making the child module: %s", err)
				}
				if err := os.WriteFile(filepath.Join(child, "main.tf"), []byte(body), 0o600); err != nil {
					t.Fatalf("writing the child module: %s", err)
				}
			}

			var got []Issue
			for _, issue := range CheckContext(t.Context(), loadConfigDir(t, dir)) {
				if tc.rule != "" && issue.Rule != tc.rule {
					continue
				}
				// A warning-severity rule does not stop a run, so it is not a
				// refusal and the page is not claiming anything about it.
				if issue.Rule.Severity() != SeverityError {
					continue
				}
				got = append(got, issue)
			}

			if tc.refused {
				if len(got) == 0 {
					t.Fatalf("the page names this construct as refused, and the linter admitted it")
				}
				return
			}
			if len(got) > 0 {
				for _, issue := range got {
					t.Errorf("the page says this is admitted, and the linter refused it: %s: %s\n%s", issue.Rule, issue.Construct, issue.Detail)
				}
			}
		})
	}
}

// compatibilitySection returns the page's text from heading through to the
// next heading at the same level.
func compatibilitySection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("the page has no %q heading; it was renamed and this test was not updated", heading)
	}
	rest := doc[start+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// compatBulletLabel matches one "- **label**" bullet, where the label may
// span lines because the page hard-wraps. It is non-greedy so two bullets in
// one section do not collapse into a single match.
var compatBulletLabel = regexp.MustCompile(`(?s)\n- \*\*(.*?)\*\*`)

// collapseSpace reduces every run of whitespace to a single space, so a label
// the page happens to wrap compares equal to the same label written on one
// line in a case above.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// compatCaseName renders a subtest name from a bullet label.
func compatCaseName(bullet string) string {
	name := strings.NewReplacer("*", "", "`", "", `"`, "", "\n", " ", " ", "_").Replace(collapseSpace(bullet))
	if len(name) > 60 {
		name = name[:60]
	}
	return strings.Trim(name, "_")
}
