// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// This is P2.1's live half, and like P2.4's it runs the built binary rather
// than the functions underneath it: the claim is about what an operator sees
// in a plan and about what a later run can then find, and both of those are
// properties of the command.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/stamp/ -run TestStampAgainstFloci -v
//
// The shape is the whole point of marker stamping, in one run:
//
//  1. Two resources are created by stock terraform from a configuration with
//     no markers anywhere - the world as it is before anyone has heard of
//     stateless mode. The state file is then deleted.
//  2. live-plan -estate=stamp-e2e proposes the markers, and adopts neither
//     resource. Both are unmarked, so neither is this estate's yet: the plan
//     proposes creating both, carrying the markers, and reports both live
//     resources as ones nobody here owns.
//  3. Those exact tags are written with the AWS CLI, which is the adoption
//     step: a tag write a human performs deliberately, on the two resources
//     they mean to bring in.
//  4. The same command run again finds both by their markers - the bucket by
//     name and the VPC by tag - and plans clean.
//
// Step 2 asserted the opposite until the audit: the bucket, being readable by
// name alone, was read into prior state and its markers arrived as an in-place
// update. That is finding C1 in demo form. Naming an existing resource is not
// owning it, and the migration story is now the same for both admission
// paths: unmarked means unowned, adoption is the tag write in step 3.
//
// It is gated because it needs Docker, terraform, the AWS CLI and a few
// minutes.

// flociPort is this run's emulator port, chosen by the kernel when a test
// starts its container. Each floci test here assigns it before any helper
// reads it; the tests run sequentially, so the later assignment cannot race
// the earlier test's use.
var flociPort string

const (
	awsRegion = "us-east-1"

	// stampEstate is the estate this run stamps for. It exists nowhere in the
	// configuration: the whole test is about a configuration that names no
	// estate at all.
	stampEstate = "stamp-e2e"

	// terraformBin stands the resources up. Stock terraform on purpose: what
	// is being stamped has to be something stateless mode did not create.
	terraformBin = "terraform"
)

func TestStampAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "marker-stamping")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort = flocitest.StartFloci(t, "cdf-p21")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	// One provider unpack for the whole machine, shared by terraform and tofu.
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := writeMarkerlessFixture(t)

	// Stock terraform creates them; tofu init is mandatory and not a
	// formality, because terraform populates registry.terraform.io provider
	// paths and tofu resolves registry.opentofu.org (P1.5's note).
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	// No destroy on the way out: the cloud these resources live in is the
	// container, and the container is removed. A terraform destroy here would
	// also fail on the lock file tofu init rewrites.
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	// --- The plan that proposes the markers ------------------------------
	first := statelessPlan(t, tofuBin, dir, "-estate="+stampEstate)

	add, change, destroy, ok := flocitest.PlanSummary(first)
	if !ok {
		t.Fatalf("no plan summary in the output:\n%s", first)
	}
	t.Logf("first plan: %d to add, %d to change, %d to destroy", add, change, destroy)
	if destroy != 0 {
		t.Errorf("stamping proposed %d destroy(s):\n%s", destroy, first)
	}

	// The bucket: readable by the name in the configuration, and carrying no
	// marker, so it is not this estate's. It is left alone and the plan
	// proposes creating the bucket the configuration declares - with the
	// markers on it. The cloud will refuse that create while the unowned
	// bucket holds the name, which is the loud failure the operator wants
	// instead of the quiet adoption this used to be.
	bucket := flocitest.ResourceBlock(t, first, "aws_s3_bucket.data")
	if strings.Contains(bucket, "will be updated in-place") {
		t.Errorf("an unmarked live bucket was adopted on the strength of its name:\n%s", bucket)
	}
	if !strings.Contains(bucket, "will be created") {
		t.Errorf("the declared bucket is not planned as a create:\n%s", bucket)
	}
	for _, want := range []string{
		`+ "tofu-estate"  = "` + stampEstate + `"`,
		`+ "tofu-address" = "aws_s3_bucket.data"`,
	} {
		if !strings.Contains(bucket, want) {
			t.Errorf("the bucket's create does not carry %s:\n%s", want, bucket)
		}
	}
	if !strings.Contains(first, "Live resource outside this estate") {
		t.Errorf("the live bucket was not reported as unowned:\n%s", first)
	}
	if !strings.Contains(first, stampBucket) || !strings.Contains(first, "[UNOWNED]") {
		t.Errorf("the omissions section does not name the live bucket that is in the way:\n%s", first)
	}

	// The VPC: nothing about stamping can bind an unmarked resource whose
	// identity the provider assigned, so this run still proposes creating it
	// - with the markers on it, which is what makes the next run different.
	// Step 3 is exactly the adoption the foreign section offers.
	vpc := flocitest.ResourceBlock(t, first, "aws_vpc.main")
	if !strings.Contains(vpc, "will be created") {
		t.Errorf("the unmarked VPC was expected to plan as a create (discovery cannot see it):\n%s", vpc)
	}
	for _, want := range []string{
		`+ "tofu-estate"  = "` + stampEstate + `"`,
		`+ "tofu-address" = "aws_vpc.main"`,
	} {
		if !strings.Contains(vpc, want) {
			t.Errorf("the VPC's create does not carry %s:\n%s", want, vpc)
		}
	}
	if !strings.Contains(first, "Adoptable:") {
		t.Errorf("the live VPC was not offered for adoption:\n%s", first)
	}

	// --- Write the tags the plan proposed --------------------------------
	vpcID := flocitest.AWSCLI(t, flociPort, "ec2", "describe-vpcs",
		"--filters", "Name=cidr,Values=10.77.0.0/16",
		"--query", "Vpcs[0].VpcId", "--output", "text")
	if vpcID == "" || vpcID == "None" {
		t.Fatalf("could not find the VPC that terraform created")
	}
	flocitest.AWSCLI(t, flociPort, "ec2", "create-tags", "--resources", vpcID,
		"--tags", "Key=tofu-estate,Value="+stampEstate, "Key=tofu-address,Value=aws_vpc.main")
	flocitest.AWSCLI(t, flociPort, "s3api", "put-bucket-tagging", "--bucket", stampBucket,
		"--tagging", fmt.Sprintf(
			"TagSet=[{Key=tofu-estate,Value=%s},{Key=tofu-address,Value=aws_s3_bucket.data}]", stampEstate))
	t.Logf("wrote the proposed markers onto %s and %s with the AWS CLI", vpcID, stampBucket)

	// --- The payoff: the same command finds them -------------------------
	second := statelessPlan(t, tofuBin, dir, "-estate="+stampEstate)

	if !strings.Contains(second, "No changes.") {
		add, change, destroy, _ := flocitest.PlanSummary(second)
		t.Errorf("the stamped estate did not plan clean: %d to add, %d to change, %d to destroy\n%s",
			add, change, destroy, second)
	}
	if strings.Contains(second, "Not read from the live system") {
		t.Errorf("something was still unreadable after the markers were written:\n%s",
			flocitest.SectionFrom(second, "Not read from the live system"))
	}
	if strings.Contains(second, "Adoptable:") {
		t.Errorf("the VPC is still unbound after being marked:\n%s", flocitest.SectionFrom(second, "Adoptable:"))
	}

	// Nothing was written: no state file, either run.
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after live-plan (err = %v)", err)
	}
}

// TestStampEstateFixtureAgainstFloci is the no-op half: the P0.1 estate
// fixture hand-writes both markers on every taggable resource, so a stamping
// pass over the standing estate proposes no marker changes at all. The
// fixture is not editable by this task on purpose - it is the control.
func TestStampEstateFixtureAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "estate no-op")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort = flocitest.StartFloci(t, "cdf-p21")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+flociPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := flocitest.CopyEstate(t)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	// No destroy on the way out: the cloud these resources live in is the
	// container, and the container is removed. A terraform destroy here would
	// also fail on the lock file tofu init rewrites.
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	// No -estate: the name comes out of the configuration's own tofu-estate
	// tags, which is the path the harness uses and the path stamping shares.
	output := statelessPlan(t, tofuBin, dir)

	if strings.Contains(output, "Ownership marker conflict") {
		t.Errorf("stamping disputed a marker the fixture wrote:\n%s", output)
	}
	if strings.Contains(output, "Ownership markers not stamped") {
		t.Errorf("stamping degraded over the estate fixture, which names its own estate:\n%s", output)
	}

	// The no-op claim: no resource in the diff gains a marker tag. Two known
	// exceptions, both the emulator's rather than this task's, and both of a
	// shape RA.1 changed:
	//
	//	aws_iam_role.app                  floci-gaps #5   iam:GetRole omits Tags
	//	aws_ssm_parameter.demo_effect     floci-gaps #10  PutParameter drops
	//	                                                  the inline tag set
	//	aws_ssm_parameter.demo_existence  floci-gaps #10  same gap, the RA.6
	//	                                                  existence-flavor fixture
	//
	// All three read back untagged. Before RA.1 that made them tags-only
	// in-place diffs re-adding the markers the fixture itself declares, and
	// this test tolerated exactly that shape for the role alone - which is
	// why PE.3's receipt turned it red the moment a second resource joined
	// the same gap, and why RA.6's second receipt is folded into the same
	// tolerance rather than opening a new one. Under RA.1 an unreadable
	// marker means unowned, so none of the three enters the prior state at
	// all: each is omitted as [UNOWNED] with an adoption hint and planned as
	// a create, and the markers in that create's diff are the
	// configuration's own, printed because the whole resource is being
	// printed - not because stamping proposed anything.
	//
	// So the assertion is now: a marker addition on any resource that is not
	// one of the three documented unowned gaps is a stamping failure, and
	// the gaps themselves must be omitted-and-created, never quietly
	// updated.
	gapAddrs := map[string]string{
		"aws_iam_role.app":                 "floci-gaps #5 (iam:GetRole omits Tags)",
		"aws_ssm_parameter.demo_effect":    "floci-gaps #10 (PutParameter drops inline tags)",
		"aws_ssm_parameter.demo_existence": "floci-gaps #10 (PutParameter drops inline tags)",
	}
	for _, addr := range flocitest.ChangedResources(output) {
		block := flocitest.ResourceBlock(t, output, addr)
		var markers []string
		for _, line := range strings.Split(block, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "+") {
				continue
			}
			if strings.Contains(trimmed, `"tofu-estate"`) || strings.Contains(trimmed, `"tofu-address"`) {
				markers = append(markers, trimmed)
			}
		}
		if len(markers) == 0 {
			continue
		}
		gap, known := gapAddrs[addr]
		if !known {
			t.Errorf("a marker was proposed over the already-marked estate on %s: %v\n%s", addr, markers, block)
			continue
		}
		// The gap's rendering is itself the assertion: a create, with the
		// omission behind it saying why. An in-place update here would mean
		// an unverified resource had entered the prior state, which is the
		// C1 shape RA.1 closed.
		if !strings.Contains(block, "will be created") {
			t.Errorf("%s carries the %s tag gap, so it must read back unowned and plan as a create; the plan says otherwise:\n%s",
				addr, gap, block)
			continue
		}
		if !strings.Contains(output, "  "+addr+" [UNOWNED]") {
			t.Errorf("%s is planned as a create with no [UNOWNED] omission explaining it:\n%s", addr, output)
			continue
		}
		if !strings.Contains(output, `tofu-address="`+addr+`"`) {
			t.Errorf("%s's [UNOWNED] omission carries no adoption hint naming it:\n%s", addr, output)
		}
		t.Logf("known %s: %s reads back untagged, so it is correctly unowned and planned as a create", gap, addr)
	}

	// Nothing is ever destroyed over a standing, already-marked estate -
	// the claim that did not move, and the one that would matter most.
	if _, _, destroy, ok := flocitest.PlanSummary(output); ok && destroy != 0 {
		t.Errorf("the estate plan proposes %d destroy(s):\n%s", destroy, output)
	}
	if strings.Contains(output, "will be destroyed") {
		t.Errorf("the estate plan contains a destroy header:\n%s", output)
	}

	// And the two gaps are the ONLY resources missing from the prior state:
	// a third omission would mean something else stopped being readable.
	if got := unownedOmissions(output); !sameSet(got, keysOf(gapAddrs)) {
		t.Errorf("the plan reports %v as unowned, want exactly the two documented emulator gaps %v:\n%s",
			got, keysOf(gapAddrs), flocitest.SectionFrom(output, "Not read from the live system"))
	}
}

// TestTaggableSetAgainstRealSchemas is TestTaggableSetCoversAdmissionTable's
// live half: the same taggability pin, answered by the schema the real AWS
// provider serves instead of the caricature in testSchemas. terraform init
// downloads the provider release the estate fixture pins, `terraform
// providers schema -json` dumps its schema without configuring anything, and
// the tags attribute of each admitted type is rebuilt as a configschema
// attribute and put through the real taggable predicate. A provider release
// that adds a tags argument to one of the untaggable four, or drops it from
// a type that has it, fails here on the version bump that brings it in - a
// reviewable failure instead of a silent change in which resources report
// SkipUntaggable.
//
// It is gated with the rest of this tier because it needs a terraform binary
// and a provider download, but it starts no emulator: a schema dump needs no
// cloud.
func TestTaggableSetAgainstRealSchemas(t *testing.T) {
	flocitest.Gate(t, "taggable-set pin")
	flocitest.RequireBinary(t, terraformBin)

	dir := flocitest.CopyEstate(t)
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-backend=false", "-input=false", "-no-color")

	cmd := exec.Command(terraformBin, "providers", "schema", "-json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("terraform providers schema -json failed: %v%s", err, stderrOfExit(err))
	}

	// The slice of the dump this test reads: per resource type, the fields of
	// the top-level tags attribute that the taggable predicate consults.
	type schemaAttr struct {
		Type     json.RawMessage `json:"type"`
		Optional bool            `json:"optional"`
		Required bool            `json:"required"`
		Computed bool            `json:"computed"`
	}
	type resourceSchema struct {
		Block struct {
			Attributes map[string]schemaAttr `json:"attributes"`
		} `json:"block"`
	}
	var dump struct {
		ProviderSchemas map[string]struct {
			ResourceSchemas map[string]resourceSchema `json:"resource_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal(out, &dump); err != nil {
		t.Fatalf("decoding the schema dump: %v", err)
	}

	var resources map[string]resourceSchema
	for addr, ps := range dump.ProviderSchemas {
		if strings.HasSuffix(addr, "/aws") {
			resources = ps.ResourceSchemas
			break
		}
	}
	if resources == nil {
		t.Fatalf("the schema dump names no aws provider; it has %d provider(s)", len(dump.ProviderSchemas))
	}

	check := func(types []string, want bool) {
		for _, resourceType := range types {
			rs, ok := resources[resourceType]
			if !ok {
				t.Errorf("the provider serves no schema for admitted type %s", resourceType)
				continue
			}
			// taggable reads nothing but the top-level tags attribute, so
			// that attribute alone is rebuilt for it.
			block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
			if a, ok := rs.Block.Attributes["tags"]; ok {
				ty, err := ctyjson.UnmarshalType(a.Type)
				if err != nil {
					t.Errorf("decoding the type of %s.tags: %v", resourceType, err)
					continue
				}
				block.Attributes["tags"] = &configschema.Attribute{
					Type:     ty,
					Optional: a.Optional,
					Required: a.Required,
					Computed: a.Computed,
				}
			}
			if got := taggable(block); got != want {
				t.Errorf("taggable(%s) = %v against the real provider schema, want %v; if the provider's schema changed, update the pin in stamp_test.go and the untaggable list in stateless/LIMITATIONS.md together",
					resourceType, got, want)
			}
		}
	}
	check(taggableAdmittedTypes, true)
	check(untaggableAdmittedTypes, false)
}

// stderrOfExit is the stderr an exec.ExitError carried, or nothing.
func stderrOfExit(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return "\n" + string(exit.Stderr)
	}
	return ""
}

var stampOmittedInstance = regexp.MustCompile(`^\s+(\S+) \[UNOWNED\]\s*$`)

func unownedOmissions(output string) []string {
	var out []string
	for _, line := range strings.Split(flocitest.SectionFrom(output, "Not read from the live system"), "\n") {
		if m := stampOmittedInstance.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// stampBucket is the marker-less fixture's bucket. Its name is in the
// configuration, which is why the projection can read it back with no marker
// and why it is the resource that shows the in-place tags update.
const stampBucket = "tofu-stamp-e2e-data"

// markerlessFixture is deliberately outside stateless/e2e/estate: the estate
// fixture is the control for the no-op case and this task may not touch it.
// No IAM here on purpose - floci does not serve tags on roles, and a fixture
// that walks into that gap would be testing the emulator.
const markerlessFixture = `# Written by internal/stateless/stamp's integration test. A configuration with
# no ownership markers anywhere: the world before anyone stamped anything.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}

# Server-assigned identity: nothing in configuration names it, so without a
# marker no run can find it.
resource "aws_vpc" "main" {
  cidr_block = "10.77.0.0/16"
}

# Client-named identity: the bucket name is in configuration, so the
# projection reads it back with no marker at all - which is what makes it the
# resource whose markers arrive as an in-place tags update.
resource "aws_s3_bucket" "data" {
  bucket = "` + stampBucket + `"
}
`

func writeMarkerlessFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(markerlessFixture), 0o600); err != nil {
		t.Fatalf("writing the marker-less fixture: %v", err)
	}
	return dir
}

// ---------------------------------------------------------------------------
// Running the command and reading its output
// ---------------------------------------------------------------------------

func statelessPlan(t *testing.T, tofuBin, dir string, args ...string) string {
	t.Helper()

	full := append([]string{"live-plan", "-no-color", "-input=false"}, args...)
	start := time.Now()
	cmd := exec.Command(tofuBin, full...) //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu %s took %s\n%s", strings.Join(full, " "), time.Since(start), output)
	if err != nil {
		t.Fatalf("live-plan failed: %v", err)
	}
	return output
}

// ---------------------------------------------------------------------------
// floci and the binary
// ---------------------------------------------------------------------------
