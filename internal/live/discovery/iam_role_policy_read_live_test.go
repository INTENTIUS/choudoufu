package discovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestIAMRolePolicyParentReadRemovalAgainstFloci is #692's parent-keyed IAM
// orphan-recovery half: the inline aws_iam_role_policy is untaggable and
// rides along on a scoped read of its owning role, exactly the shape
// aws_s3_bucket_policy proves for S3. GetRolePolicy returns a clean
// NoSuchEntity once the inline policy is deleted (verified against real AWS
// account 354867293429, 2026-09-02), which is the exists/not-exists
// distinction parentReadRemovable requires.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestIAMRolePolicyParentReadRemovalAgainstFloci -v
func TestIAMRolePolicyParentReadRemovalAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "iam role policy parent-read removal")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-iamrp")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	dir := t.TempDir()
	writeIAMRolePolicyFixture(t, dir, true)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	if !rolePolicyExists(flociPort) {
		t.Fatal("the stock apply did not create a live inline role policy")
	}

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)

	writeIAMRolePolicyFixture(t, dir, false)

	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	res, diags := Discover(ctx, Request{
		Estate:      "iamrp-parent-read",
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    provider,
		Region:      awsRegion,
		Sweep:       true,
		SweepTypes:  []string{"aws_iam_role", "aws_iam_role_policy"},
	})
	t.Logf("discovery result:\n%s", res)
	assertNoErrors(t, diags)

	f, ok := findParentRead(res, "aws_iam_role_policy", iamRolePolicyRole+":inline")
	if !ok {
		t.Fatalf("no parent-read finding for the role's inline policy:\n%s", res)
	}
	t.Logf("FINDING: Removal=%v Withheld=%q Parent=%q ParentAddr=%q ImportID=%q",
		f.Removal, f.Withheld, f.Parent, f.ParentAddr.String(), f.ImportID)

	if !f.Removal {
		t.Errorf("aws_iam_role_policy finding is not a removal (Withheld=%q)", f.Withheld)
	}

	wantAddr := mustAddr(t, "aws_iam_role_policy.inline")
	var foundResolution bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != wantAddr.String() {
			continue
		}
		foundResolution = true
		if !r.Undeclared {
			t.Error("the parent-read removal's resolution is not marked Undeclared")
		}
	}
	if !foundResolution {
		t.Fatalf("no resolution was produced at aws_iam_role_policy.app:\n%s", res)
	}

	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	proj, projDiags := projection.BuildFrom(ctx, cfg, res.Resolutions, provs)
	assertNoErrors(t, projDiags)

	is := proj.State.ResourceInstance(wantAddr)
	if is == nil || is.Current == nil {
		t.Fatalf("aws_iam_role_policy.app did not materialize:\n%s", res)
	}
}

const iamRolePolicyRole = "iamrp-parent-read-role"

func writeIAMRolePolicyFixture(t *testing.T, dir string, withPolicy bool) {
	t.Helper()
	src := fmt.Sprintf(`
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
}

resource "aws_iam_role" "app" {
  name               = %q
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    tofu-estate  = "iamrp-parent-read"
    tofu-address = "aws_iam_role.app"
  }
}
`, iamRolePolicyRole)

	if withPolicy {
		src += `
resource "aws_iam_role_policy" "app" {
  name = "inline"
  role = aws_iam_role.app.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject"]
      Resource = "*"
    }]
  })
}
`
	}

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil { //nolint:gosec // test temp dir
		t.Fatalf("writing the fixture: %v", err)
	}
}

func rolePolicyExists(flociPort string) bool {
	full := []string{"--endpoint-url", flocitest.Endpoint(flociPort), "iam", "get-role-policy", "--role-name", iamRolePolicyRole, "--policy-name", "inline"}
	_, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	return err == nil
}
