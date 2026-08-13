// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package residue is the residue roster (issue #49): the named, printable
// set of resource types "not covered" resolves to, and why. It answers the
// question live/LIMITATIONS.md's untaggable list already answers for one
// narrow case ("which admitted types can't be swept, and why"), generalized
// to every type a configuration can name.
//
// Two consumers read this package:
//
//   - tools/survey-gen renders live/LIMITATIONS.md's residue-roster span
//     from the accessors below, and a drift test holds the doc to them.
//   - internal/live/lint's admission refusal calls [Lookup] to name a
//     refused type's cohort, when it has one, in the refusal Detail.
//
// Two cohorts (deprecated services and CFN-only constructs) and one roster
// (emulator-blocked) are hand data: no schema anywhere says a service is
// retired, that a CloudFormation type is a template mechanism rather than
// an infrastructure resource, or that an emulator gap blocks a type from
// e2e proof. Those three judgments are curated below, with their evidence
// in comments, the same way internal/live/lint/admission.go's opsExcluded
// carries the credential and waiter judgments no schema can prove.
//
// The other two cohorts (unmapped TF types and registry-laggard live
// services) are entirely computed, from the two artifacts embedded below:
// live/mapping.json (issue #43) and live/registry.json (issue #42). They
// are embedded, not copied into a hand-written table, so there is no
// second copy of the join to fall out of sync with the committed
// artifacts — a regenerated mapping.json or registry.json changes this
// package's answers the moment it is committed, with no separate
// regeneration step of its own.
package residue

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed mapping.json
var mappingJSONBytes []byte

//go:embed registry.json
var registryJSONBytes []byte

// mappingRow is live/mapping.json's row shape (tools/mapping-gen/mapping.go's
// Row, re-read here rather than imported: that package is a `main` package
// and cannot be imported).
type mappingRow struct {
	TFType     string  `json:"tf_type"`
	CFNType    *string `json:"cfn_type"`
	Via        string  `json:"via"`
	FoldParent *string `json:"fold_parent"`
	Note       *string `json:"note"`
}

type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

// registryType is the slice of live/registry.json's per-type shape this
// package reads: the type name and its handler set. tools/registry-gen's
// own Type carries more fields; only these two matter for the residue roster.
type registryType struct {
	TypeName string          `json:"type_name"`
	Handlers registryHandler `json:"handlers"`
}

type registryHandler struct {
	Create bool `json:"create"`
	Read   bool `json:"read"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
	List   bool `json:"list"`
}

func (h registryHandler) none() bool {
	return !h.Create && !h.Read && !h.Update && !h.Delete && !h.List
}

type registryArtifact struct {
	Types []registryType `json:"types"`
}

// Cohort names one of the exclusion cohorts a resource type can fall into.
// A type belongs to at most one cohort; [Lookup] resolves ties in the order
// the cohorts are checked, most specific first.
type Cohort string

const (
	// CohortDeprecated is a type in a service AWS has retired, deprecated,
	// or is winding down. See [DeprecatedServices].
	CohortDeprecated Cohort = "deprecated-service"

	// CohortCFNOnly is a CloudFormation Registry construct with no
	// Terraform counterpart at all — never reachable from this package's
	// TF-side [Lookup], since no configuration can name a type Terraform
	// does not have, but still part of the roster for the doc side. See
	// [CFNOnlyConstructs].
	CohortCFNOnly Cohort = "cfn-only"

	// CohortUnmapped is a TF type live/mapping.json's join found no CFN
	// counterpart for (via "none").
	CohortUnmapped Cohort = "unmapped"

	// CohortRegistryLaggard is a TF type mapped to a CFN type whose
	// Registry entry ships no working handler at all, so the registry-
	// backed admission path (issue #40) cannot reach it.
	CohortRegistryLaggard Cohort = "registry-laggard"

	// CohortEmulatorBlocked is a type wireable against real AWS but not
	// provable against the floci emulator (issue #26). See
	// [EmulatorBlocked].
	CohortEmulatorBlocked Cohort = "emulator-blocked"
)

// DeprecatedService is one curated entry in [DeprecatedServices]: an AWS
// service prefix, on both sides of the TF/CFN naming, judged out of scope
// by policy.
type DeprecatedService struct {
	// TFPrefix is the Terraform provider-local type prefix this service's
	// resources share, e.g. "aws_pinpoint_". Used by [Lookup].
	TFPrefix string

	// CFNPrefix is the CloudFormation Registry type-name prefix the same
	// service shares, e.g. "AWS::Pinpoint::". Used to count the service's
	// registry-side footprint for the doc, independent of whether every
	// member has a TF counterpart at all (most of Greengrass V1 does not
	// — see the entry below).
	CFNPrefix string

	// Service is the human-readable service name.
	Service string

	// Reason is the one-sentence judgment, with its evidence, that puts
	// the service out of scope. This is the hand data opsExcluded-style
	// judgments always are: no schema anywhere says a service is retired.
	Reason string
}

// DeprecatedServices is the curated list of AWS services this fork treats
// as out of scope by policy: retired, end-of-life, or being wound down.
// Issue #49 names these seven as the anchors; membership of a *type* in the
// cohort (which resource types count, and how many CFN registry types the
// service carries) is computed by [Lookup] and the accessors below, against
// live/mapping.json and live/registry.json — only the service→deprecated
// judgment itself is hand data.
var DeprecatedServices = []DeprecatedService{
	{
		TFPrefix:  "aws_pinpoint_",
		CFNPrefix: "AWS::Pinpoint::",
		Service:   "Pinpoint",
		// AWS announced Pinpoint end-of-support for October 30, 2026;
		// existing functionality is being folded into End User Messaging.
		Reason: "a service AWS is retiring (end-of-support October 30, 2026)",
	},
	{
		TFPrefix:  "aws_greengrass_",
		CFNPrefix: "AWS::Greengrass::",
		Service:   "Greengrass V1",
		// AWS IoT Greengrass V1 is superseded by the component-based
		// Greengrass V2 (GA December 2020); AWS directs new deployments to
		// V2 and V1 receives no new features. The AWS provider never wired
		// aws_greengrass_* resources at all (0 TF types below), so this
		// entry's whole registry-side count (16 types) falls under
		// CohortUnmapped rather than ever being reachable through
		// CohortDeprecated's TF-prefix check — named here anyway so the
		// doc's registry-side tally is honest about the service.
		Reason: "a service AWS has superseded with Greengrass V2, shipping no new features for V1",
	},
	{
		TFPrefix:  "aws_waf_",
		CFNPrefix: "AWS::WAF::",
		Service:   "WAF Classic",
		// AWS WAF Classic (the pre-2019 API) is superseded by the unified
		// AWS WAFV2 API; AWS's own migration guidance treats Classic as
		// legacy and recommends WAFv2 for all new web ACLs.
		Reason: "a service AWS has superseded with the unified WAFv2 API",
	},
	{
		TFPrefix:  "aws_wafregional_",
		CFNPrefix: "AWS::WAFRegional::",
		Service:   "WAF Classic Regional",
		Reason:    "a service AWS has superseded with the unified WAFv2 API, same as WAF Classic",
	},
	{
		TFPrefix:  "aws_appmesh_",
		CFNPrefix: "AWS::AppMesh::",
		Service:   "App Mesh",
		// AWS App Mesh is closed to new customers; AWS directs users to
		// ECS Service Connect and other alternatives.
		Reason: "a service AWS has closed to new customers and is winding down",
	},
	{
		TFPrefix:  "aws_appstream_",
		CFNPrefix: "AWS::AppStream::",
		Service:   "AppStream 2.0",
		Reason:    "a service this fork holds out of scope by policy",
	},
	{
		TFPrefix:  "aws_dax_",
		CFNPrefix: "AWS::DAX::",
		Service:   "DAX",
		Reason:    "a service (DynamoDB Accelerator) this fork holds out of scope by policy",
	},
}

// CFNOnlyConstruct is one curated entry in [CFNOnlyConstructs].
type CFNOnlyConstruct struct {
	// Type is the CloudFormation Registry type name.
	Type string

	// Reason is the one-sentence judgment that the construct is CFN's own
	// template machinery rather than an infrastructure resource, so no TF
	// counterpart can exist by design rather than by omission.
	Reason string
}

// CFNOnlyConstructs is the curated list of CloudFormation Registry types
// that are CloudFormation's own template and stack mechanics, not AWS
// infrastructure — the CFN-side mirror of this fork's own logical-resource
// family (null_resource, local_file, random_password, time_sleep,
// live/LIMITATIONS.md's "Enforced today" section): a construct whose whole
// value is something a template-processing engine keeps track of, with no
// live twin any provider could read back. That is a judgment, the same way
// deciding null_resource has no live twin is a judgment, so this list is
// hand data. What is computed is the claim that goes with it: [init] checks
// every entry here against live/mapping.json and panics if one now has a TF
// counterpart, so the claim cannot go stale silently.
var CFNOnlyConstructs = []CFNOnlyConstruct{
	{
		Type:   "AWS::CloudFormation::WaitCondition",
		Reason: "a template-internal signal that a stack step finished, not an AWS resource",
	},
	{
		Type:   "AWS::CloudFormation::WaitConditionHandle",
		Reason: "the pre-signed URL a WaitCondition polls, existing only to be written to",
	},
	{
		Type:   "AWS::CloudFormation::Macro",
		Reason: "registers a template preprocessor for CloudFormation's own macro transform, not infrastructure",
	},
	{
		Type:   "AWS::CloudFormation::CustomResource",
		Reason: "a template escape hatch to an arbitrary Lambda-backed handler, with no AWS resource of its own",
	},
}

// EmulatorBlockedType is one curated entry in [EmulatorBlocked].
type EmulatorBlockedType struct {
	// Type is the TF provider-local resource type.
	Type string

	// Admitted is whether this type is in the v0 admission table today.
	// True for a type admitted but still carrying standing e2e residue
	// (its admission is real; only the emulator proof is blocked). False
	// for a type the emulator gap kept out of a wiring slice entirely —
	// admitting it would be real work with nothing to prove it against.
	Admitted bool

	// Reason is the one-sentence account of the emulator gap.
	Reason string
}

// EmulatorBlocked is the curated roster of types issue #26 names as
// wireable against real AWS but not provable against the floci emulator
// today. Which floci behavior blocks which type is read off the harness
// (live/e2e/run.sh) and the admission table, not derivable from any
// artifact, so this is hand data — the same status as DeprecatedServices
// and CFNOnlyConstructs.
var EmulatorBlocked = []EmulatorBlockedType{
	{
		Type:     "aws_iam_role",
		Admitted: true,
		// live/e2e/run.sh's RESIDUE_UNOWNED: floci's iam:GetRole omits
		// Tags, so a marker run can never see the role's own tofu-address
		// tag come back and reports it [UNOWNED] on every plan.
		Reason: "floci's iam:GetRole omits Tags, so the role's own marker never reads back and every plan reports it unowned",
	},
	{
		Type:     "aws_s3_bucket_policy",
		Admitted: true,
		// live/e2e/run.sh's RESIDUE_CHANGED: downstream of the role gap
		// above — the policy document embeds aws_iam_role.app's ARN, and
		// an unowned role is planned as a create, so the policy's plan
		// never settles either.
		Reason: "downstream of aws_iam_role's residue: its policy document embeds the unowned role's ARN, so its own plan never settles",
	},
	{
		Type:     "aws_ecr_repository",
		Admitted: false,
		// lex00/floci#23: floci's ECR emulation needs a docker daemon
		// this harness's image does not carry, so ECR calls fail outright.
		Reason: "floci's ECR emulation needs a docker daemon the current harness image does not carry (lex00/floci#23)",
	},
	{
		Type:     "aws_iam_user",
		Admitted: false,
		Reason:   "dropped from its wiring slice by the same floci IAM tag gap as aws_iam_role (lex00/floci#22)",
	},
	{
		Type:     "aws_db_instance",
		Admitted: false,
		// lex00/floci#28: RDS works end-to-end only when the docker
		// socket is mounted into the emulator container, which this
		// harness does not do today.
		Reason: "RDS only works fully against floci when the docker socket is mounted into the emulator container, which this harness does not do (lex00/floci#28)",
	},
}

// state is the parsed form of the two embedded artifacts, computed once.
type state struct {
	mapping  mappingArtifact
	registry registryArtifact

	byTFType  map[string]mappingRow
	byCFNType map[string]registryType
}

var (
	stateOnce sync.Once
	global    state
)

func load() *state {
	stateOnce.Do(func() {
		if err := json.Unmarshal(mappingJSONBytes, &global.mapping); err != nil {
			panic(fmt.Sprintf("residue: decoding embedded mapping.json: %v", err))
		}
		if err := json.Unmarshal(registryJSONBytes, &global.registry); err != nil {
			panic(fmt.Sprintf("residue: decoding embedded registry.json: %v", err))
		}
		global.byTFType = make(map[string]mappingRow, len(global.mapping.Rows))
		for _, r := range global.mapping.Rows {
			global.byTFType[r.TFType] = r
		}
		global.byCFNType = make(map[string]registryType, len(global.registry.Types))
		for _, t := range global.registry.Types {
			global.byCFNType[t.TypeName] = t
		}

		// The CFN-only claim is computed, not merely asserted: every entry
		// must be a real registry type with no reference anywhere in
		// mapping.json (as either a cfn_type or a fold_parent). A future
		// mapping-gen run that links one of these to a new TF type makes
		// the claim false, and this panics rather than let the roster
		// quietly lie. Tests exercise this same check without a panic
		// (see tools/survey-gen's residue_test.go), so a CI run always
		// surfaces it before it reaches a binary.
		referenced := map[string]bool{}
		for _, r := range global.mapping.Rows {
			if r.CFNType != nil {
				referenced[*r.CFNType] = true
			}
			if r.FoldParent != nil {
				referenced[*r.FoldParent] = true
			}
		}
		for _, c := range CFNOnlyConstructs {
			if _, ok := global.byCFNType[c.Type]; !ok {
				panic(fmt.Sprintf("residue: CFNOnlyConstructs names %s, which is not in the embedded registry.json", c.Type))
			}
			if referenced[c.Type] {
				panic(fmt.Sprintf("residue: CFNOnlyConstructs names %s, which live/mapping.json now maps a TF type to; it is no longer CFN-only", c.Type))
			}
		}
	})
	return &global
}

// deprecatedPrefixFor returns the [DeprecatedService] whose TFPrefix
// matches tfType, if any.
func deprecatedPrefixFor(tfType string) (DeprecatedService, bool) {
	for _, d := range DeprecatedServices {
		if strings.HasPrefix(tfType, d.TFPrefix) {
			return d, true
		}
	}
	return DeprecatedService{}, false
}

// emulatorBlockedFor returns the [EmulatorBlockedType] entry for tfType, if
// any.
func emulatorBlockedFor(tfType string) (EmulatorBlockedType, bool) {
	for _, e := range EmulatorBlocked {
		if e.Type == tfType {
			return e, true
		}
	}
	return EmulatorBlockedType{}, false
}

// Lookup reports which exclusion cohort tfType belongs to, and a full
// sentence naming it, or ok=false when the type is in no cohort — the
// common case, since most unadmitted types are simply not wired yet rather
// than excluded by any rule.
//
// Checked in order, most specific first: a deprecated-service type is named
// as deprecated even when its mapping row would also read as unmapped or
// laggard, because "out of scope by policy" is the more actionable reason.
// [CohortCFNOnly] never matches here, since no TF configuration can name a
// CloudFormation-only type.
func Lookup(tfType string) (cohort Cohort, sentence string, ok bool) {
	s := load()

	if d, found := deprecatedPrefixFor(tfType); found {
		return CohortDeprecated, fmt.Sprintf("%s is in %s, %s; choudoufu does not admit it.", tfType, d.Service, d.Reason), true
	}
	if e, found := emulatorBlockedFor(tfType); found {
		return CohortEmulatorBlocked, fmt.Sprintf("%s is wireable against real AWS but not provable against the floci emulator (issue #26): %s.", tfType, e.Reason), true
	}

	row, found := s.byTFType[tfType]
	if !found {
		return "", "", false
	}
	switch row.Via {
	case "none":
		note := "no CloudFormation counterpart is known"
		if row.Note != nil && *row.Note != "" {
			note = *row.Note
		}
		return CohortUnmapped, fmt.Sprintf("%s has no CloudFormation Registry counterpart (%s), so the registry-backed admission path cannot reach it.", tfType, note), true
	case "name", "alias", "service-alias", "fold":
		cfnType := ""
		if row.CFNType != nil {
			cfnType = *row.CFNType
		} else if row.FoldParent != nil {
			cfnType = *row.FoldParent
		}
		if cfnType == "" {
			return "", "", false
		}
		reg, ok := s.byCFNType[cfnType]
		if !ok || !reg.Handlers.none() {
			return "", "", false
		}
		return CohortRegistryLaggard, fmt.Sprintf("%s maps to %s, but the CloudFormation Registry ships no working handler for it, so only the provider's own identity schema can admit it.", tfType, cfnType), true
	}
	return "", "", false
}

// DeprecatedCount returns the number of CloudFormation Registry types under
// service's CFNPrefix.
func DeprecatedCount(service DeprecatedService) int {
	s := load()
	n := 0
	for _, t := range s.registry.Types {
		if strings.HasPrefix(t.TypeName, service.CFNPrefix) {
			n++
		}
	}
	return n
}

// DeprecatedTotal returns the sum of [DeprecatedCount] over every
// [DeprecatedServices] entry.
func DeprecatedTotal() int {
	n := 0
	for _, d := range DeprecatedServices {
		n += DeprecatedCount(d)
	}
	return n
}

// UnmappedGroup is one distinct note among the via:"none" rows of
// live/mapping.json, with the count of TF types that carry it.
type UnmappedGroup struct {
	Note  string
	Count int
}

// UnmappedGroups returns every via:"none" row's note, grouped, sorted by
// descending count then by note text. UnmappedTotal is the sum of every
// group's Count.
func UnmappedGroups() (groups []UnmappedGroup, total int) {
	s := load()
	counts := map[string]int{}
	for _, r := range s.mapping.Rows {
		if r.Via != "none" {
			continue
		}
		note := ""
		if r.Note != nil {
			note = *r.Note
		}
		counts[note]++
		total++
	}
	for note, c := range counts {
		groups = append(groups, UnmappedGroup{Note: note, Count: c})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Note < groups[j].Note
	})
	return groups, total
}

// LaggardType is one TF type in the registry-laggard cohort: mapped to a
// CFN type (by name, alias, service-alias, or fold) whose Registry entry
// ships no working handler at all.
type LaggardType struct {
	TFType  string
	CFNType string
}

// RegistryLaggardTypes returns every TF type in the registry-laggard
// cohort, sorted by TF type, excluding any type already counted under
// [DeprecatedServices] (a deprecated service's own laggard registry
// coverage is not this cohort's news to report a second time).
func RegistryLaggardTypes() []LaggardType {
	s := load()
	var out []LaggardType
	for _, r := range s.mapping.Rows {
		if r.Via == "none" {
			continue
		}
		cfnType := ""
		if r.CFNType != nil {
			cfnType = *r.CFNType
		} else if r.FoldParent != nil {
			cfnType = *r.FoldParent
		}
		if cfnType == "" {
			continue
		}
		if _, deprecated := deprecatedPrefixFor(r.TFType); deprecated {
			continue
		}
		reg, ok := s.byCFNType[cfnType]
		if !ok || !reg.Handlers.none() {
			continue
		}
		out = append(out, LaggardType{TFType: r.TFType, CFNType: cfnType})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TFType < out[j].TFType })
	return out
}
