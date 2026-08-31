// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The mapping artifact's via column vocabulary (tools/mapping-gen). Only
// these three name a CFN type this package will hand out; see the package
// doc's "What counts as mapped". viaServiceAlias was missing from this list
// until #124's aps cohort failed replan on aws_prometheus_workspace - the
// roster predated that tier of the mapping and silently dropped every row
// carrying it.
const (
	viaName         = "name"
	viaAlias        = "alias"
	viaServiceAlias = "service-alias"
)

// mappingArtifact mirrors live/mapping.json's shape (tools/mapping-gen's
// Mapping), keeping only the fields this package reads.
type mappingArtifact struct {
	Rows []mappingRow `json:"rows"`
}

type mappingRow struct {
	TFType  string  `json:"tf_type"`
	CFNType *string `json:"cfn_type"`
	Via     string  `json:"via"`
}

// registryArtifact mirrors live/registry.json's shape (tools/registry-gen's
// RegistryArtifact), keeping only the fields this package reads.
type registryArtifact struct {
	Types []registryEntry `json:"types"`
}

type registryEntry struct {
	TypeName          string   `json:"type_name"`
	PrimaryIdentifier []string `json:"primary_identifier,omitempty"`
	Tagging           struct {
		Taggable bool `json:"taggable"`
	} `json:"tagging"`
	Handlers struct {
		List              bool     `json:"list"`
		ListRequiredInput []string `json:"list_required_input,omitempty"`
	} `json:"handlers"`

	// Relationships are the schema's own relationshipRef declarations
	// (issue #151). Only the referenced type name is read here; the
	// property that carries the reference stays in the artifact for a
	// consumer that needs it.
	Relationships []struct {
		TypeName string `json:"type_name"`
	} `json:"relationships,omitempty"`
}

// Roster is live/mapping.json joined against live/registry.json, held in
// memory after one parse of each. The zero value is not useful; build one
// with [Load] or [Parse].
type Roster struct {
	// cfnType is tf_type -> cfn_type, for rows whose via is "name" or
	// "alias" only (see the package doc).
	cfnType map[string]string

	// tfTypeFor is cfn_type -> every tf_type live/mapping.json's "name" or
	// "alias" rows map to it, [Roster.TFTypesForCFNType]'s index. Built from
	// the same rows as cfnType, in the other direction: the ARN join (issue
	// #51, internal/live/discovery/tagging.go) has a CFN type in hand and
	// needs the TF type, not the other way around. Almost always one entry;
	// more than one means the CFN type's TF mapping is not unique, which
	// that join treats as ambiguous rather than picking one.
	tfTypeFor map[string][]string

	// tfTypeForAnyProvenance is the same reverse index built from EVERY row
	// that names a CFN type at all, whatever its via - the reverse of
	// anyCFNType, and deliberately wider than tfTypeFor above for the same
	// reason anyCFNType is wider than cfnType.
	//
	// The narrow index answers "may Cloud Control ENUMERATE this type on its
	// own", which is why it accepts only the provenances the package doc's
	// "What counts as mapped" lists. The reverse join has a different
	// question in front of it: the Resource Groups Tagging API has already
	// handed back one object, with its ARN and its own ownership markers
	// inline, and the only thing left to decide is which TF type that object
	// IS. Nothing is enumerated on the strength of this answer, so a row's
	// provenance is not the property that makes it safe - being the only
	// ADMITTED candidate is, which is the filter the ARN join's own caller
	// applies before using this ([joinTaggedResource]).
	//
	// Measured against the committed artifacts at this commit: 47 rows carry
	// via "former2" (mapping-gen's pinned iann0036/former2 dataset, used only
	// where no name/alias/service-alias row existed), 38 of them name a type
	// the identity table admits, and every one of those 38 CFN types has
	// exactly one admitted TF type - so the wider index adds no ambiguity
	// anywhere it adds an answer. Two of the 38 are reachable from the ARN
	// join table today (AWS::EC2::CustomerGateway and, through the classic
	// load balancer's own shape, AWS::ElasticLoadBalancing::LoadBalancer);
	// the rest become reachable if and when a row for their ARN segment is
	// added.
	tfTypeForAnyProvenance map[string][]string

	// listable is cfn_type -> whether live/registry.json's handlers.list is
	// set with no required input (see the package doc's "What counts as
	// listable"). A CFN type absent from live/registry.json - the mapping
	// artifact named one that the registry roster does not carry, which
	// should not happen for two artifacts generated from the same CFN
	// vintage but is not this package's place to assume - is simply absent
	// here too, and Listable reports false for it.
	listable map[string]bool

	// listRequiredInput is cfn_type -> live/registry.json's
	// handlers.list_required_input, for a type whose handlers.list is set
	// but the required-input list is non-empty (the exact complement of
	// what makes listable true) - [Roster.EnumerationSourceScoped]'s own
	// data. A type with handlers.list false, or list true with no required
	// input, has no entry here.
	listRequiredInput map[string][]string

	// taggable is cfn_type -> live/registry.json's tagging.taggable.
	taggable map[string]bool

	// arity is cfn_type -> len(primary_identifier), the number of "|"-joined
	// segments a Cloud Control identifier for the type carries.
	arity map[string]int

	// primaryIdentifier is cfn_type -> live/registry.json's own
	// primary_identifier, in order - the CFN property name each "|"-joined
	// segment of a ListResources/GetResource Identifier carries, positionally.
	// [Roster.RequiredListInput]'s own property names are not guaranteed to
	// appear here at all (AWS::DataZone::Project requires
	// "DomainIdentifier", a property distinct from either primary_identifier
	// entry) or to sit at any particular position when they do
	// (AWS::ApiGateway::Deployment's RestApiId is index 1, not 0) - a caller
	// that wants to verify a scoped listing's own identifier actually matches
	// what it asked for needs the position, not an assumption.
	primaryIdentifier map[string][]string

	// service is the CFN service segment per TF type, from every mapping
	// row naming a CFN type - wider than cfnType, see ServiceOf.
	service map[string]string

	// anyCFNType is that same wider join, keeping the whole CFN type name.
	anyCFNType map[string]string

	// relationships is each CFN type's declared relationshipRef targets
	// (issue #151), by CFN type name.
	relationships map[string][]string
}

// Load reads and parses the two artifacts from disk.
func Load(mappingPath, registryPath string) (*Roster, error) {
	mappingJSON, err := os.ReadFile(mappingPath) //nolint:gosec // caller-supplied path to a known artifact
	if err != nil {
		return nil, fmt.Errorf("registry: reading %s: %w", mappingPath, err)
	}
	registryJSON, err := os.ReadFile(registryPath) //nolint:gosec // caller-supplied path to a known artifact
	if err != nil {
		return nil, fmt.Errorf("registry: reading %s: %w", registryPath, err)
	}
	return Parse(mappingJSON, registryJSON)
}

// Parse builds a Roster from the two artifacts' JSON bytes directly, for a
// caller that already has them (a test fixture, an embedded copy, bytes
// fetched some other way) without going through the filesystem.
func Parse(mappingJSON, registryJSON []byte) (*Roster, error) {
	var m mappingArtifact
	if err := json.Unmarshal(mappingJSON, &m); err != nil {
		return nil, fmt.Errorf("registry: parsing the mapping artifact: %w", err)
	}
	var reg registryArtifact
	if err := json.Unmarshal(registryJSON, &reg); err != nil {
		return nil, fmt.Errorf("registry: parsing the registry artifact: %w", err)
	}

	r := &Roster{
		cfnType:       make(map[string]string, len(m.Rows)),
		service:       make(map[string]string, len(m.Rows)),
		anyCFNType:    make(map[string]string, len(m.Rows)),
		relationships: make(map[string][]string, len(reg.Types)),
		tfTypeFor:     make(map[string][]string, len(m.Rows)),

		tfTypeForAnyProvenance: make(map[string][]string, len(m.Rows)),

		listable:          make(map[string]bool, len(reg.Types)),
		listRequiredInput: make(map[string][]string, len(reg.Types)),
		taggable:          make(map[string]bool, len(reg.Types)),
		arity:             make(map[string]int, len(reg.Types)),
		primaryIdentifier: make(map[string][]string, len(reg.Types)),
	}
	for _, row := range m.Rows {
		if row.CFNType == nil || *row.CFNType == "" {
			continue
		}
		// The service map is deliberately wider than cfnType: every row
		// naming a CFN type at all answers "which AWS service is this",
		// including the former2-sourced ones Cloud Control enumeration
		// excludes. aws_volume_attachment is via:former2 and is
		// AWS::EC2::VolumeAttachment, which is exactly what tells
		// identity's parent derivation it belongs with aws_ebs_volume
		// (issue #129) - and it is not something CloudControlType may say,
		// since its own contract is about enumerability.
		r.anyCFNType[row.TFType] = *row.CFNType
		r.tfTypeForAnyProvenance[*row.CFNType] = append(r.tfTypeForAnyProvenance[*row.CFNType], row.TFType)
		if parts := strings.Split(*row.CFNType, "::"); len(parts) >= 2 && parts[1] != "" {
			r.service[row.TFType] = parts[1]
		}
		if row.Via != viaName && row.Via != viaAlias && row.Via != viaServiceAlias {
			continue
		}
		r.cfnType[row.TFType] = *row.CFNType
		r.tfTypeFor[*row.CFNType] = append(r.tfTypeFor[*row.CFNType], row.TFType)
	}
	for _, e := range reg.Types {
		r.listable[e.TypeName] = e.Handlers.List && len(e.Handlers.ListRequiredInput) == 0
		if e.Handlers.List && len(e.Handlers.ListRequiredInput) > 0 {
			r.listRequiredInput[e.TypeName] = append([]string(nil), e.Handlers.ListRequiredInput...)
		}
		r.taggable[e.TypeName] = e.Tagging.Taggable
		r.arity[e.TypeName] = len(e.PrimaryIdentifier)
		if len(e.PrimaryIdentifier) > 0 {
			r.primaryIdentifier[e.TypeName] = append([]string(nil), e.PrimaryIdentifier...)
		}
		for _, rel := range e.Relationships {
			r.relationships[e.TypeName] = append(r.relationships[e.TypeName], rel.TypeName)
		}
	}
	return r, nil
}

// CloudControlType returns the CFN type live/mapping.json joins tfType to,
// and whether it named one at all (a "name" or "alias" row - see the
// package doc). A "fold" or "none" row, or a tfType the mapping artifact
// never saw, reports ok=false.
func (r *Roster) CloudControlType(tfType string) (cfnType string, ok bool) {
	if r == nil {
		return "", false
	}
	cfnType, ok = r.cfnType[tfType]
	return cfnType, ok
}

// TFTypesForCFNType returns every TF type live/mapping.json's "name" or
// "alias" rows map to cfnType, sorted - the reverse of
// [Roster.CloudControlType], for a caller that has a CFN type in hand and
// wants the TF type it names (the ARN join, issue #51: an ARN's service and
// resource segments join to a CFN type first, and only then to a TF type).
// nil for a CFN type nothing maps to. A result with more than one entry
// means the CFN type's TF mapping is not unique in the committed artifact;
// today's live/mapping.json never produces one, but a caller doing a reverse
// join must still treat it as ambiguous rather than picking the first.
func (r *Roster) TFTypesForCFNType(cfnType string) []string {
	if r == nil || len(r.tfTypeFor[cfnType]) == 0 {
		return nil
	}
	out := append([]string(nil), r.tfTypeFor[cfnType]...)
	sort.Strings(out)
	return out
}

// TFTypesForCFNTypeAnyProvenance is [Roster.TFTypesForCFNType] over every
// mapping row that names a CFN type, whatever its via - see
// tfTypeForAnyProvenance's own field comment for why the reverse join is
// allowed a wider index than the enumeration join, and for the measurement
// behind it. Sorted, nil for a CFN type no row names at all. More than one
// entry is ambiguity the caller must resolve or refuse, exactly as with the
// narrow method.
func (r *Roster) TFTypesForCFNTypeAnyProvenance(cfnType string) []string {
	if r == nil || len(r.tfTypeForAnyProvenance[cfnType]) == 0 {
		return nil
	}
	out := append([]string(nil), r.tfTypeForAnyProvenance[cfnType]...)
	sort.Strings(out)
	return out
}

// Listable reports whether cfnType can be enumerated with Cloud Control's
// ListResources and no required input (see the package doc's "What counts
// as listable"). False for a type live/registry.json never saw.
func (r *Roster) Listable(cfnType string) bool {
	listable, _ := r.ListableKnown(cfnType)
	return listable
}

// ListableKnown is [Roster.Listable] with the same found/not-found split
// [Roster.TaggableKnown] carries, and for the same reason.
func (r *Roster) ListableKnown(cfnType string) (listable, known bool) {
	if r == nil {
		return false, false
	}
	listable, known = r.listable[cfnType]
	return listable, known
}

// Taggable reports live/registry.json's tagging.taggable for cfnType. False
// for a type live/registry.json never saw.
func (r *Roster) Taggable(cfnType string) bool {
	taggable, _ := r.TaggableKnown(cfnType)
	return taggable
}

// TaggableKnown is [Roster.Taggable] with the question the bare bool cannot
// answer: whether live/registry.json has a row for this CFN type at all.
//
// The two are different facts and the plain form collapses them, which is
// issue #168. A caller that turns a false into an operator-facing sentence
// naming the artifact ("live/registry.json records X as untaggable") is
// claiming the artifact said something; when there is no row, it said
// nothing, and what the operator needs to hear is that two artifacts have
// drifted rather than that a resource type cannot carry a tag.
//
// Zero CFN types the roster maps are missing from the registry today. That
// is a property of two artifacts regenerated from different upstreams at
// different times, not a guarantee.
func (r *Roster) TaggableKnown(cfnType string) (taggable, known bool) {
	if r == nil {
		return false, false
	}
	taggable, known = r.taggable[cfnType]
	return taggable, known
}

// IdentifierArity is the number of "|"-joined segments a Cloud Control
// identifier for cfnType carries - live/registry.json's
// len(primary_identifier). Zero for a type live/registry.json never saw,
// which is indistinguishable here from a (nonexistent) zero-part identifier;
// callers that need to tell those apart should check [Roster.Listable]
// first.
func (r *Roster) IdentifierArity(cfnType string) int {
	if r == nil {
		return 0
	}
	return r.arity[cfnType]
}

// PrimaryIdentifier returns live/registry.json's own primary_identifier for
// cfnType, in order - the CFN property name each "|"-joined segment of a
// ListResources/GetResource Identifier carries, positionally (see the
// [Roster.primaryIdentifier] field doc for why a caller needs the position
// rather than assuming one). Nil for a type live/registry.json never saw.
func (r *Roster) PrimaryIdentifier(cfnType string) []string {
	if r == nil {
		return nil
	}
	pi := r.primaryIdentifier[cfnType]
	if pi == nil {
		return nil
	}
	return append([]string(nil), pi...)
}

// EnumerationSource is the combined join a caller actually wants: the CFN
// type Cloud Control should be asked to list tfType through, when the
// mapping names one and the registry roster says it is listable. ok is
// false for anything short of both - unmapped, folded, or mapped to a type
// that turns out to require input to list - which is exactly the "neither"
// leg of discovery's enumeration-source selection (issue #47): the caller
// falls back to its existing refusal.
func (r *Roster) EnumerationSource(tfType string) (cfnType string, ok bool) {
	cfnType, mapped := r.CloudControlType(tfType)
	if !mapped || !r.Listable(cfnType) {
		return "", false
	}
	return cfnType, true
}

// RequiredListInput returns the CFN property names cfnType's list handler
// requires as scoping input (live/registry.json's
// handlers.list_required_input), in registry order, and whether it has any.
// False for a type live/registry.json never saw, one whose handlers.list is
// unset, or one whose list needs no input at all - [Roster.Listable] is true
// for that last case instead, and the two never overlap.
func (r *Roster) RequiredListInput(cfnType string) (properties []string, ok bool) {
	if r == nil {
		return nil, false
	}
	properties, ok = r.listRequiredInput[cfnType]
	if !ok {
		return nil, false
	}
	return append([]string(nil), properties...), true
}

// EnumerationSourceScoped is [Roster.EnumerationSource]'s counterpart for a
// TF type mapped to a CFN type whose list handler requires scoping input:
// where EnumerationSource reports ok=false and leaves the caller with a
// refusal, this reports the CFN type and the required property names, for a
// caller that has a parent value in hand to scope with
// (internal/live/discovery's parent-scoped Cloud Control leg,
// internal/live/cloudcontrol.Client.ListResourcesScoped).
//
// ok is false for anything short of a mapped CFN type whose registry row
// sets handlers.list with a non-empty required-input list - which, by
// construction, is exactly the set [Roster.EnumerationSource] excludes for
// requiring input. A tfType is therefore never ok from both methods.
func (r *Roster) EnumerationSourceScoped(tfType string) (cfnType string, requiredInput []string, ok bool) {
	cfnType, mapped := r.CloudControlType(tfType)
	if !mapped {
		return "", nil, false
	}
	requiredInput, ok = r.RequiredListInput(cfnType)
	if !ok {
		return "", nil, false
	}
	return cfnType, requiredInput, true
}

// ServiceOf reports the CloudFormation service segment of the type tfType
// maps to - "EC2" for aws_volume_attachment, via AWS::EC2::VolumeAttachment -
// and whether it is mapped at all.
//
// It exists for [identity.ServiceOf] (issue #129), whose parent derivation
// needs to know whether two Terraform types belong to the same AWS service.
// The Terraform name cannot answer that: aws_volume_attachment and
// aws_ebs_volume share no prefix and are both AWS::EC2. Only the mapping
// knows.
//
// Wider than [Roster.CloudControlType] on purpose. That one answers "can
// Cloud Control enumerate this", so it keeps only name/alias/service-alias
// rows; this one answers "which service is this", so a former2-sourced row
// counts too - aws_volume_attachment is one, and excluding it is what sent
// it to the residue in the first draft of this change.
//
// A type this roster has no CFN mapping for reports ok=false rather than an
// empty service, so a caller can tell "unmapped" from "mapped to something
// with no service segment", and fall back rather than treat two unmapped
// types as sharing one.
func (r *Roster) ServiceOf(tfType string) (string, bool) {
	if r == nil {
		return "", false
	}
	svc, ok := r.service[tfType]
	return svc, ok
}

// RelatedTypes returns the set of CloudFormation types the schema for
// tfType's own CFN type declares a relationshipRef to (issue #151), keyed by
// CFN type name. Empty when the type is unmapped or its schema declares none,
// which is the overwhelming majority: 26 of 1,683 schemas carry the
// annotation at the pin this was written against.
//
// It is a set rather than a per-property map on purpose. A consumer asking
// "is this derived parent one AWS declares a relationship to" does not need
// to know which property carries it, and pairing a Terraform argument to a
// CloudFormation property name would be a second inference on top of the one
// being checked.
func (r *Roster) RelatedTypes(tfType string) map[string]bool {
	if r == nil {
		return nil
	}
	cfnType, ok := r.CloudControlTypeOrService(tfType)
	if !ok {
		return nil
	}
	rels := r.relationships[cfnType]
	if len(rels) == 0 {
		return nil
	}
	out := make(map[string]bool, len(rels))
	for _, t := range rels {
		out[t] = true
	}
	return out
}

// CloudControlTypeOrService returns the CFN type tfType maps to under the
// wider join [Roster.ServiceOf] uses - every mapping row naming a CFN type,
// including the former2-sourced ones [Roster.CloudControlType] excludes
// because they are not Cloud Control enumerable.
//
// Named for what it is: the CFN type, for a caller that wants identity or
// relationship facts rather than enumerability.
func (r *Roster) CloudControlTypeOrService(tfType string) (string, bool) {
	if r == nil {
		return "", false
	}
	cfn, ok := r.anyCFNType[tfType]
	return cfn, ok
}
