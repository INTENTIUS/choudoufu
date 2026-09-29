// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// examples/cross-estate-dependency is the worked example for two estates that
// depend on one another (GitHub issue #1059): a network estate declaring
// aws_vpc.main, a service estate whose aws_vpc data source filters on that
// VPC's own tofu-estate/tofu-address marker pair, and one chant Op that
// applies the producer before the consumer. live/OUTPUTS.md is the decision
// it demonstrates, and internal/live/lifecycle's
// TestCrossEstateDataSourceAgainstFloci is the same estate pair proved
// against the emulator.
//
// The example's own `npm test` is the real guard - it regenerates all three
// forge trees and diffs them byte for byte, and it reads both roots' HCL
// through @cdktf/hcl2json. It needs node and an npm install, and this
// repository's Go CI has neither, so it does not run there. This file is the
// backstop that does, over the parts that matter most if they rot quietly:
//
//   - the coupling between the two roots, which is a pair of strings nothing
//     in the HCL language ties together. Rename aws_vpc.main in the producer
//     and every tool stays green until someone applies the consumer against a
//     real account and the data source finds no VPC;
//   - the two absences the example exists to demonstrate - no `output` block
//     in the producer, no terraform_remote_state anywhere - because an
//     absence is exactly what a later edit restores without noticing;
//   - the two estates being two, since sharing one estate across two roots is
//     mutual destruction: the sweep is estate-scoped rather than root-scoped,
//     so each root's plan would see the other's resources as
//     undeclared_tagged;
//   - and that the generator has been run since its inputs last changed, by
//     re-hashing those inputs against the stamp generate.ts writes.
//
// What it cannot see, stated so nobody reads a green run here as more than it
// is: with no node available it never regenerates, so it proves input state
// and correspondence, not equality. A workflow hand-edited after generation
// is invisible here; the example's own currency test is what catches that.
const crossEstateDir = "../examples/cross-estate-dependency"

var (
	crossEstateNetworkDir = filepath.Join(crossEstateDir, "terraform", "network")
	crossEstateServiceDir = filepath.Join(crossEstateDir, "terraform", "service")
)

// crossEstateForgeWorkflows maps a forge to where its generated tree lands and
// whether that tree is one file per Op. GitLab's Op generator emits every
// job into one file - its trigger is job-scoped rather than workflow-scoped,
// so there is nothing to split - which is why it is the one forge whose file
// count does not track the Op count.
var crossEstateForgeWorkflows = map[string]struct {
	dir     string
	perOp   bool
	oneFile string
}{
	"github":  {dir: filepath.Join("github", ".github", "workflows"), perOp: true},
	"forgejo": {dir: filepath.Join("forgejo", ".forgejo", "workflows"), perOp: true},
	"gitlab":  {dir: "gitlab", oneFile: "ops.gitlab-ci.yml"},
}

// readCrossEstate reads one file under the example, failing the test rather
// than returning an error: every path below is checked in, so a missing one is
// the finding.
func readCrossEstate(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{crossEstateDir}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// crossEstateOps is the Op names on disk, derived from src/*.op.ts rather than
// listed here, so adding an Op without regenerating fails the workflow-set
// check below instead of silently passing a hard-coded list.
func crossEstateOps(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(crossEstateDir, "src"))
	if err != nil {
		t.Fatalf("reading the example's src directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".op.ts") {
			names = append(names, strings.TrimSuffix(entry.Name(), ".op.ts"))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("the example declares no Ops at all, which cannot be right")
	}
	return names
}

// ---------------------------------------------------------------------------
// The coupling between the two roots
// ---------------------------------------------------------------------------

// crossEstateSidecarEstate pulls the `estate` value out of a root's
// estate.chdf.hcl. The sidecar's whole body is the live block's content with
// no wrapper, so the value is a top-level attribute.
func crossEstateSidecarEstate(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "estate.chdf.hcl"))
	if err != nil {
		t.Fatalf("reading %s/estate.chdf.hcl: %v", dir, err)
	}
	m := regexp.MustCompile(`(?m)^\s*estate\s*=\s*"([^"]+)"`).FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("%s/estate.chdf.hcl declares no estate", dir)
	}
	return m[1]
}

// crossEstateVarDefault reads one variable block's `default` out of a .tf
// file. Deliberately narrow: these are two fixtures this repository owns, each
// with one quoted default per variable, and a full HCL parse here would buy
// nothing the example's own hcl2json-based test does not already buy.
func crossEstateVarDefault(t *testing.T, tf, name string) string {
	t.Helper()
	block := regexp.MustCompile(`(?s)variable\s+"` + regexp.QuoteMeta(name) + `"\s*\{(.*?)\n\}`).FindStringSubmatch(tf)
	if block == nil {
		t.Fatalf("no variable %q in the configuration", name)
	}
	m := regexp.MustCompile(`(?m)^\s*default\s*=\s*"([^"]+)"`).FindStringSubmatch(block[1])
	if m == nil {
		t.Fatalf("variable %q has no quoted default", name)
	}
	return m[1]
}

func TestCrossEstateExampleConsumerNamesTheProducer(t *testing.T) {
	service := readCrossEstate(t, "terraform", "service", "main.tf")
	network := readCrossEstate(t, "terraform", "network", "main.tf")

	// The estate name in the consumer's filter is the producer's own estate.
	// Nothing in the language ties the two together; this is the tie.
	wantEstate := crossEstateSidecarEstate(t, crossEstateNetworkDir)
	if got := crossEstateVarDefault(t, service, "network_estate"); got != wantEstate {
		t.Errorf("the service root filters on tofu-estate=%q, but the network root's sidecar declares %q. "+
			"The consumer's data source would resolve nothing, and no tool would say so until an apply "+
			"against a real account.", got, wantEstate)
	}

	// The address in the consumer's filter is a block the producer declares.
	// tofu-address is the configuration address choudoufu stamps on the
	// resource, so renaming the producer's block changes it.
	addr := crossEstateVarDefault(t, service, "network_vpc_address")
	parts := strings.SplitN(addr, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("var.network_vpc_address is %q, which is not a <type>.<name> address", addr)
	}
	declared := regexp.MustCompile(`(?m)^resource\s+"` + regexp.QuoteMeta(parts[0]) + `"\s+"` + regexp.QuoteMeta(parts[1]) + `"`)
	if !declared.MatchString(network) {
		t.Errorf("the service root filters on tofu-address=%q, which the network root does not declare", addr)
	}

	// Both filters are on the marker pair, not on a naming convention
	// invented on top of it.
	for _, tag := range []string{"tag:tofu-estate", "tag:tofu-address"} {
		if !strings.Contains(service, `name   = "`+tag+`"`) {
			t.Errorf("the service root's data source does not filter on %q", tag)
		}
	}
	if !strings.Contains(service, "vpc_id     = data.aws_vpc.network.id") {
		t.Error("the service root's subnet does not take its vpc_id from the data source, so nothing in " +
			"this example actually carries a value across the estate boundary")
	}
}

func TestCrossEstateExampleEstatesAreTwo(t *testing.T) {
	network := crossEstateSidecarEstate(t, crossEstateNetworkDir)
	service := crossEstateSidecarEstate(t, crossEstateServiceDir)
	if network == service {
		t.Fatalf("both roots declare estate %q. An estate is the unit of ownership (live/MARKERS.md) and the "+
			"sweep is estate-scoped rather than root-scoped, so each root's plan would see the other's "+
			"resources as undeclared_tagged and propose destroying every one of them.", network)
	}
}

// crossEstateCode strips `#` and `//` comment lines from an HCL body. Both
// roots discuss terraform_remote_state, backends and outputs at length in
// their own comments - that prose is the example's teaching material, and a
// guard that matched it would fire on the explanation rather than on the
// thing explained.
func crossEstateCode(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestCrossEstateExampleProducerPublishesNothing(t *testing.T) {
	network := crossEstateCode(readCrossEstate(t, "terraform", "network", "main.tf"))
	if regexp.MustCompile(`(?m)^output\s+"`).MatchString(network) {
		t.Error("the producer grew an output block. The whole point of live/OUTPUTS.md's decision is that it " +
			"needs none: the consumer reads the live resource, so there is nothing to publish - and since a " +
			"live root writes no state file, nothing that could read a published value back.")
	}
	for _, root := range []struct{ name, body string }{
		{"network", network},
		{"service", crossEstateCode(readCrossEstate(t, "terraform", "service", "main.tf"))},
	} {
		if strings.Contains(root.body, "terraform_remote_state") {
			t.Errorf("the %s root reads terraform_remote_state. The lint rule that refused it was removed "+
				"under #179 stage 3, which makes this worse rather than better: a live producer writes no "+
				"state file, so a remote-state read that still resolves is reading a snapshot frozen at "+
				"migration time, with no marker on the file to say so (live/LIMITATIONS.md).", root.name)
		}
		for _, block := range []string{"backend", "cloud"} {
			if regexp.MustCompile(`(?m)^\s*` + block + `\s+"`).MatchString(root.body) {
				t.Errorf("the %s root declares a %s block; TF024 refuses one on a live root", root.name, block)
			}
		}
	}
	// A workspace is a state-file partition and a live root has no state file
	// to partition: TF025 refuses a non-default one. The roots are declared in
	// chant.config.ts, so that is where a workspace would be set.
	if regexp.MustCompile(`(?m)^\s*workspace\s*:`).MatchString(readCrossEstate(t, "chant.config.ts")) {
		t.Error("chant.config.ts sets a workspace on a root. TF025 refuses a non-default workspace on a live root, " +
			"and two estates are how this example separates two domains instead.")
	}
}

// ---------------------------------------------------------------------------
// The generated half
// ---------------------------------------------------------------------------

func TestCrossEstateExampleWorkflowsAreTrackedAndComplete(t *testing.T) {
	ops := crossEstateOps(t)

	for forge, layout := range crossEstateForgeWorkflows {
		dir := filepath.Join(crossEstateDir, layout.dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Errorf("%s: reading %s: %v", forge, dir, err)
			continue
		}
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		sort.Strings(names)

		if layout.perOp {
			var want []string
			for _, op := range ops {
				want = append(want, op+".yml")
			}
			if strings.Join(names, ",") != strings.Join(want, ",") {
				t.Errorf("%s holds %v, but the example declares Ops %v. An Op added or removed without "+
					"`npm run generate` leaves the committed tree describing a pipeline nobody has.",
					dir, names, ops)
			}
		} else {
			if strings.Join(names, ",") != layout.oneFile {
				t.Errorf("%s holds %v, want just %q", dir, names, layout.oneFile)
			}
			body := readCrossEstate(t, layout.dir, layout.oneFile)
			for _, op := range ops {
				if !strings.Contains(body, op) {
					t.Errorf("%s/%s has no job for Op %q", layout.dir, layout.oneFile, op)
				}
			}
		}

		for _, name := range names {
			body := readCrossEstate(t, layout.dir, name)
			if !strings.Contains(body, "DO NOT EDIT") {
				t.Errorf("%s/%s carries no generated banner", layout.dir, name)
			}
			if !strings.Contains(body, "CHANT_FORGE: "+forge) {
				t.Errorf("%s/%s does not set CHANT_FORGE to %q, so the Op a runner builds need not be the "+
					"Op this file was generated from", layout.dir, name, forge)
			}
			// A generated workflow is committed once and re-run unattended
			// against a role that can write to two estates, so the binary it
			// installs is pinned by version and by checksum.
			if !regexp.MustCompile(`choudoufu_v\d+\.\d+\.\d+_linux_amd64\.tar\.gz`).MatchString(body) {
				t.Errorf("%s/%s does not install a pinned choudoufu version", layout.dir, name)
			}
			if !regexp.MustCompile(`[0-9a-f]{64}\s+/tmp/choudoufu_`).MatchString(body) {
				t.Errorf("%s/%s does not verify the choudoufu download against a checksum", layout.dir, name)
			}
		}
	}

	// Tracked, not merely present: a generated file that exists on one machine
	// is not a deliverable.
	out, err := exec.Command("git", "-C", "..", "ls-files", "examples/cross-estate-dependency").Output()
	if err != nil {
		t.Skipf("git ls-files is unavailable here: %v", err)
	}
	tracked := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		tracked[line] = true
	}
	for forge, layout := range crossEstateForgeWorkflows {
		entries, err := os.ReadDir(filepath.Join(crossEstateDir, layout.dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			rel := filepath.ToSlash(filepath.Join("examples/cross-estate-dependency", layout.dir, entry.Name()))
			if !tracked[rel] {
				t.Errorf("%s: %s is not tracked by git", forge, rel)
			}
		}
	}
}

// TestCrossEstateExampleStampMatchesItsInputs re-hashes the generator inputs
// and compares them against the stamp generate.ts writes. This is the one
// currency question a machine with no node can answer: commit order alone
// cannot, because an input change that moves no output byte leaves nothing to
// commit and would read as stale forever.
func TestCrossEstateExampleStampMatchesItsInputs(t *testing.T) {
	var stamp struct {
		Inputs map[string]string `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(readCrossEstate(t, "generated-from.json")), &stamp); err != nil {
		t.Fatalf("parsing generated-from.json: %v", err)
	}
	if len(stamp.Inputs) == 0 {
		t.Fatal("generated-from.json records no inputs at all")
	}

	// The stamp's own list must be the list generate.ts reads, or the stamp
	// answers a narrower question than it claims to.
	gen := readCrossEstate(t, "generate.ts")
	m := regexp.MustCompile(`const GENERATOR_INPUTS = \[([^\]]*)\]`).FindStringSubmatch(gen)
	if m == nil {
		t.Fatal("generate.ts no longer declares GENERATOR_INPUTS, so this guard cannot check the stamp's scope")
	}
	declared := map[string]bool{}
	for _, raw := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		declared[raw[1]] = true
	}
	for path := range stamp.Inputs {
		top := strings.SplitN(path, "/", 2)[0]
		if !declared[top] && !declared[path] {
			t.Errorf("the stamp records %q, which is under no GENERATOR_INPUTS entry", path)
		}
	}
	// Every src/*.op.ts is an input, because every one of them changes what
	// the generator emits.
	for _, op := range crossEstateOps(t) {
		if _, ok := stamp.Inputs["src/"+op+".op.ts"]; !ok {
			t.Errorf("the stamp does not record src/%s.op.ts, so a change to that Op would not move it", op)
		}
	}

	for path, want := range stamp.Inputs {
		body, err := os.ReadFile(filepath.Join(crossEstateDir, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("the stamp records %s, which is not on disk: %v", path, err)
			continue
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != want {
			t.Errorf("%s has changed since the workflows were generated (stamp %s, on disk %s). "+
				"Run `npm run generate` in examples/cross-estate-dependency and commit the result.",
				path, want[:12], got[:12])
		}
	}
}
