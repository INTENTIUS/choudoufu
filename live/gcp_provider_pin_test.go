// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The gcp lane's estates require hashicorp/google exactly at
// live/oracle-versions.json's google_provider_version, through
// gauntlet_required_provider, so two runs of one commit measure one
// provider. A GCP root anywhere else that inits against a bare or other
// requirement measures whatever the registry served that day. This test
// holds every GCP root in the places that write one to the same pin:
//
//   - a .tf, .tf.json or .tofu file under live/e2e (the limits fixtures
//     included), live/smoke, examples and site/src: every hashicorp/google
//     or google-beta requirement names exactly the pin;
//   - an hcl code block in a Markdown page under live, examples or site/src:
//     the same, so a snippet a reader copies is the version the lane runs;
//   - a shell script under live/smoke or live/e2e that writes a google_*
//     resource or a provider "google" block: it writes its requirement
//     through a helper that reads the pin (gcp_write_root,
//     gcp_required_provider, or gauntlet_required_provider's
//     GOOGLE_REQUIRED_PROVIDER), and any literal hashicorp/google source it
//     prints carries the pin or the helper's printf placeholder.

// googleSource finds one hashicorp/google or google-beta source argument,
// in HCL or JSON spelling.
var googleSource = regexp.MustCompile(`"?source"?\s*[=:]\s*"(?:registry\.(?:terraform|opentofu)\.io/)?hashicorp/google(?:-beta)?"`)

// enclosingBlock is the text of the innermost {...} around index i.
func enclosingBlock(s string, i int) string {
	open := strings.LastIndex(s[:i], "{")
	if open < 0 {
		return s
	}
	end := strings.Index(s[i:], "}")
	if end < 0 {
		return s[open:]
	}
	return s[open : i+end+1]
}

func googlePin(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("oracle-versions.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Google string `json:"google_provider_version"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Google == "" {
		t.Fatalf("live/oracle-versions.json has no google_provider_version (%v)", err)
	}
	return v.Google
}

// unpinnedGoogle reports each hashicorp/google requirement in text whose
// block does not name pin exactly. placeholder, when set, is a printf verb
// a helper substitutes the pin for.
func unpinnedGoogle(text, pin string, placeholder bool) []string {
	want := `"?version"?\s*[=:]\s*"(?:=\s*)?` + regexp.QuoteMeta(pin) + `"`
	if placeholder {
		want = `"?version"?\s*[=:]\s*"(?:=\s*)?(?:` + regexp.QuoteMeta(pin) + `|%s)"`
	}
	ok := regexp.MustCompile(want)
	var bad []string
	for _, loc := range googleSource.FindAllStringIndex(text, -1) {
		if block := enclosingBlock(text, loc[0]); !ok.MatchString(block) {
			line := 1 + strings.Count(text[:loc[0]], "\n")
			bad = append(bad, strings.TrimSpace(strings.SplitN(text[loc[0]:], "\n", 2)[0])+" at line "+itoa(line))
		}
	}
	return bad
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

var hclFence = regexp.MustCompile("(?s)```(?:hcl|terraform|tf)\\n(.*?)```")

// gcpScript is a script about google roots: it writes a google_* resource
// or a google provider block. writesRoot is one that writes a root's
// resources or its requirements, rather than only a helper's provider
// block (live/e2e/lib/gcp.sh's gcp_provider_block, whose callers write the
// requirement).
var (
	gcpScript  = regexp.MustCompile(`resource "google_|provider "google(-beta)?"`)
	writesRoot = regexp.MustCompile(`resource "google_|required_providers`)
)

// pinningHelpers are the ways a script may write the google requirement,
// each of which reads live/oracle-versions.json.
var pinningHelpers = []string{"gcp_write_root", "gcp_required_provider", "GOOGLE_REQUIRED_PROVIDER"}

// gcpPinProblems walks the four places and returns one line per root that
// declares hashicorp/google without the pin.
func gcpPinProblems(t *testing.T, root, pin string) []string {
	t.Helper()
	var problems []string
	walk := func(dir string, visit func(path, text string)) {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			return
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".terraform") {
				return filepath.SkipDir
			}
			if d.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			visit(rel, string(raw))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"live/e2e", "live/smoke", "examples", "site/src", "live"} {
		walk(dir, func(path, text string) {
			switch {
			case strings.HasSuffix(path, ".tf") || strings.HasSuffix(path, ".tf.json") || strings.HasSuffix(path, ".tofu"):
				if dir == "live" {
					return // the subdirectories above cover every root under live
				}
				for _, b := range unpinnedGoogle(text, pin, false) {
					problems = append(problems, path+": "+b)
				}
			case strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".mdx"):
				if dir == "live" && strings.Count(path, string(filepath.Separator)) > 1 && !strings.HasPrefix(path, filepath.Join("live", "gcp")) {
					return
				}
				for _, m := range hclFence.FindAllStringSubmatch(text, -1) {
					for _, b := range unpinnedGoogle(m[1], pin, false) {
						problems = append(problems, path+" (hcl block): "+b)
					}
				}
			case strings.HasSuffix(path, ".sh") && (dir == "live/e2e" || dir == "live/smoke"):
				for _, b := range unpinnedGoogle(text, pin, true) {
					problems = append(problems, path+": "+b)
				}
				if gcpScript.MatchString(text) && writesRoot.MatchString(text) {
					helped := false
					for _, h := range pinningHelpers {
						helped = helped || strings.Contains(text, h)
					}
					if !helped {
						problems = append(problems, path+": writes a google root without gcp_write_root, gcp_required_provider or GOOGLE_REQUIRED_PROVIDER, so its hashicorp/google requirement is not the pin")
					}
				}
			}
		})
	}
	return problems
}

func TestEveryGCPRootPinsTheGoogleProvider(t *testing.T) {
	pin := googlePin(t)
	for _, p := range gcpPinProblems(t, "..", pin) {
		t.Error(p)
	}
	// The helpers themselves read the pin from oracle-versions.json, so a
	// script that calls one is pinned by construction.
	for file, helper := range map[string]string{
		"smoke/lib.sh":        "gcp_required_provider() {",
		"e2e/lib/gauntlet.sh": "gauntlet_required_provider() {",
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		i := strings.Index(s, helper)
		if i < 0 {
			t.Errorf("%s: %s is gone", file, helper)
			continue
		}
		body := s[i:]
		if j := strings.Index(body, "\n}\n"); j > 0 {
			body = body[:j]
		}
		if !strings.Contains(body, "oracle-versions.json") && !strings.Contains(body, "gauntlet_oracle_pin") {
			t.Errorf("%s: %s does not read the pin from live/oracle-versions.json", file, helper)
		}
	}
	raw, err := os.ReadFile("e2e/reference-gcp/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "gauntlet_required_provider google hashicorp/google google_provider_version") {
		t.Error("reference-gcp no longer reads GOOGLE_REQUIRED_PROVIDER through gauntlet_required_provider's google_provider_version")
	}
}

// The check sees what it is for: a bare requirement, another version, a
// JSON root, a Markdown snippet and a script writing its own literal root
// are each reported, and the pinned spellings are not. Red against a
// checker that only read .tf files.
func TestGCPPinCheckSeesEveryUnpinnedShape(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	block := func(v string) string {
		return "terraform {\n  required_providers {\n    google = {\n      source  = \"hashicorp/google\"\n" + v + "    }\n  }\n}\n"
	}
	write("live/e2e/limits/ok/main.tf", block("      version = \"8.6.0\"\n"))
	write("live/e2e/limits/ok-eq/main.tf", block("      version = \"= 8.6.0\"\n"))
	write("live/e2e/limits/bare/main.tf", block(""))
	write("live/e2e/limits/other/main.tf", block("      version = \"~> 8.0\"\n"))
	write("examples/gcp/main.tf.json", `{"terraform":{"required_providers":{"google":{"source":"hashicorp/google","version":"8.5.0"}}}}`)
	write("site/src/content/docs/x.mdx", "```hcl\n"+block("")+"```\n")
	write("live/smoke/drafts/gcp-hand.sh", "cat > main.tf <<TF\n"+block("")+"resource \"google_pubsub_topic\" \"t\" {}\nTF\n")
	write("live/smoke/drafts/gcp-helped.sh", "gcp_write_root \"$W\" e\ncat > \"$W/main.tf\" <<TF\nresource \"google_pubsub_topic\" \"t\" {}\nTF\n")
	write("live/smoke/lib.sh", "printf '    google = {\\n      source  = \"hashicorp/google\"\\n      version = \"= %s\"\\n    }\\n' \"$pin\"\n")
	got := strings.Join(gcpPinProblems(t, root, "8.6.0"), "\n")
	for _, want := range []string{"limits/bare/main.tf", "limits/other/main.tf", "examples/gcp/main.tf.json", "x.mdx (hcl block)", "gcp-hand.sh: source", "gcp-hand.sh: writes a google root"} {
		if !strings.Contains(got, want) {
			t.Errorf("the check missed %s:\n%s", want, got)
		}
	}
	for _, clean := range []string{"limits/ok/", "limits/ok-eq/", "gcp-helped.sh", "smoke/lib.sh"} {
		if strings.Contains(got, clean) {
			t.Errorf("the check reported the pinned %s:\n%s", clean, got)
		}
	}
}
