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

	"github.com/hashicorp/hcl/v2/hclwrite"
)

// Composition, as a function of scale (issue #564's "one flag" requirement).
//
// Every count below is linear in scale, so "the same estate at 4x" is
// -scale 4 rather than a different artifact, and the proportions this
// produces hold (approximately - see buildEstate's doc comment on the
// fixed, non-scaling supporting layer) as scale grows.
//
//	teams      = teamsPerScale * scale      (6 identity resources each, individually named)
//	services   = servicesPerScale * scale   (2 identity + 2 container each)
//	records    = dnsRecordsPerScale * scale (one for_each'd resource, see writeRecords)
//	countTeams = countTeamsPerScale * scale (6 resource declarations, each count = countTeams)
//	podTeams   = len(modulePodKeys) * podSizePerScale * scale (module-nested, see buildModulePods)
//
// At scale=1 (the "genuinely small tier" #564 asks for): 6 named teams (36
// identity resources), 1 service (2 identity + 2 container), 10 DNS
// records + 1 zone (all 10 records from one for_each block, not 10 named
// blocks - issue #574), 2 count-expanded teams (12 identity resources from
// one `count`-carrying block set - issue #574), 2 module-nested pods of 1
// team-equivalent each (12 identity resources, issue #574's "hardest
// shape" - see buildModulePods), plus a fixed layer that does not scale
// with it - 1 VPC, 1 subnet, 1 security group (counted as "supporting")
// and 1 ECS cluster (counted as "container") - because it is realistic for
// many services to share one VPC and one cluster rather than minting a new
// one per service. See composition.identityPercent and gen_test.go's
// TestIdentityShareApproximatesTarget for the measured share at larger
// scale.
const (
	teamsPerScale      = 6
	servicesPerScale   = 1
	dnsRecordsPerScale = 10

	// countTeamsPerScale is the identity layer's `count`-expanded share
	// (issue #574): one set of six resource declarations, each carrying
	// `count = countTeamsPerScale * scale`, so a single HCL block produces
	// that many near-identical live instances distinguished only by
	// count.index - see buildCountTeams.
	countTeamsPerScale = 2

	// podSizePerScale is how many team-equivalents each module-nested pod
	// instance declares via its OWN internal `count` (issue #574's
	// module-nested share) - see buildModulePods and modulePodKeys.
	podSizePerScale = 1
)

// modulePodKeys are the for_each keys the root module call over
// modules/team_pod uses. Fixed at two regardless of scale, so the module
// call always has more than one instance - the shape
// internal/live/markers/modulemarker.go's marker_module_prefix exists to
// serve (issue #378): a module call whose several instances share one HCL
// body per resource, where the resource inside ALSO carries its own
// `count` (podSizePerScale, above). module.team_pod["pod-a"].aws_iam_role.
// pod_role[0] is exactly that double-indexed shape - issue #574's "at
// least one module-nested expansion... the hardest shape."
var modulePodKeys = []string{"pod-a", "pod-b"}

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

	// Expansion counters, issue #574: how many resource INSTANCES (not HCL
	// blocks) each meta-argument shape accounts for. All three are already
	// included in identityResources/dnsResources above - these exist to
	// report the proportions #574 asks for (see generatedMD and main.go),
	// not to be summed again.
	countExpandedInstances   int // buildCountTeams: one block set, `count = n`, root level
	forEachExpandedInstances int // writeRecords: one block, `for_each` over a map, root level
	moduleNestedInstances    int // buildModulePods: module call (for_each) whose body ALSO carries `count`
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

// expandedPercent is what share of every resource this run generated came
// from a meta-argument-expanded block (count, for_each, or module-nested)
// rather than an individually-named one - issue #574's headline shape
// question, reported next to identityPercent/duplicationPercent rather than
// asserted, the same convention every other composition metric here uses.
func (c composition) expandedPercent() float64 {
	t := c.totalResources()
	if t == 0 {
		return 0
	}
	expanded := c.countExpandedInstances + c.forEachExpandedInstances + c.moduleNestedInstances
	return 100 * float64(expanded) / float64(t)
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
// mechanical check. Every resource lives in one Terraform state - a single
// root module PLUS one module call (issue #574, "modules/team_pod"),
// wrapped with for_each so it has more than one instance: still a single
// state, exercising the module-nested marker-address shape
// (internal/live/markers/modulemarker.go) rather than the multi-state
// decomposition #546's last comment contrasts a terralith with.
func buildEstate(scale int, prefix string) *estate {
	teams := teamsPerScale * scale
	services := servicesPerScale * scale
	dnsRecords := dnsRecordsPerScale * scale
	countTeams := countTeamsPerScale * scale
	podSize := podSizePerScale * scale

	dup := newDupTracker()

	var iam strings.Builder
	comp := composition{}

	// ── Identity layer: teams ────────────────────────────────────────────
	for i := 0; i < teams; i++ {
		n := buildTeam(&iam, i, prefix, dup)
		comp.identityResources += n
	}

	// ── Identity layer: count-expanded teams (issue #574) ────────────────
	iam.WriteString("\n# count-expanded teams (issue #574): one block set per resource type,\n" +
		"# count = " + fmt.Sprintf("%d", countTeams) + ", near-identical instances distinguished only by\n" +
		"# count.index - the idiom docs/use/migrate.md's manual content-matching\n" +
		"# adoption loop cannot offer for adoption (an indexed instance is never\n" +
		"# matched), unlike live-import which reads identity from state directly.\n\n")
	countN := buildCountTeams(&iam, prefix, countTeams)
	comp.identityResources += countN
	comp.countExpandedInstances += countN

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
	writeRecords(&dns, dnsRecords)
	comp.dnsResources += dnsRecords
	comp.forEachExpandedInstances += dnsRecords

	comp.totalRolePolicyBlocks = dup.total()
	comp.duplicateRolePolicyBlocks = dup.duplicates()

	// ── Identity layer: module-nested pods (issue #574) ──────────────────
	moduleN := len(modulePodKeys) * podSize * 6
	comp.identityResources += moduleN
	comp.moduleNestedInstances += moduleN

	files := map[string]string{
		"versions.tf":                   versionsTF(),
		"main.tf":                       mainTF(prefix, podSize),
		"pods.tf":                       modulePodsTF(),
		"modules/team_pod/variables.tf": podModuleVariablesTF(),
		"modules/team_pod/main.tf":      podModuleMainTF(),
		"iam.tf":                        header("iam.tf", "the identity layer: team roles/policies/attachments/profiles and each ECS service's execution role") + iam.String(),
		"network.tf":                    header("network.tf", "the one shared VPC/subnet/security group the container-service layer's tasks run in") + network.String(),
		"ecs.tf":                        header("ecs.tf", "the container-service layer: one cluster, and per service a template task definition plus a service with lifecycle.ignore_changes on task_definition") + ecs.String(),
		"dns.tf":                        header("dns.tf", "the DNS fan-out: one zone, many for_each'd records (issue #574)") + dns.String(),
		"GENERATED.md":                  generatedMD(scale, prefix, teams, services, dnsRecords, countTeams, podSize, comp),
	}

	// Canonicalize the HCL here, in this process, with the same library
	// `terraform fmt` and `tofu fmt` are built on
	// (internal/command/fmt.go's formatSourceCode).
	//
	// This used to be delegated entirely to the external `fmt` binary
	// main.go shells out to after write(), and that is the root of issue
	// #578's defect 1. The templates above are hand-aligned, hand-alignment
	// drifts from what the formatter would produce - `=` padded across a
	// multi-line jsonencode, which ends an alignment group - and the drift
	// was invisible because the external pass silently repaired it on the
	// way out. So the estate's canonical-ness depended on an OPTIONAL
	// binary: with no terraform/tofu/choudoufu on PATH, terralith-gen
	// emitted misformatted HCL and said nothing. #574 then added
	// modules/team_pod below the root, the external pass was missing
	// -recursive, and that one file lost even its silent repair.
	//
	// Formatting here removes the dependency: what this generator produces
	// is canonical at every scale and prefix whether or not any binary is
	// installed, and TestGeneratedTerralithIsCanonicallyFormatted can
	// assert it with nothing but the process it runs in. main.go's fmt pass
	// keeps its second job - the free parse of the whole tree - which is
	// #578's defect 2, and is now a check over already-canonical files
	// rather than a repair nobody was watching.
	//
	// *.tf only: GENERATED.md is Markdown.
	for name, content := range files {
		if strings.HasSuffix(name, ".tf") {
			files[name] = string(hclwrite.Format([]byte(content)))
		}
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
		dst := filepath.Join(out, name)
		// name may carry a subdirectory (issue #574: modules/team_pod/*.tf),
		// so its parent needs creating too - unlike every file before #574,
		// which all landed directly in out.
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil { //nolint:gosec // a generated Terraform fixture, not a secret
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
//
// Endpoint, region and credentials all come from the environment
// (AWS_ENDPOINT_URL, AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY),
// so the provider block below carries only the emulator-facing flags that
// have no environment-variable form - the same wiring
// live/e2e/estate/versions.tf's header describes.
//
// skip_requesting_account_id is deliberately absent, and leaving it out is
// issue #628's decision rather than an oversight.
//
// The generator used to set it. It is the only one of the three skip_*
// flags that changes what the provider KNOWS rather than what it CHECKS:
// skip_credentials_validation and skip_metadata_api_check suppress probes
// whose answers are used for nothing else, while skip_requesting_account_id
// leaves the provider's account id empty, and every identity the provider
// then composes as an ARN loses its account segment
// (arn:aws:ecs:us-east-1::cluster/<name>). That is issue #572, and since
// #596 landed choudoufu refuses on it rather than proposing a duplicate.
//
// Measured on this generator's own output, scale 1 against floci, with only
// that one line changing (both runs keep skip_credentials_validation):
//
//	with it:     choudoufu plan exits 1 in 137s, "Error: Live resource
//	             listed but not importable" on aws_ecs_task_definition
//	without it:  choudoufu plan exits 0 in 2s, "No changes. Your
//	             infrastructure matches the configuration."
//
// Three reasons it goes rather than being kept behind a flag or documented
// as a known trap (issue #628 lists all three options):
//
//  1. It is wrong against real AWS, where the account id is real and the
//     ARNs matter. live/live-cert/terralith-scale.sh already replaces this
//     whole block for that reason, citing #572 in its own header.
//  2. It is unnecessary against floci, which serves STS GetCallerIdentity
//     (account 000000000000). live/e2e/estate/versions.tf reached the same
//     conclusion first, under P2.3, from an independent symptom: with the
//     account unresolved the provider's owner-id filter on a filtered EC2
//     list goes out empty. Same flag, two unrelated breakages.
//  3. The "it is the canonical LocalStack block" objection does not survive
//     the detail. That idiom is six or seven arguments - access_key,
//     secret_key, region, an endpoints block, and the three skips - and
//     this generator emits three of them, taking the rest from the
//     environment, so it was never emitting the canonical block. A
//     stranger's real estate, which is what this generator imitates (see
//     main.go), carries none of the three skips and no endpoints override
//     at all. Dropping this one moves the fixture toward its subject rather
//     than away from it.
//
// No flag either: a knob for it would have no caller. Both harnesses that
// plan this output and assert the plan is empty - terralith-scale.sh's
// test_plan stage and internal/live/statefulcost's live test - already
// replace the whole block on the way past, and the one place that keeps the
// flag (internal/live/discovery/slicing_bench_test.go, which counts API
// calls rather than asserting a verdict) writes its own versions.tf instead
// of reading this one.
//
// provider_test.go is the guard, and it checks this against
// live/e2e/estate/versions.tf rather than against this template.
func versionsTF() string {
	return fmt.Sprintf(`# versions.tf - provider wiring only. Generated by tools/terralith-gen.
# No backend block: local state, same as any stock Terraform root module
# before migration. No "live" block anywhere in this estate - see #564.
#
# Endpoint, region and credentials come from the environment
# (AWS_ENDPOINT_URL, AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY);
# the provider block carries only the flags that have no such form.

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
  s3_use_path_style           = true

  # skip_requesting_account_id is deliberately absent (issue #628). With it
  # set the provider's account id is empty, every ARN-shaped identity it
  # composes loses its account segment, and ECS identity resolution fails
  # (issue #572) - which choudoufu now refuses on rather than proposing a
  # duplicate. Floci and real AWS both answer STS GetCallerIdentity, so
  # letting the provider ask costs one request and keeps this fixture
  # plannable. Same call live/e2e/estate/versions.tf made under P2.3.
}
`, providerSource, providerVersion)
}

func mainTF(prefix string, podSize int) string {
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

  # pod_size (issue #574): how many team-equivalents each module-nested
  # pod instance (pods.tf, modules/team_pod) declares via its own internal
  # count. Passed through rather than hardcoded in pods.tf so it scales
  # with -scale like every other bucket.
  pod_size = %d
}
`, prefix, podSize)
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

// buildCountTeams writes the identity layer's `count`-expanded share
// (issue #574): the same six resource TYPES buildTeam writes per named
// team, but as one declaration per type carrying `count = n`, so n
// team-equivalents come from six written blocks total rather than 6*n.
// Content is deliberately uniform across instances (one trust principal,
// one policy body) except for the name, which is derived from count.index
// - the point of `count` is that its instances are near-identical, not
// that this reproduces buildTeam's boilerplate/scoped alternation inside
// an HCL ternary. Returns 6*n, the identity resource instances produced.
func buildCountTeams(w *strings.Builder, prefix string, n int) int {
	fmt.Fprintf(w, `resource "aws_iam_role" "count_team" {
  count              = %d
  name               = "%s-count-team-${format("%%04d", count.index)}-role"
  assume_role_policy = %s
}

`, n, prefix, assumeRolePolicyHCL("ec2.amazonaws.com"))

	fmt.Fprintf(w, `resource "aws_iam_role_policy" "count_team_inline" {
  count  = %d
  name   = "%s-count-team-${format("%%04d", count.index)}-inline"
  role   = aws_iam_role.count_team[count.index].name
  policy = %s
}

`, n, prefix, inlinePolicyHCL(inlineTemplates[0].actions))

	fmt.Fprintf(w, `resource "aws_iam_policy" "count_team_policy" {
  count  = %d
  name   = "%s-count-team-${format("%%04d", count.index)}-policy"
  policy = %s
}

`, n, prefix, boilerplatePolicyHCL(boilerplatePolicies[0].actions))

	fmt.Fprintf(w, `resource "aws_iam_role_policy_attachment" "count_team_managed_attach" {
  count      = %d
  role       = aws_iam_role.count_team[count.index].name
  policy_arn = %q
}

`, n, managedPolicyARNs[0])

	fmt.Fprintf(w, `resource "aws_iam_role_policy_attachment" "count_team_custom_attach" {
  count      = %d
  role       = aws_iam_role.count_team[count.index].name
  policy_arn = aws_iam_policy.count_team_policy[count.index].arn
}

`, n)

	fmt.Fprintf(w, `resource "aws_iam_instance_profile" "count_team_profile" {
  count = %d
  name  = "%s-count-team-${format("%%04d", count.index)}-profile"
  role  = aws_iam_role.count_team[count.index].name
}

`, n, prefix)

	return 6 * n
}

// ── Identity layer: module-nested pods (issue #574) ─────────────────────

// modulePodsTF is the root module call: for_each over modulePodKeys, so it
// always has more than one instance, wrapping modules/team_pod (see
// podModuleMainTF) - the module-nested identity bucket. prefix and
// pod_size are threaded through as variables since a module body has no
// access to the caller's locals.
func modulePodsTF() string {
	keys := make([]string, len(modulePodKeys))
	for i, k := range modulePodKeys {
		keys[i] = fmt.Sprintf("%q", k)
	}
	return fmt.Sprintf(`# pods.tf - the identity layer's module-nested share (issue #574): a
# module call with more than one instance (for_each over %d pod keys),
# each instance declaring its own count-expanded team-equivalents
# internally (modules/team_pod/main.tf). module.team_pod["pod-a"].
# aws_iam_role.pod_role[0] is exactly the double-indexed shape
# internal/live/markers/modulemarker.go's marker_module_prefix exists to
# serve (issue #378) - the "hardest shape" issue #574 asks this generator
# to produce at least one of.

module "team_pod" {
  source = "./modules/team_pod"

  for_each = toset([%s])

  prefix   = "${local.name_prefix}-${each.key}"
  pod_size = local.pod_size
}
`, len(modulePodKeys), strings.Join(keys, ", "))
}

func podModuleVariablesTF() string {
	return `# modules/team_pod/variables.tf - generated by tools/terralith-gen
# (issue #574). Rewritten in full on every run.

variable "prefix" {
  type = string
}

variable "pod_size" {
  type = number
}
`
}

// podModuleMainTF is the wrapped module's own body: the same six resource
// types buildCountTeams writes at the root, expressed with var.prefix and
// var.pod_size in place of the literal prefix/n a root-level count block
// would use - a module has no access to the caller's locals, only what it
// declares as variables (podModuleVariablesTF).
func podModuleMainTF() string {
	return fmt.Sprintf(`# modules/team_pod/main.tf - generated by tools/terralith-gen (issue
# #574). Rewritten in full on every run. One pod's team-equivalents,
# count-expanded inside a module call that itself has more than one
# instance (pods.tf) - the module-nested marker-address shape.

resource "aws_iam_role" "pod_role" {
  count              = var.pod_size
  name               = "${var.prefix}-team-${format("%%04d", count.index)}-role"
  assume_role_policy = %s
}

resource "aws_iam_role_policy" "pod_inline" {
  count  = var.pod_size
  name   = "${var.prefix}-team-${format("%%04d", count.index)}-inline"
  role   = aws_iam_role.pod_role[count.index].name
  policy = %s
}

resource "aws_iam_policy" "pod_policy" {
  count  = var.pod_size
  name   = "${var.prefix}-team-${format("%%04d", count.index)}-policy"
  policy = %s
}

resource "aws_iam_role_policy_attachment" "pod_managed_attach" {
  count      = var.pod_size
  role       = aws_iam_role.pod_role[count.index].name
  policy_arn = %q
}

resource "aws_iam_role_policy_attachment" "pod_custom_attach" {
  count      = var.pod_size
  role       = aws_iam_role.pod_role[count.index].name
  policy_arn = aws_iam_policy.pod_policy[count.index].arn
}

resource "aws_iam_instance_profile" "pod_profile" {
  count = var.pod_size
  name  = "${var.prefix}-team-${format("%%04d", count.index)}-profile"
  role  = aws_iam_role.pod_role[count.index].name
}
`, assumeRolePolicyHCL("ec2.amazonaws.com"), inlinePolicyHCL(inlineTemplates[1].actions),
		boilerplatePolicyHCL(boilerplatePolicies[1].actions), managedPolicyARNs[1])
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

// writeRecords writes the whole DNS fan-out as a single for_each block over
// a literal locals map (issue #574's "share expanded with for_each over a
// map" bullet - the DNS fan-out is the natural fit the issue names
// explicitly). n named blocks (one per record, before #574) becomes one
// resource block plus an n-entry map; per-key content is unchanged from
// the original cycling (A/CNAME/TXT by index, still declaration-carried
// per #564 - never computed from a data source or another resource's
// attribute).
func writeRecords(w *strings.Builder, n int) {
	var entries strings.Builder
	for k := 0; k < n; k++ {
		key := fmt.Sprintf("host-%04d", k)
		var recType, value string
		switch k % 3 {
		case 0:
			recType = "A"
			value = fmt.Sprintf("[%q]", fmt.Sprintf("10.60.%d.%d", (k/256)%256, k%256))
		case 1:
			recType = "CNAME"
			value = fmt.Sprintf("[%q]", fmt.Sprintf("target-%04d.upstream.example.com.", k))
		default:
			// TXT: the AWS provider (hashicorp/aws, tested at 6.59.0) already
			// quote-wraps a TXT record's value itself - Route53 requires each
			// TXT value to carry a literal pair of double quotes, and the
			// provider adds that pair, so the text inside this one-element
			// HCL list must be the bare text with none of its own. Two
			// earlier versions of this line pre-wrapped the text in a
			// literal quote pair before the %q that renders the HCL string
			// literal (the scalar `records = ["..."]` form before #574, and
			// the map entry's list value after it), producing a value the
			// provider then quoted AGAIN on top of - real AWS rejects the
			// resulting double-quoted content with "InvalidCharacterString
			// (Value should be enclosed in quotation marks) encountered with
			// '""v=textNNNN""'" (issue #567's live-AWS run against a real
			// account, 2026-08-30); floci accepted the malformed value
			// silently, which is why this was never caught by #564/#565/#566's
			// floci-only measurements. A/CNAME never had the problem: their
			// values carry no quote pair of their own either.
			recType = "TXT"
			value = fmt.Sprintf("[%q]", fmt.Sprintf("v=text%04d", k))
		}
		fmt.Fprintf(&entries, "    %q = { type = %q, value = %s }\n", key, recType, value)
	}

	fmt.Fprintf(w, `locals {
  dns_records = {
%s  }
}

resource "aws_route53_record" "record" {
  for_each = local.dns_records

  zone_id = aws_route53_zone.main.zone_id
  name    = "${each.key}.${aws_route53_zone.main.name}"
  type    = each.value.type
  ttl     = 300
  records = each.value.value
}

`, entries.String())
}

// ── GENERATED.md ─────────────────────────────────────────────────────────

func generatedMD(scale int, prefix string, teams, services, dnsRecords, countTeams, podSize int, c composition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# terralith (scale=%d, prefix=%q)\n\n", scale, prefix)
	b.WriteString("Generated by `tools/terralith-gen` (issue #564; expansion added by #574). Rewritten in full on every run.\n\n")
	fmt.Fprintf(&b, "%d named teams, %d count-expanded team(s) (count=%d), %d module-nested pod(s) of\n", teams, countTeams, countTeams, len(modulePodKeys))
	fmt.Fprintf(&b, "%d team(s) each, %d service(s), %d DNS record(s) (one for_each block) + 1 zone,\n", podSize, services, dnsRecords)
	b.WriteString("plus a fixed 3-resource network layer (1 VPC, 1 subnet, 1 security group) and\n1 ECS cluster.\n\n")
	b.WriteString("## Composition\n\n")
	fmt.Fprintf(&b, "| Bucket | Count | Share |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| identity | %d | %.1f%% |\n", c.identityResources, c.identityPercent())
	fmt.Fprintf(&b, "| container | %d | %.1f%% |\n", c.containerResources, 100*float64(c.containerResources)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| dns | %d | %.1f%% |\n", c.dnsResources, 100*float64(c.dnsResources)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| supporting (network) | %d | %.1f%% |\n", c.supportingResources, 100*float64(c.supportingResources)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| **total** | **%d** | |\n\n", c.totalResources())
	fmt.Fprintf(&b, "Role/policy duplication (see composition's doc comment for the exact method): %d/%d blocks measured duplicate (%.1f%%).\n\n",
		c.duplicateRolePolicyBlocks, c.totalRolePolicyBlocks, c.duplicationPercent())
	b.WriteString("## Expansion (issue #574)\n\n")
	b.WriteString("How many of the resource instances above come from a meta-argument-expanded\nblock rather than an individually-named one - the axis #566's migration\nmeasurement could not test until this generator produced it:\n\n")
	fmt.Fprintf(&b, "| Shape | Instances | Share of total |\n|---|---|---|\n")
	fmt.Fprintf(&b, "| `count` (root, aws_iam_role.count_team etc.) | %d | %.1f%% |\n", c.countExpandedInstances, 100*float64(c.countExpandedInstances)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| `for_each` over a map (root, aws_route53_record.record) | %d | %.1f%% |\n", c.forEachExpandedInstances, 100*float64(c.forEachExpandedInstances)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| module-nested `count` (module.team_pod[...].aws_iam_role.pod_role[...]) | %d | %.1f%% |\n", c.moduleNestedInstances, 100*float64(c.moduleNestedInstances)/float64(c.totalResources()))
	fmt.Fprintf(&b, "| **expanded, total** | **%d** | **%.1f%%** |\n\n", c.countExpandedInstances+c.forEachExpandedInstances+c.moduleNestedInstances, c.expandedPercent())
	b.WriteString("This is stock Terraform: no choudoufu-specific configuration block, ownership\nsidecar, or resource-tag marker appears anywhere in this output - see\nshape_test.go's TestNoChoudoufuConstructLeaks for the mechanical check.\n")
	return b.String()
}
