// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// # What "service family" means here, and what is approximated
//
// live/COVERAGE.md's weighting note names twelve families by their
// CloudFormation-registry shape (EC2/VPC is one CFN namespace, EC2 - VPC
// resources are filed there too; SQS/SNS and EKS/ECS are two namespaces
// each, named together because both members are individually small; ELB
// merges CloudFormation's classic ElasticLoadBalancing namespace with
// ElasticLoadBalancingV2). This generator resolves a type's CFN namespace
// from live/mapping.json's own cfn_type field (or fold_parent's, for a fold
// child) - the same registry evidence tools/row-gen's own Service field
// already batches by (classify.go's serviceOf) - and matches it against
// those twelve names first.
//
// Just over two thirds of the pending-ratification population carries no
// CFN model at all (live/mapping.json's via is cfn-unmodeled or tf-only -
// COVERAGE.md's own "of those, with no CloudFormation model" row), so a
// CFN-only rule would leave most of the queue in one large "no family"
// bucket. For those types, and only for those, this generator falls back to
// the Terraform type's own prefix (the token right after "aws_") and a
// small, hand-checked alias table (priorityPrefixFallback below) that maps
// the prefixes actually observed in today's pending-ratification set onto
// the twelve named families - aws_default_vpc and aws_ebs_snapshot have no
// CFN model but are unmistakably EC2/VPC, for instance. That table is
// therefore a description of what this generator found in the pending set
// at build time, not a general AWS service taxonomy; re-run it after a
// provider bump and a genuinely new prefix will fall through to the
// non-priority path below rather than being silently mis-filed.
//
// A type outside the twelve named families keeps its resolved CFN namespace
// as its family name when it has one (batches such as "Redshift" or
// "AppMesh"), and otherwise its own TF prefix, capitalized, as a stand-in
// family name (batches such as "Quicksight" or "Pinpoint"). Those non-priority
// families are ordered after the twelve named ones, by pending-type count
// descending and then alphabetically - a determinism tie-break, not a
// measured usage claim; COVERAGE.md's weighting note states an order only
// for the twelve it names.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Committed artifact paths, repo-relative.
const (
	ReadinessJSONRel = "live/readiness.json"
	MappingJSONRel   = "live/mapping.json"
	OutputJSONRel    = "live/ratification-queue.json"
)

// GeneratedBy is the artifact's own generated_by field.
const GeneratedBy = "go run ./tools/ratification-queue-gen"

// BatchSizeTarget is issue #426's own "batches of ~25 types" figure: the
// most types a single batch (and so a single follow-up ratification issue)
// ever carries. A family with more pending types than this splits into
// several sequential batches; a family with fewer is one batch, whatever its
// size - "grouped by service family" takes precedence over hitting the
// target exactly, so a batch is never a mix of two families.
const BatchSizeTarget = 25

// PendingRatificationStatus is the live/readiness.json status token this
// queue is built from - see tools/readiness-gen/build.go's StatusPendingRatification.
const PendingRatificationStatus = "pending-ratification"

// PriorityFamilies is live/COVERAGE.md's own weighting note, quoted
// verbatim and in order: "The services estates are actually made of
// (EC2/VPC, S3, IAM, Lambda, RDS, DynamoDB, SQS/SNS, EKS/ECS, ELB, Route53,
// KMS, CloudWatch)". A family present in the pending set keeps this order;
// one of the twelve with zero pending types this run simply produces no
// batch.
var PriorityFamilies = []string{
	"EC2/VPC", "S3", "IAM", "Lambda", "RDS", "DynamoDB",
	"SQS/SNS", "EKS/ECS", "ELB", "Route53", "KMS", "CloudWatch",
}

// Artifact is live/ratification-queue.json's shape.
type Artifact struct {
	GeneratedBy string `json:"generated_by"`
	Issue       string `json:"issue"`
	Purpose     string `json:"purpose"`

	Inputs           Inputs   `json:"inputs"`
	BatchSizeTarget  int      `json:"batch_size_target"`
	PriorityFamilies []string `json:"priority_families"`

	Counts Counts `json:"counts"`

	// BatchTemplate is issue #426's own "Batch template" section, carried
	// here verbatim so a follow-up unit (or the maintainer) filing the
	// per-batch issues this artifact does not file has the exact title and
	// body shape without re-reading the issue - see this package's doc
	// comment and the issue itself for provenance.
	BatchTemplate BatchTemplate `json:"batch_template"`

	Batches []Batch `json:"batches"`
}

// Inputs names exactly what this generator read, so a reader can tell
// whether the artifact is stale without re-deriving anything.
type Inputs struct {
	ReadinessJSON        string `json:"readiness_json"`
	ReadinessGeneratedBy string `json:"readiness_generated_by"`
	MappingJSON          string `json:"mapping_json"`
	ProposeCommand       string `json:"propose_command"`
	ProposeSummary       string `json:"propose_summary"`
}

// Counts is the queue's own partition summary.
type Counts struct {
	// PendingRatification is live/readiness.json's
	// counts.statuses["pending-ratification"] at read time - the Accept
	// criterion this artifact's own test pins: it must equal the sum of
	// every batch's type count.
	PendingRatification int `json:"pending_ratification"`
	Batches             int `json:"batches"`
	Families            int `json:"families"`

	// TypesWithProposeEvidence is how many queued types PROPOSE's own
	// report covered this run (issue #65's high-confidence stage) - almost
	// always small, since PROPOSE only ever emits a handful of candidates
	// per run; TypesWithReadinessFactsOnly is everyone else, whose evidence
	// pointer is live/readiness.json's facts field alone.
	TypesWithProposeEvidence    int `json:"types_with_propose_evidence"`
	TypesWithReadinessFactsOnly int `json:"types_with_readiness_facts_only"`
}

// BatchTemplate is issue #426's "Batch template" section, structured.
type BatchTemplate struct {
	Note string `json:"note"`

	// TitlePattern: "ratification batch: SERVICE-FAMILY (N types)" - SERVICE-FAMILY
	// and N are filled in per batch from this artifact's own family and
	// type-count fields.
	TitlePattern string `json:"title_pattern"`

	// BodySpotCheckSteps is COVERAGE.md's four-step spot-check contract,
	// which the issue's template asks every per-batch issue to restate.
	BodySpotCheckSteps []string `json:"body_spot_check_steps"`

	Accept []string `json:"accept"`
}

// Batch is one ordered chunk of the queue: one service family (or one
// sequential slice of a family too large for BatchSizeTarget), the type
// list, and each type's evidence pointer.
type Batch struct {
	Number int    `json:"number"`
	Family string `json:"family"`
	Types  []Row  `json:"types"`
}

// Row is one queued type.
type Row struct {
	Type     string   `json:"type"`
	Tier     string   `json:"tier"`
	Evidence Evidence `json:"evidence"`
}

// Evidence is a queued type's evidence pointer. Source names which channel
// is primary; both channels are populated when both apply, so nothing PROPOSE
// or readiness.json's facts already state is lost to the primary/fallback
// choice.
type Evidence struct {
	// Source is "propose" when tools/row-gen -propose covered this type
	// this run, else "readiness-facts".
	Source string `json:"source"`

	// Propose* fields are set only when Source == "propose": the exact
	// candidate block go run ./tools/row-gen -propose printed for this
	// type, captured whole (not re-parsed field by field) so nothing is
	// lost if row-gen's own block format shifts, plus the two lines a
	// reader most wants without opening the block: the rule that fired and
	// the rule class's own track record.
	ProposeRule        string `json:"propose_rule,omitempty"`
	ProposeTrackRecord string `json:"propose_track_record,omitempty"`
	ProposeBlock       string `json:"propose_block,omitempty"`

	// The rest is live/readiness.json's own facts for this type (Facts.SurveyPath,
	// MappingVia, MappingFoldParent, NotImportable, Rejected, RejectedReason),
	// plus MappingCFNType, read directly from live/mapping.json - the same
	// artifact readiness.json's own mapping_via/mapping_fold_parent facts
	// are lifted from, so this is the sibling field that makes those two
	// legible rather than a new evidence source.
	SurveyPath        string `json:"survey_path"`
	MappingVia        string `json:"mapping_via,omitempty"`
	MappingFoldParent string `json:"mapping_fold_parent,omitempty"`
	MappingCFNType    string `json:"mapping_cfn_type,omitempty"`
	NotImportable     bool   `json:"not_importable,omitempty"`
	Rejected          bool   `json:"rejected,omitempty"`
	RejectedReason    string `json:"rejected_reason,omitempty"`
}

// -- live/readiness.json, narrowed to what this generator reads --

type readinessFacts struct {
	Taggable          bool   `json:"taggable"`
	SurveyPath        string `json:"survey_path"`
	NotImportable     bool   `json:"not_importable"`
	Rejected          bool   `json:"rejected"`
	RejectedReason    string `json:"rejected_reason,omitempty"`
	MappingVia        string `json:"mapping_via,omitempty"`
	MappingFoldParent string `json:"mapping_fold_parent,omitempty"`
}

type readinessRow struct {
	Type   string         `json:"type"`
	Tier   string         `json:"tier"`
	Status string         `json:"status"`
	Facts  readinessFacts `json:"facts"`
}

type readinessArtifact struct {
	GeneratedBy string `json:"generated_by"`
	Counts      struct {
		Statuses map[string]int `json:"statuses"`
	} `json:"counts"`
	Types []readinessRow `json:"types"`
}

// -- live/mapping.json, narrowed to what this generator reads --

type mappingRow struct {
	TFType     string  `json:"tf_type"`
	CFNType    *string `json:"cfn_type"`
	Via        string  `json:"via"`
	FoldParent *string `json:"fold_parent"`
}

type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

// repoRoot resolves the checkout's root from this file's own location, the
// same trick every other tools/*-gen's repoRoot uses.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at tools/ratification-queue-gen/build.go.
	return filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
}

func decodeJSON(root, rel string, v any) error {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(root, filepath.FromSlash(rel))))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decoding %s: %w", rel, err)
	}
	return nil
}

func loadReadiness(root string) (*readinessArtifact, error) {
	var a readinessArtifact
	if err := decodeJSON(root, ReadinessJSONRel, &a); err != nil {
		return nil, err
	}
	if len(a.Types) == 0 {
		return nil, fmt.Errorf("%s decoded to zero types; the shape this generator reads has changed", ReadinessJSONRel)
	}
	return &a, nil
}

func loadMapping(root string) (map[string]mappingRow, error) {
	var m mappingArtifact
	if err := decodeJSON(root, MappingJSONRel, &m); err != nil {
		return nil, err
	}
	if len(m.Rows) == 0 {
		return nil, fmt.Errorf("%s decoded to zero rows; the shape this generator reads has changed", MappingJSONRel)
	}
	out := make(map[string]mappingRow, len(m.Rows))
	for _, r := range m.Rows {
		out[r.TFType] = r
	}
	return out, nil
}

// runPropose shells out to `go run ./tools/row-gen -propose` the same way
// tools/admission-pipeline/propose.go does (row-gen is its own package
// main), and returns its captured stdout (the report) and stderr's one-line
// summary.
func runPropose(root string) (report, summary string, err error) {
	cmd := exec.Command("go", "run", "./tools/row-gen", "-propose") //nolint:gosec // fixed args, no user input
	cmd.Dir = root
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PWD=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("go run ./tools/row-gen -propose: %w\nstderr:\n%s", err, stderr.String())
	}
	for _, line := range strings.Split(stderr.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "row-gen -propose:") {
			summary = line
			break
		}
	}
	return stdout.String(), summary, nil
}

// Build reads live/readiness.json and live/mapping.json, runs
// `go run ./tools/row-gen -propose`, and assembles the ordered queue. It
// writes nothing under internal/live or tools/row-gen; live/readiness.json
// itself is read-only input.
func Build(root string) (Artifact, error) {
	readiness, err := loadReadiness(root)
	if err != nil {
		return Artifact{}, err
	}
	mapping, err := loadMapping(root)
	if err != nil {
		return Artifact{}, err
	}
	proposeReport, proposeSummary, err := runPropose(root)
	if err != nil {
		return Artifact{}, err
	}
	proposeCandidates, err := parseProposeCandidates(proposeReport)
	if err != nil {
		return Artifact{}, err
	}

	var pending []readinessRow
	for _, r := range readiness.Types {
		if r.Status == PendingRatificationStatus {
			pending = append(pending, r)
		}
	}
	declaredPending := readiness.Counts.Statuses[PendingRatificationStatus]
	if len(pending) != declaredPending {
		return Artifact{}, fmt.Errorf("%s lists %d types with status %q but its own counts.statuses says %d; one of the two is stale",
			ReadinessJSONRel, len(pending), PendingRatificationStatus, declaredPending)
	}

	rows := make([]Row, 0, len(pending))
	familyOfType := make(map[string]string, len(pending))
	proposeEvidenceCount := 0
	for _, p := range pending {
		m := mapping[p.Type]
		cfnService := ""
		if m.CFNType != nil {
			cfnService = cfnNamespace(*m.CFNType)
		} else if m.FoldParent != nil {
			cfnService = cfnNamespace(*m.FoldParent)
		}
		familyOfType[p.Type] = familyOf(p.Type, cfnService)

		ev := Evidence{
			SurveyPath:        p.Facts.SurveyPath,
			MappingVia:        p.Facts.MappingVia,
			MappingFoldParent: p.Facts.MappingFoldParent,
			NotImportable:     p.Facts.NotImportable,
			Rejected:          p.Facts.Rejected,
			RejectedReason:    p.Facts.RejectedReason,
		}
		if m.CFNType != nil {
			ev.MappingCFNType = *m.CFNType
		} else if m.FoldParent != nil {
			ev.MappingCFNType = *m.FoldParent
		}
		if c, ok := proposeCandidates[p.Type]; ok {
			ev.Source = "propose"
			ev.ProposeRule = c.Rule
			ev.ProposeTrackRecord = c.TrackRecord
			ev.ProposeBlock = c.Block
			proposeEvidenceCount++
		} else {
			ev.Source = "readiness-facts"
		}

		rows = append(rows, Row{Type: p.Type, Tier: p.Tier, Evidence: ev})
	}

	batches := buildBatches(rows, familyOfType)

	counts := Counts{
		PendingRatification:         len(rows),
		Batches:                     len(batches),
		TypesWithProposeEvidence:    proposeEvidenceCount,
		TypesWithReadinessFactsOnly: len(rows) - proposeEvidenceCount,
	}
	families := map[string]bool{}
	for _, b := range batches {
		families[b.Family] = true
	}
	counts.Families = len(families)

	return Artifact{
		GeneratedBy: GeneratedBy,
		Issue:       "#426",
		Purpose:     "The ordered ratification worklist: every live/readiness.json pending-ratification type, batched by service family in COVERAGE.md's usage-weighted order, with each type's evidence pointer. Produces no admission itself - see this file's package doc comment for the family rule and tools/row-gen/propose.go for the PROPOSE evidence channel.",
		Inputs: Inputs{
			ReadinessJSON:        ReadinessJSONRel,
			ReadinessGeneratedBy: readiness.GeneratedBy,
			MappingJSON:          MappingJSONRel,
			ProposeCommand:       "go run ./tools/row-gen -propose",
			ProposeSummary:       proposeSummary,
		},
		BatchSizeTarget:  BatchSizeTarget,
		PriorityFamilies: append([]string{}, PriorityFamilies...),
		Counts:           counts,
		BatchTemplate:    batchTemplate(),
		Batches:          batches,
	}, nil
}

// cfnNamespace pulls the namespace segment out of a CFN type name
// (AWS::Lambda::Function -> "Lambda") - the same rule tools/row-gen/classify.go's
// serviceOf uses, duplicated rather than imported since row-gen is its own
// package main (this file's own doc comment on why every caller shells out
// instead).
func cfnNamespace(cfnType string) string {
	parts := strings.Split(cfnType, "::")
	if len(parts) >= 2 {
		return parts[1]
	}
	return cfnType
}

// priorityPrefixFallback maps a TF type's prefix (the token right after
// "aws_") onto one of the twelve PriorityFamilies, for a type with no CFN
// model at all - see this file's package doc comment for what this table is
// and is not. Checked only when the type has no resolved CFN namespace.
var priorityPrefixFallback = map[string]string{
	"ec2": "EC2/VPC", "vpc": "EC2/VPC", "ebs": "EC2/VPC", "ami": "EC2/VPC",
	"eip": "EC2/VPC", "default": "EC2/VPC", "spot": "EC2/VPC", "snapshot": "EC2/VPC",

	"s3": "S3", "s3control": "S3", "s3tables": "S3", "s3files": "S3", "s3vectors": "S3",

	"iam":    "IAM",
	"lambda": "Lambda",
	"rds":    "RDS", "db": "RDS",
	"dynamodb": "DynamoDB",

	"sqs": "SQS/SNS", "sns": "SQS/SNS",
	"eks": "EKS/ECS", "ecs": "EKS/ECS",
	"elb": "ELB", "alb": "ELB", "lb": "ELB",

	"route53": "Route53", "route53domains": "Route53", "route53profiles": "Route53",
	"route53recoverycontrolconfig": "Route53", "route53recoveryreadiness": "Route53",

	"kms":        "KMS",
	"cloudwatch": "CloudWatch",
}

// priorityFamilyForCFNNamespace matches a resolved CFN namespace against
// the twelve PriorityFamilies - see the package doc comment.
func priorityFamilyForCFNNamespace(ns string) (string, bool) {
	switch {
	case ns == "EC2":
		return "EC2/VPC", true
	case ns == "S3" || strings.HasPrefix(ns, "S3"):
		return "S3", true
	case ns == "IAM":
		return "IAM", true
	case ns == "Lambda":
		return "Lambda", true
	case ns == "RDS":
		return "RDS", true
	case ns == "DynamoDB":
		return "DynamoDB", true
	case ns == "SQS" || ns == "SNS":
		return "SQS/SNS", true
	case ns == "EKS" || ns == "ECS":
		return "EKS/ECS", true
	case ns == "ElasticLoadBalancing" || ns == "ElasticLoadBalancingV2":
		return "ELB", true
	case ns == "Route53" || strings.HasPrefix(ns, "Route53"):
		return "Route53", true
	case ns == "KMS":
		return "KMS", true
	case ns == "CloudWatch" || ns == "Logs":
		return "CloudWatch", true
	default:
		return "", false
	}
}

// tfPrefix is the token right after "aws_" in a TF type name.
func tfPrefix(tfType string) string {
	s := strings.TrimPrefix(tfType, "aws_")
	if i := strings.IndexByte(s, '_'); i >= 0 {
		return s[:i]
	}
	return s
}

// familyOf assigns one type's family: a CFN namespace match against the
// twelve priority families first, then the TF-prefix fallback table (only
// when the type has no CFN namespace at all), then the type's own resolved
// CFN namespace or, failing that, its own TF prefix capitalized - see the
// package doc comment.
func familyOf(tfType, cfnNamespaceOfType string) string {
	if cfnNamespaceOfType != "" {
		if fam, ok := priorityFamilyForCFNNamespace(cfnNamespaceOfType); ok {
			return fam
		}
		return cfnNamespaceOfType
	}
	prefix := tfPrefix(tfType)
	if fam, ok := priorityPrefixFallback[prefix]; ok {
		return fam
	}
	if prefix == "" {
		return "other"
	}
	return strings.ToUpper(prefix[:1]) + prefix[1:]
}

// priorityRank returns the PriorityFamilies index, or -1 for a non-priority
// family.
func priorityRank(family string) int {
	for i, f := range PriorityFamilies {
		if f == family {
			return i
		}
	}
	return -1
}

// buildBatches orders every family (priority families first, in
// PriorityFamilies' fixed order, then the rest by pending-type count
// descending and then name ascending as a determinism tie-break), sorts
// each family's types by name, and chunks each family into batches of at
// most BatchSizeTarget - see BatchSizeTarget's own doc comment for why a
// batch never mixes two families.
func buildBatches(rows []Row, familyOfType map[string]string) []Batch {
	byFamily := map[string][]Row{}
	for _, r := range rows {
		f := familyOfType[r.Type]
		byFamily[f] = append(byFamily[f], r)
	}
	for f := range byFamily {
		rs := byFamily[f]
		sort.Slice(rs, func(i, j int) bool { return rs[i].Type < rs[j].Type })
		byFamily[f] = rs
	}

	families := make([]string, 0, len(byFamily))
	for f := range byFamily {
		families = append(families, f)
	}
	sort.Slice(families, func(i, j int) bool {
		ri, rj := priorityRank(families[i]), priorityRank(families[j])
		iPriority, jPriority := ri >= 0, rj >= 0
		if iPriority != jPriority {
			return iPriority // priority families sort before non-priority ones
		}
		if iPriority && jPriority {
			return ri < rj // fixed COVERAGE.md order
		}
		// Both non-priority: count descending, then name ascending.
		if len(byFamily[families[i]]) != len(byFamily[families[j]]) {
			return len(byFamily[families[i]]) > len(byFamily[families[j]])
		}
		return families[i] < families[j]
	})

	var batches []Batch
	n := 0
	for _, f := range families {
		rs := byFamily[f]
		for start := 0; start < len(rs); start += BatchSizeTarget {
			end := start + BatchSizeTarget
			if end > len(rs) {
				end = len(rs)
			}
			n++
			batches = append(batches, Batch{Number: n, Family: f, Types: rs[start:end]})
		}
	}
	return batches
}

// batchTemplate is issue #426's own "Batch template" section, structured -
// see this file's package doc comment.
func batchTemplate() BatchTemplate {
	return BatchTemplate{
		Note:         "Issue #426's 'Batch template' section, instantiated once per batch when a follow-up unit files the per-batch issues: SERVICE-FAMILY is the batch's family field, N is len(types).",
		TitlePattern: "ratification batch: SERVICE-FAMILY (N types)",
		BodySpotCheckSteps: []string{
			"Open the provider documentation's Import section (or Identity Schema block, where the evidence pointer says the entry came from precedence evidence) for the type and confirm the pasted argument or attribute is what that section documents.",
			"Confirm the type creates or exports no credential material. If it does, do not paste it - record the rejection by name in tools/row-gen/rejected.json with a reason, so PROPOSE and future batches never offer it again.",
			"Paste the printed block(s) unedited into tools/row-gen/ratified.json, then re-run `go run ./tools/row-gen -emit`.",
			"Build the cohort estate, run the suites, and get a floci probe before merging - the same as any hand-ratified batch.",
		},
		Accept: []string{
			"Types admitted with fixtures and tests.",
			"Golden diff (internal/live/check's TestIdentityGolden) explained line by line.",
			"live/readiness.json regenerated and docs re-rendered in the same PR.",
			"Per-type evidence listed in the PR description.",
		},
	}
}
