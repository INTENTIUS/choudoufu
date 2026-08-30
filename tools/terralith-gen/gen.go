// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Composition, as a function of scale (issue #564's "one flag" requirement).
//
// Every count below is linear in scale, so "the same estate at 4x" is
// -scale 4 rather than a different artifact, and the proportions this
// produces hold (approximately - see buildEstate's doc comment on the
// fixed, non-scaling supporting layer) as scale grows.
//
//	teams    = teamsPerScale * scale     (6 identity resources each)
//	services = servicesPerScale * scale  (2 identity + 2 container each)
//	records  = dnsRecordsPerScale * scale
//
// At scale=1 (the "genuinely small tier" #564 asks for): 6 teams (36
// identity resources), 1 service (2 identity + 2 container), 10 DNS
// records + 1 zone, plus a fixed layer that does not scale with it - 1
// VPC, 1 subnet, 1 security group (counted as "supporting") and 1 ECS
// cluster (counted as "container") - because it is realistic for many
// services to share one VPC and one cluster rather than minting a new one
// per service. That is 38 identity resources of 55 total, ~69%; see
// composition.identityPercent and gen_test.go's
// TestIdentityShareApproximatesTarget for the measured share at larger
// scale, where the fixed layer's weight shrinks and identity's share
// rises toward ~75%.
const (
	teamsPerScale      = 6
	servicesPerScale   = 1
	dnsRecordsPerScale = 10
)

// composition is the actual, computed shape of one generated estate - not
// asserted, measured from the same counters the generator increments
// while it builds the HCL. main.go prints it; the tests in gen_test.go
// check it against the ~70% identity / duplication targets #564 (via
// #546) describes.
type composition struct {
	identityResources   int
	containerResources  int
	dnsResources        int
	supportingResources int

	// Role/policy duplication, per #564's literal wording ("near-identical
	// roles and policies differing by a name prefix or a single ARN"):
	// only aws_iam_role and aws_iam_policy have no foreign-key argument
	// forcing every instance to differ, so they are the only types this
	// measures. See dupTracker and its call sites below for the exact
	// method: two blocks are "duplicate" when their body is byte-identical
	// once the resource's own name is set aside.
	totalRolePolicyBlocks     int
	duplicateRolePolicyBlocks int
}

func (c composition) totalResources() int {
	return c.identityResources + c.containerResources + c.dnsResources + c.supportingResources
}

func (c composition) identityPercent() float64 {
	t := c.totalResources()
	if t == 0 {
		return 0
	}
	return 100 * float64(c.identityResources) / float64(t)
}

func (c composition) duplicationPercent() float64 {
	if c.totalRolePolicyBlocks == 0 {
		return 0
	}
	return 100 * float64(c.duplicateRolePolicyBlocks) / float64(c.totalRolePolicyBlocks)
}

// dupTracker accumulates a canonicalized "content key" per role/policy
// block - everything about the block except its own name/label - and
// reports how many of the blocks it saw share a key with at least one
// other. It is not a general HCL differ: the caller decides what the key
// is (see buildIAM's dup.add calls), which is what makes the method
// explicit and auditable rather than a black box.
type dupTracker struct {
	counts map[string]int
	keys   []string
}

func newDupTracker() *dupTracker {
	return &dupTracker{counts: map[string]int{}}
}

func (d *dupTracker) add(key string) {
	d.counts[key]++
	d.keys = append(d.keys, key)
}

func (d *dupTracker) total() int { return len(d.keys) }

func (d *dupTracker) duplicates() int {
	n := 0
	for _, k := range d.keys {
		if d.counts[k] >= 2 {
			n++
		}
	}
	return n
}

// estate is one generated terralith: every file this run writes, plus the
// composition counters accumulated while building them.
type estate struct {
	files       map[string]string // relative path -> content
	composition composition
}

// buildEstate is the whole generator. It never emits a "live" block, a
// record_store, configs.LiveSidecarFilename, or a tofu-estate/tofu-address
// tag anywhere - see shape_test.go's TestNoChoudoufuConstructLeaks for the
// mechanical check. Every resource lives in the estate's single root
// module; there is no module call anywhere in the output, which is what
// keeps this a single state rather than the decomposition #546's last
// comment contrasts it with.
func buildEstate(scale int, prefix string) *estate {
	teams := teamsPerScale * scale
	services := servicesPerScale * scale
	dnsRecords := dnsRecordsPerScale * scale

	dup := newDupTracker()

	var iam strings.Builder
	comp := composition{}

	// ── Identity layer: teams ────────────────────────────────────────────
	for i := 0; i < teams; i++ {
		n := buildTeam(&iam, i, prefix, dup)
		comp.identityResources += n
	}

	// ── Container-service layer ──────────────────────────────────────────
	var network strings.Builder
	writeNetwork(&network, prefix)
	comp.supportingResources += 3 // vpc, subnet, security group

	var ecs strings.Builder
	writeCluster(&ecs, prefix)
	comp.containerResources++ // cluster
	for j := 0; j < services; j++ {
		id, c := buildService(&ecs, &iam, j, prefix, dup)
		comp.identityResources += id
		comp.containerResources += c
	}

	// ── DNS fan-out ───────────────────────────────────────────────────────
	var dns strings.Builder
	writeZone(&dns, prefix)
	comp.dnsResources++ // zone
	for k := 0; k < dnsRecords; k++ {
		writeRecord(&dns, k)
		comp.dnsResources++
	}

	comp.totalRolePolicyBlocks = dup.total()
	comp.duplicateRolePolicyBlocks = dup.duplicates()

	files := map[string]string{
		"versions.tf":  versionsTF(),
		"main.tf":      mainTF(prefix),
		"iam.tf":       header("iam.tf", "the identity layer: team roles/policies/attachments/profiles and each ECS service's execution role") + iam.String(),
		"network.tf":   header("network.tf", "the one shared VPC/subnet/security group the container-service layer's tasks run in") + network.String(),
		"ecs.tf":       header("ecs.tf", "the container-service layer: one cluster, and per service a template task definition plus a service with lifecycle.ignore_changes on task_definition") + ecs.String(),
		"dns.tf":       header("dns.tf", "the DNS fan-out: one zone, many records") + dns.String(),
		"GENERATED.md": generatedMD(scale, prefix, teams, services, dnsRecords, comp),
	}

	return &estate{files: files, composition: comp}
}

func header(file, purpose string) string {
	return fmt.Sprintf("# %s - generated by tools/terralith-gen (issue #564); %s.\n# Rewritten in full on every run.\n\n", file, purpose)
}

// write writes every file this run produced into out, creating it if
// necessary. It does not touch any file it did not generate: unlike
// estate-gen's cohort directories, a terralith-gen output directory is not
// a long-lived, hand-annotated fixture with an ownership split to enforce
// - callers (issue #565's onward) generate fresh into a scratch directory
// per run.
func (e *estate) write(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for name, content := range e.files {
		if err := os.WriteFile(filepath.Join(out, name), []byte(content), 0o644); err != nil { //nolint:gosec // a generated Terraform fixture, not a secret
			return err
		}
	}
	return nil
}

// ── versions.tf / main.tf ────────────────────────────────────────────────

// versionsTF deliberately carries no backend block: stock Terraform with
// no backend configured uses local state, exactly where a real adoption
// starts (#546's framing, docs/use/migrate.md step 1). It also carries no
// "live" block - see shape_test.go.
func versionsTF() string {
	return fmt.Sprintf(`# versions.tf - provider wiring only. Generated by tools/terralith-gen.
# No backend block: local state, same as any stock Terraform root module
# before migration. No "live" block anywhere in this estate - see #564.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = %q
      version = "= %s"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style            = true
}
`, providerSource, providerVersion)
}

func mainTF(prefix string) string {
	return fmt.Sprintf(`# main.tf - shared locals. Generated by tools/terralith-gen.

locals {
  name_prefix = %q

  # A placeholder image tag: the container-service layer's task
  # definition template holds every Terraform-owned fact (cpu, memory,
  # environment, log configuration) with this standing in for the
  # actual image a deploy pipeline pushes - issue #564's deploy-time
  # drift pattern. aws_ecs_service.*.lifecycle.ignore_changes (ecs.tf)
  # is what lets a real deploy change the running task_definition
  # without Terraform reverting it back to this placeholder on the
  # next plan.
  placeholder_image = "111111111111.dkr.ecr.us-east-1.amazonaws.com/placeholder:latest"
}
`, prefix)
}

// ── Identity layer ────────────────────────────────────────────────────────

// isBoilerplateTeam decides whether team i draws its customer-managed
// policy from the small, shared boilerplatePolicies pool (a literal
// content duplicate of every other team sharing that same index, once
// names are stripped) or gets a policy scoped to its own name (genuinely
// unique content). Alternating keeps both halves of the identity layer
// present at every scale rather than only appearing once teams is large.
func isBoilerplateTeam(i int) bool { return i%2 == 0 }

// buildTeam writes one team's six identity resources and returns how many
// it wrote (always 6): aws_iam_role, aws_iam_role_policy (inline),
// aws_iam_policy (customer-managed), two aws_iam_role_policy_attachment
// (one AWS-managed, one to the team's own customer-managed policy), and
// aws_iam_instance_profile. Role and policy bodies are registered with dup
// per #564's literal "roles and policies" wording (see composition's doc
// comment); the attachments, the inline policy and the instance profile
// are not measured, because each carries a required role reference that
// can never be shared with another team's - see templates.go's comment on
// inlineTemplates for why.
func buildTeam(w *strings.Builder, i int, prefix string, dup *dupTracker) int {
	team := fmt.Sprintf("%s-team-%04d", prefix, i)
	label := fmt.Sprintf("team_%04d", i)

	var trustHCL, dupKey string
	if isBoilerplateTeam(i) {
		// Boilerplate teams draw from the small, shared service-principal
		// pool - the literal "differs only by a name prefix" duplication
		// #564 describes.
		principal := trustPrincipals[(i/2)%len(trustPrincipals)]
		trustHCL = assumeRolePolicyHCL(principal)
		dupKey = "role:" + principal
	} else {
		// Scoped teams get a cross-account trust naming their own
		// synthetic account ID: genuinely distinct content, the role-layer
		// twin of scopedPolicyHCL's per-team resource ARN below.
		acct := crossAccountID(i)
		trustHCL = assumeRolePolicyCrossAccountHCL(acct)
		dupKey = "role:cross-account:" + acct // unique by construction
	}
	fmt.Fprintf(w, `resource "aws_iam_role" "%s_role" {
  name               = "%s-role"
  assume_role_policy = %s
}

`, label, team, trustHCL)
	dup.add(dupKey)

	inline := inlineTemplates[i%len(inlineTemplates)]
	fmt.Fprintf(w, `resource "aws_iam_role_policy" "%s_inline" {
  name   = "%s-inline"
  role   = aws_iam_role.%s_role.name
  policy = %s
}

`, label, team, label, inlinePolicyHCL(inline.actions))

	var policyDoc string
	if isBoilerplateTeam(i) {
		bp := boilerplatePolicies[(i/2)%len(boilerplatePolicies)]
		policyDoc = boilerplatePolicyHCL(bp.actions)
		dup.add("policy:boilerplate:" + bp.label)
	} else {
		policyDoc = scopedPolicyHCL(team, []string{"s3:GetObject", "s3:PutObject"})
		dup.add("policy:scoped:" + team) // unique by construction: the ARN embeds this team's own name
	}
	fmt.Fprintf(w, `resource "aws_iam_policy" "%s_policy" {
  name   = "%s-policy"
  policy = %s
}

`, label, team, policyDoc)

	arn := managedPolicyARNs[i%len(managedPolicyARNs)]
	fmt.Fprintf(w, `resource "aws_iam_role_policy_attachment" "%s_managed_attach" {
  role       = aws_iam_role.%s_role.name
  policy_arn = %q
}

`, label, label, arn)

	fmt.Fprintf(w, `resource "aws_iam_role_policy_attachment" "%s_custom_attach" {
  role       = aws_iam_role.%s_role.name
  policy_arn = aws_iam_policy.%s_policy.arn
}

`, label, label, label)

	fmt.Fprintf(w, `resource "aws_iam_instance_profile" "%s_profile" {
  name = "%s-profile"
  role = aws_iam_role.%s_role.name
}

`, label, team, label)

	return 6
}

// ── Container-service layer ─────────────────────────────────────────────

func writeNetwork(w *strings.Builder, prefix string) {
	fmt.Fprintf(w, `resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    Name = "%s-vpc"
  }
}

resource "aws_subnet" "main" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.42.1.0/24"
  availability_zone = "us-east-1a"

  tags = {
    Name = "%s-subnet"
  }
}

resource "aws_security_group" "ecs" {
  name        = "%s-ecs-sg"
  description = "terralith-gen: shared security group for the container-service layer"
  vpc_id      = aws_vpc.main.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

`, prefix, prefix, prefix)
}

func writeCluster(w *strings.Builder, prefix string) {
	fmt.Fprintf(w, `resource "aws_ecs_cluster" "main" {
  name = "%s-cluster"
}

`, prefix)
}

// buildService writes one service's execution role, its attachment, its
// template task definition, and its service (with the deploy-time drift
// pattern: a placeholder image tag on the task definition,
// lifecycle.ignore_changes = [task_definition] on the service). Returns
// (identity resource count, container resource count) = (2, 2).
func buildService(ecs, iam *strings.Builder, j int, prefix string, dup *dupTracker) (identity, container int) {
	svc := fmt.Sprintf("%s-svc-%04d", prefix, j)
	label := fmt.Sprintf("svc_%04d", j)

	const ecsPrincipal = "ecs-tasks.amazonaws.com"
	fmt.Fprintf(iam, `resource "aws_iam_role" "%s_exec_role" {
  name               = "%s-exec-role"
  assume_role_policy = %s
}

`, label, svc, assumeRolePolicyHCL(ecsPrincipal))
	dup.add("role:" + ecsPrincipal)

	fmt.Fprintf(iam, `resource "aws_iam_role_policy_attachment" "%s_exec_attach" {
  role       = aws_iam_role.%s_exec_role.name
  policy_arn = %q
}

`, label, label, ecsExecutionPolicyARN)
	identity = 2

	fmt.Fprintf(ecs, `resource "aws_ecs_task_definition" "%s" {
  family                   = "%s"
  requires_compatibilities = ["FARGATE"]
  network_mode              = "awsvpc"
  cpu                       = "256"
  memory                    = "512"
  execution_role_arn        = aws_iam_role.%s_exec_role.arn

  container_definitions = jsonencode([
    {
      name      = "%s-container"
      image     = local.placeholder_image
      essential = true
      cpu       = 256
      memory    = 512
      portMappings = [{
        containerPort = 8080
        protocol      = "tcp"
      }]
      environment = [{
        name  = "SERVICE_NAME"
        value = "%s"
      }]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = "/ecs/%s"
          "awslogs-region"        = "us-east-1"
          "awslogs-stream-prefix" = "svc"
        }
      }
    }
  ])
}

resource "aws_ecs_service" "%s" {
  name            = "%s"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.%s.arn
  desired_count   = 0
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = [aws_subnet.main.id]
    security_groups  = [aws_security_group.ecs.id]
    assign_public_ip = false
  }

  lifecycle {
    ignore_changes = [task_definition]
  }
}

`, label, svc, label, svc, svc, svc, label, svc, label)
	container = 2

	return identity, container
}

// ── DNS fan-out ───────────────────────────────────────────────────────────

func writeZone(w *strings.Builder, prefix string) {
	fmt.Fprintf(w, `resource "aws_route53_zone" "main" {
  name = "%s.terralith.test"
}

`, prefix)
}

// writeRecord writes one record, cycling A/CNAME/TXT by index. Every
// value is declared literally in the record itself - "declaration-carried"
// per #564 - never computed from a data source or another resource's
// attribute.
func writeRecord(w *strings.Builder, k int) {
	label := fmt.Sprintf("rec_%04d", k)
	name := fmt.Sprintf("host-%04d.${aws_route53_zone.main.name}", k)
	var recType, value string
	switch k % 3 {
	case 0:
		recType = "A"
		value = fmt.Sprintf("%q", fmt.Sprintf("10.60.%d.%d", (k/256)%256, k%256))
	case 1:
		recType = "CNAME"
		value = fmt.Sprintf("%q", fmt.Sprintf("target-%04d.upstream.example.com.", k))
	default:
		// TXT: the AWS provider (hashicorp/aws, tested at 6.59.0) already
		// quote-wraps a TXT record's value itself - Route53 requires each
		// TXT value to carry a literal pair of double quotes, and the
		// provider adds that pair, so the HCL value here must be the bare
		// text with none of its own. A previous version of this line
		// pre-wrapped the text in a literal quote pair before the %q that
		// renders the HCL string literal, producing a value the provider
		// then quoted AGAIN on top of - real AWS rejects the resulting
		// double-quoted content with "InvalidCharacterString (Value should
		// be enclosed in quotation marks) encountered with '""v=textNNNN""'"
		// (issue #567's live-AWS run against a real account, 2026-08-30);
		// floci accepted the malformed value silently, which is why this
		// was never caught by #564/#565/#566's floci-only measurements.
		recType = "TXT"
		value = fmt.Sprintf("%q", fmt.Sprintf("v=text%04d", k))
	}
	fmt.Fprintf(w, `resource "aws_route53_record" "%s" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "%s"
  type    = "%s"
  ttl     = 300
  records = [%s]
}

`, label, name, recType, value)
}

// ── GENERATED.md ─────────────────────────────────────────────────────────

func generatedMD(scale int, prefix string, teams, services, dnsRecords int, c composition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# terralith (scale=%d, prefix=%q)\n\n", scale, prefix)
	b.WriteString("Generated by `tools/terralith-gen` (issue #564). Rewritten in full on every run.\n\n")
	fmt.Fprintf(&b, "%d teams, %d service(s), %d DNS record(s) + 1 zone, plus a fixed 3-resource\n", teams, services, dnsRecords)
	b.WriteString("network layer (1 VPC, 1 subnet, 1 security group) and 1 ECS cluster.\n\n")
	b.WriteString("## Composition\n\n")
	fmt.Fprintf(&b, "| Bucket | Count | Share |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| identity | %d | %.1f%% |\n", c.identityResources, c.identityPercent())
	fmt.Fprintf(&b, "| container | %d | %.1f%% |\n", c.containerResources, 100*float64(c.containerResources)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| dns | %d | %.1f%% |\n", c.dnsResources, 100*float64(c.dnsResources)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| supporting (network) | %d | %.1f%% |\n", c.supportingResources, 100*float64(c.supportingResources)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| **total** | **%d** | |\n\n", c.totalResources())
	fmt.Fprintf(&b, "Role/policy duplication (see composition's doc comment for the exact method): %d/%d blocks measured duplicate (%.1f%%).\n\n",
		c.duplicateRolePolicyBlocks, c.totalRolePolicyBlocks, c.duplicationPercent())
	b.WriteString("This is stock Terraform: no choudoufu-specific configuration block, ownership\nsidecar, or resource-tag marker appears anywhere in this output - see\nshape_test.go's TestNoChoudoufuConstructLeaks for the mechanical check.\n")
	return b.String()
}
