// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestControllerHeldAgainstFloci is GitHub issue #1606's "Done when": AWS
// resources carrying the tags an ACK controller writes, found by a real
// plan against floci, are not proposed for destroy, not offered for
// adoption, and reported as controller-held.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/foreign/ -run TestControllerHeldAgainstFloci -v
//
// Everything live is made out of band with the AWS CLI, the way an ACK
// controller would make it, with ACK's default tags
// (services.k8s.aws/controller-version, services.k8s.aws/namespace):
//
//   - an S3 bucket that also carries this estate's markers for an address
//     the configuration does not declare: the orphan shape, which the sweep
//     destroys on main;
//   - a security group whose name matches the declared one: the content
//     match that makes it an adoption candidate on main;
//   - and a plain security group with no tags at all, the control that
//     proves the unclaimed listing ran and still reports what is foreign.
func TestControllerHeldAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "controller-held")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	port := flocitest.StartFloci(t, "cdf-held")
	t.Setenv("AWS_ENDPOINT_URL", flocitest.Endpoint(port))
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)
	tofuBin := flocitest.BuildTofu(t)

	const estate = "held-e2e"
	suffix := fmt.Sprint(os.Getpid())
	ackBucket := "held-e2e-ack-marked-" + suffix
	sgName := "held-e2e-web-" + suffix
	plainName := "held-e2e-plain-" + suffix

	vpcID := flocitest.AWSCLI(t, port, "ec2", "describe-vpcs",
		"--filters", "Name=isDefault,Values=true", "--query", "Vpcs[0].VpcId", "--output", "text")
	if vpcID == "" || vpcID == "None" {
		t.Fatal("floci serves no default VPC to put the security groups in")
	}

	dir := t.TempDir()
	config := fmt.Sprintf(`terraform {
  live {
    estate = %q
  }
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
  s3_use_path_style           = true
}

resource "aws_security_group" "web" {
  name        = %q
  description = "declared"
  vpc_id      = %q
}
`, estate, sgName, vpcID)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	const ackTagSet = "{Key=services.k8s.aws/controller-version,Value=s3-v1.0.14},{Key=services.k8s.aws/namespace,Value=team-a}"
	flocitest.AWSCLI(t, port, "s3api", "create-bucket", "--bucket", ackBucket)
	flocitest.AWSCLI(t, port, "s3api", "put-bucket-tagging", "--bucket", ackBucket,
		"--tagging", "TagSet=["+ackTagSet+",{Key=tofu-estate,Value="+estate+"},{Key=tofu-address,Value=aws_s3_bucket.gone}]")
	ackSG := flocitest.AWSCLI(t, port, "ec2", "create-security-group",
		"--group-name", sgName, "--description", "made by ACK", "--vpc-id", vpcID,
		"--tag-specifications", "ResourceType=security-group,Tags=["+strings.ReplaceAll(ackTagSet, "s3-v1.0.14", "ec2-v1.2.3")+"]",
		"--query", "GroupId", "--output", "text")
	plainSG := flocitest.AWSCLI(t, port, "ec2", "create-security-group",
		"--group-name", plainName, "--description", "nobody's", "--vpc-id", vpcID,
		"--query", "GroupId", "--output", "text")

	// Read the tags back with no choudoufu code involved, so a failure
	// below is about the sweep and not about whether floci kept them.
	if got := flocitest.AWSCLI(t, port, "s3api", "get-bucket-tagging", "--bucket", ackBucket, "--output", "text"); !strings.Contains(got, "services.k8s.aws/namespace") {
		t.Fatalf("floci did not keep the ACK tags on bucket %s: %q", ackBucket, got)
	}
	if got := flocitest.AWSCLI(t, port, "ec2", "describe-security-groups", "--group-ids", ackSG, "--query", "SecurityGroups[0].Tags", "--output", "text"); !strings.Contains(got, "services.k8s.aws/namespace") {
		t.Fatalf("floci did not keep the ACK tags on security group %s: %q", ackSG, got)
	}

	cmd := exec.Command(tofuBin, "live-plan", "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), collectUnclaimedEnv+"=1")
	outBytes, err := cmd.CombinedOutput()
	output := string(outBytes)
	t.Logf("choudoufu live-plan:\n%s", output)
	if err != nil {
		t.Fatalf("live-plan failed: %v", err)
	}

	// Not proposed for destroy.
	if strings.Contains(output, "aws_s3_bucket.gone will be destroyed") {
		t.Errorf("the ACK-made bucket carrying this estate's marker is proposed for destroy")
	}
	if _, _, destroy, ok := flocitest.PlanSummary(output); ok && destroy != 0 {
		t.Errorf("the plan proposes %d destroy(s); a controller-held resource must never be one", destroy)
	}

	// Not adoptable, not foreign; the control group proves the listing ran.
	if strings.Contains(output, "Adoptable:") {
		t.Errorf("something was offered for adoption; the only name match is the ACK-made group %s", ackSG)
	}
	if !strings.Contains(foreignSection(t, output), plainSG) {
		t.Fatalf("the plain control group %s is not reported foreign, so the unclaimed listing did not run and this test proves nothing", plainSG)
	}
	// Reported as controller-held, naming the controller and its object.
	// Every mention of an ACK-made resource in the plan must fall inside
	// the Controller-held section: a mention anywhere else (the foreign
	// section, an adoption offer, a destroy) is the resource being treated
	// as something other than held.
	held := flocitest.SectionFrom(output, "Controller-held:")
	if held == "" {
		t.Fatalf("the plan has no Controller-held section at all")
	}
	heldStart := strings.Index(output, held)
	heldEnd := heldStart + len(held)
	for _, id := range []string{ackBucket, ackSG} {
		found := false
		for off := 0; ; {
			i := strings.Index(output[off:], id)
			if i < 0 {
				break
			}
			at := off + i
			found = true
			if at < heldStart || at >= heldEnd {
				t.Errorf("the ACK-made %s appears outside the controller-held section: %q", id, lineAt(output, at))
			}
			off = at + len(id)
		}
		if !found {
			t.Errorf("the ACK-made %s is not in the plan at all", id)
		}
	}

	flat := strings.Join(strings.Fields(held), " ")
	for _, want := range []string{
		"aws_s3_bucket " + ackBucket + " held by ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a",
		"carries this estate's marker for aws_s3_bucket.gone, which the configuration does not declare; not destroyed.",
		"aws_security_group " + ackSG + " (" + sgName + ") held by ACK ec2 controller (ec2-v1.2.3), custom resource in namespace team-a",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the controller-held section does not say %q:\n%s", want, held)
		}
	}

	// live-ls lists what carries the estate's marker, and names the marked
	// ACK bucket controller-held.
	ls := exec.Command(tofuBin, "live-ls", "-estate", estate, "-region", awsRegion, "-no-color") //nolint:gosec // test binary
	ls.Dir = dir
	lsOut, err := ls.CombinedOutput()
	t.Logf("choudoufu live-ls:\n%s", lsOut)
	if err != nil {
		t.Fatalf("live-ls failed: %v", err)
	}
	lsFlat := strings.Join(strings.Fields(string(lsOut)), " ")
	if !strings.Contains(lsFlat, "arn:aws:s3:::"+ackBucket+" held by: ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a (controller-held: never swept, never adopted)") {
		t.Errorf("live-ls does not name %s controller-held", ackBucket)
	}
}

// lineAt is the whole line of s containing byte offset at, for a failure
// message that shows where a mention landed.
func lineAt(s string, at int) string {
	start := strings.LastIndex(s[:at], "\n") + 1
	end := strings.Index(s[at:], "\n")
	if end < 0 {
		return s[start:]
	}
	return s[start : at+end]
}
