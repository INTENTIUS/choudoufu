// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"fmt"
	"math/big"
	"path"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// Live represents a module's live configuration: a "live" block inside a
// "terraform" block, or the [LiveSidecarFilename] sidecar file, whose whole
// body is the same content the block would carry. Its presence is what puts a
// run into stateless mode: no state file, no backend, no lock, with prior
// state rebuilt from the live system on every operation.
//
// It is deliberately a configuration block and not a command-line flag.
// Whether a team's infrastructure has an authoritative state file is a
// property of the configuration, checked in and reviewed with it; a flag
// would mean a run that forgot it silently fell back to writing a state
// file, which is exactly the failure this mode exists to remove.
//
// The block is also why a stateless module cannot have a backend: a "backend"
// or "cloud" block alongside it is refused here, in the configuration
// decoder, rather than only by the stateless subset lint. The decoder is the
// earlier wall, and it is the one every command passes through - including
// the ones that would otherwise reach for a state manager before any
// stateless code ran.
type Live struct {
	// Estate is the name of the estate this configuration owns, as it appears
	// in the tofu-estate marker described by live/MARKERS.md. It is
	// optional: when it is not set, the estate name is derived from the
	// tofu-estate tags the configuration already stamps, and a configuration
	// that stamps none (or several) is told to name one here.
	//
	// The value must be a literal string. An estate name assembled from a
	// variable would mean the identity of the thing that owns live resources
	// depends on how a run was invoked, and ownership records that move with
	// the invocation are not ownership records.
	Estate string

	// EstateSet distinguishes an absent estate argument from one set to the
	// empty string, so that the second can be an error rather than silently
	// meaning "derive it".
	EstateSet bool

	// EstateRange is where the "estate" argument was written, the zero value
	// when the block does not set it. It is recorded so that a diagnostic
	// about the estate can point at the argument that named it rather than at
	// the whole block, which is what DeclRange gives.
	EstateRange hcl.Range

	// DeclRange is the "live" block's own header, which is what a diagnostic
	// about the block as a whole points at - a backend beside it, or a
	// stateless refusal that has no more specific argument to name. For a
	// live configuration read from the sidecar file it is the start of that
	// file, so the same diagnostics point at the file instead.
	DeclRange hcl.Range

	// Sidecar records that this live configuration was read from the
	// [LiveSidecarFilename] sidecar file rather than from a live block inside
	// a terraform block. The two sources are equivalent everywhere else; the
	// field exists so that a module carrying both can be told exactly which
	// two places disagree, rather than getting the duplicate-block error
	// meant for two live blocks in .tf files.
	Sidecar bool

	// Policy is the optional nested "policy" block: one verb per ownership
	// quadrant (declared-in-source x carries-the-tag), plus the tag those
	// quadrants read and the delete quadrant's safety rails. Nil when the
	// live block sets no policy block at all, which must mean today's fixed
	// behavior (issue #67's "existing estates change nothing").
	//
	// This struct is the raw decode only - the literal string an author
	// wrote for each attribute, or its zero value with the matching *Set
	// flag false when they left it out. It deliberately knows nothing about
	// which verbs are valid for which quadrant: that is
	// internal/live/policy's ValidVerbs matrix, enforced at lint time by
	// internal/live/lint, the same layering every other semantic rule about
	// this fork's configuration follows (compare Estate above, whose own
	// grammar is checked by internal/live/discovery.ValidEstateName rather
	// than here).
	Policy *LivePolicy

	// RecordStore is the optional nested "record_store" block: where GitHub
	// issue #73's record-backed logical types (null_resource, terraform_data,
	// time_*, non-sensitive random_*) persist their micro-state. Nil when the
	// live block sets no record_store block at all, which must mean those
	// types stay refused exactly as they were before #73 - internal/live/lint
	// only lifts the RECORD_ADMITTED refusal when this is non-nil. See
	// [LiveRecordStore].
	RecordStore *LiveRecordStore

	// Strict is the optional nested "strict" block: GitHub issue #365's
	// profile toggles, each one of the principles this fork exists for
	// turned into a setting whose default is today's behavior. Nil when the
	// live block sets no strict block at all, which must mean exactly what
	// a configuration written before the block existed got - the same
	// "absent means absent" contract Policy and RecordStore already follow,
	// and the thing that makes HANDOFF.md's "compatible out of the box"
	// true by construction rather than by review.
	//
	// Like [Live.Policy], this is the raw decode only: the literal string
	// an author wrote, with no opinion on whether it means anything. That
	// judgement needs internal/live/strict's vocabulary and belongs to
	// internal/live/lint.
	Strict *LiveStrict
}

// LiveStrict is the "strict" block nested inside a live block. See
// [Live.Strict].
type LiveStrict struct {
	// MarkerRepair is the literal string an author wrote for the
	// "marker_repair" argument - "repair", "never", or whatever they typed,
	// valid or not. Validity is internal/live/strict's vocabulary, checked
	// at lint time by internal/live/lint.checkLiveStrict, for the same
	// reason [LivePolicy]'s quadrant verbs are checked there: this package
	// does not depend on that one.
	//
	// MarkerRepairSet distinguishes an omitted argument, which resolves to
	// internal/live/strict.DefaultMarkerRepair and therefore to today's
	// behavior, from one written out.
	MarkerRepair      string
	MarkerRepairSet   bool
	MarkerRepairRange hcl.Range

	// MarkersRecord is the optional nested `markers "record"` block: which
	// resources hold their identity in the estate's record store instead of
	// in an ownership marker tag, HANDOFF.md's "per-type or per-address
	// markers = record, for tag budgets and tag policies, trading IAM
	// governability for a record-held identity". Nil when the strict block
	// declares no such block, which must mean today's behavior - every
	// taggable resource is marked.
	//
	// It is a LABELED block rather than an attribute because HANDOFF's
	// phrasing is shorthand for what is really a selection with two lists,
	// and because the label leaves room for the inverse selection
	// (`markers "tag"`) without a grammar change.
	//
	// Like every other field here this is the raw decode: two literal lists
	// of strings, with no opinion on whether the type names exist or the
	// addresses parse. That judgement needs internal/addrs' target grammar
	// and the provider's schemas, and belongs to internal/live/lint - the
	// same layering [Live.Estate] already has, whose grammar is checked by
	// internal/live/discovery.ValidEstateName rather than here.
	MarkersRecord *LiveStrictMarkers

	// DeclRange is the "strict" block's own header.
	DeclRange hcl.Range
}

// LiveStrictMarkers is a `markers "<kind>"` block nested inside a strict
// block. See [LiveStrict.MarkersRecord].
type LiveStrictMarkers struct {
	// Kind is the block's label. "record" is the only one this fork knows
	// today; decodeStrictBlock refuses anything else, so nothing else
	// reaches this field.
	Kind      string
	KindRange hcl.Range

	// Types names resource types whose every instance this selection
	// covers, in the same literal-list-of-strings shape
	// [LivePolicyScope.Types] uses and read by the same decode helper.
	Types      []string
	TypesSet   bool
	TypesRange hcl.Range

	// Addresses names individual resources this selection covers, in the
	// `-target` grammar (internal/addrs' ParseTargetStr): module-qualified
	// or not, whole-resource, with no wildcards. Parsed by
	// internal/live/lint rather than here.
	Addresses      []string
	AddressesSet   bool
	AddressesRange hcl.Range

	// DeclRange is the "markers" block's own header.
	DeclRange hcl.Range
}

// LiveRecordStore is the "record_store" block nested inside a live block. Its
// label picks the backend ("local", "ssm", or "s3"), the same
// labeled-block-names-the-implementation shape a stock "backend" block uses,
// per issue #73's "phrased in familiar backend-like terms" ruling. See
// [Live.RecordStore].
type LiveRecordStore struct {
	// Type is the block's label: "local", "ssm", or "s3". Validated against
	// exactly those three spellings in decodeRecordStoreBlock; nothing else
	// reaches this field.
	Type      string
	TypeRange hcl.Range

	// Path is the "local" backend's directory, relative to the module
	// directory. Optional; empty means the caller's own default (a
	// ".tofu-records" directory beside the module, mirroring plain local
	// state's default filename). Literal string, relative, and confined to
	// the module directory: see validateRecordStorePath.
	Path      string
	PathSet   bool
	PathRange hcl.Range

	// Bucket is the "s3" backend's bucket name. Required for that backend;
	// unused by the other two.
	Bucket      string
	BucketSet   bool
	BucketRange hcl.Range

	// KeyPrefix overrides the "ssm" and "s3" backends' default key
	// namespace, which the caller derives from the estate name. Optional.
	// When set, it must stay disjoint from the receipts namespace
	// (live/RECEIPTS.md's "/tofu-receipts/<estate>/<effect>"): a key_prefix
	// whose first "/"-delimited segment is literally "tofu-receipts" is a
	// decode error, so a record can never be written where a receipt's own
	// namespace lives. See validateRecordStoreKeyPrefix.
	KeyPrefix      string
	KeyPrefixSet   bool
	KeyPrefixRange hcl.Range

	// Region overrides the "ssm" and "s3" backends' AWS region. Optional;
	// empty defers to the ordinary AWS SDK default-config chain (environment,
	// shared config, IMDS), the same as every other AWS client this fork
	// builds when a caller names no region.
	Region      string
	RegionSet   bool
	RegionRange hcl.Range

	// DeclRange is the "record_store" block's own header.
	DeclRange hcl.Range
}

// LivePolicy is the "policy" block nested inside a live block. See [Live.Policy].
type LivePolicy struct {
	// DeclaredTagged, DeclaredUntagged, UndeclaredTagged and
	// UndeclaredUntagged are the four quadrant verbs, named for their
	// policy-block attribute: "declared_tagged" and so on. Each is the
	// literal string an author wrote - "converge", "delete", or whatever
	// they typed, valid or not; a typo is caught by
	// internal/live/lint.checkLivePolicy, not here, because deciding
	// validity needs the per-quadrant matrix and this package does not
	// depend on it. The matching *Set field distinguishes an omitted
	// attribute (which resolves to today's fixed behavior for that
	// quadrant) from one written out.
	DeclaredTagged      string
	DeclaredTaggedSet   bool
	DeclaredTaggedRange hcl.Range

	DeclaredUntagged      string
	DeclaredUntaggedSet   bool
	DeclaredUntaggedRange hcl.Range

	UndeclaredTagged      string
	UndeclaredTaggedSet   bool
	UndeclaredTaggedRange hcl.Range

	UndeclaredUntagged      string
	UndeclaredUntaggedSet   bool
	UndeclaredUntaggedRange hcl.Range

	// TagKey and TagValue name the tag every quadrant's "tagged" half is
	// read against. Both are optional; omitted, they default to the estate
	// marker (internal/live/policy.Build fills in markers.TagEstate and
	// this estate's name), which is the reading every quadrant had before
	// this block existed. A configuration sets them to use a preservation
	// tag distinct from the estate marker instead - issue #67's "one
	// semantic question for the maintainer to confirm".
	TagKey      string
	TagKeySet   bool
	TagKeyRange hcl.Range

	TagValue      string
	TagValueSet   bool
	TagValueRange hcl.Range

	// Scope narrows a delete-quadrant verb's reach. Nil when the policy
	// block sets no scope block. internal/live/lint refuses a quadrant
	// explicitly assigned "delete" with no scope block at all - "an
	// unscoped account-wide purge is a lint refusal, not a default" per
	// issue #67 - so a Policy that later carries a delete verb and a nil
	// Scope can only happen if that check was skipped.
	Scope *LivePolicyScope

	// Threshold is the delete quadrant's first-run guard: a policy whose
	// delete quadrant would touch more resources than this refuses to
	// apply. Parsed and validated as a non-negative literal integer here;
	// enforcing it against an actual roster is the behavioral half's job
	// (issue #67, "First-run protection"), not this package's.
	Threshold      int
	ThresholdSet   bool
	ThresholdRange hcl.Range

	// DeclRange is the "policy" block's own header.
	DeclRange hcl.Range
}

// LivePolicyScope is the "scope" block nested inside a policy block. See
// [LivePolicy.Scope].
type LivePolicyScope struct {
	// Services, Types and Regions each narrow what a delete-quadrant verb
	// may reach: provider service namespaces, resource type names, and
	// regions, respectively. Every one is optional and any combination may
	// be set; an empty list is indistinguishable from an absent attribute,
	// because in both cases nothing was named to narrow by, so
	// internal/live/lint requires at least one of the three to be
	// non-empty in a scope block that accompanies a delete quadrant.
	Services []string
	Types    []string
	Regions  []string

	// DeclRange is the "scope" block's own header.
	DeclRange hcl.Range
}

var livePolicyScopeSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "services"},
		{Name: "types"},
		{Name: "regions"},
	},
}

var livePolicySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "declared_tagged"},
		{Name: "declared_untagged"},
		{Name: "undeclared_tagged"},
		{Name: "undeclared_untagged"},
		{Name: "tag_key"},
		{Name: "tag_value"},
		{Name: "threshold"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "scope"},
	},
}

var liveBlockSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "estate"},
		// "snapshots" and "snapshot_path" are tombstones: the observational
		// snapshot subsystem they configured was removed by issue #109, and
		// the two names stay in the schema solely so that a configuration
		// still carrying either gets the specific removal error
		// decodeLiveBlock authors (pointing at the argument, naming what
		// replaced it) instead of HCL's generic "Unsupported argument".
		{Name: "snapshots"},
		{Name: "snapshot_path"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "policy"},
		{Type: "record_store", LabelNames: []string{"type"}},
		{Type: "strict"},
	},
}

var liveStrictSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "marker_repair"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "markers", LabelNames: []string{"kind"}},
	},
}

var liveStrictMarkersSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "types"},
		{Name: "addresses"},
	},
}

var recordStoreBlockSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "path"},
		{Name: "bucket"},
		{Name: "key_prefix"},
		{Name: "region"},
	},
}

func decodeLiveBlock(block *hcl.Block) (*Live, hcl.Diagnostics) {
	return decodeLiveBody(block.Body, block.DefRange)
}

// decodeLiveBody decodes a live configuration from its body, which is either
// a live block's body (declRange being the block header) or the whole body of
// the sidecar file (declRange being the start of that file). Every rule is
// shared: the sidecar is a second place to write the same content, not a
// second dialect, so the estate grammar, the snapshot tombstones and the
// nested blocks all behave identically in both.
func decodeLiveBody(body hcl.Body, declRange hcl.Range) (*Live, hcl.Diagnostics) {
	s := &Live{
		DeclRange: declRange,
	}

	content, diags := body.Content(liveBlockSchema)

	if attr, exists := content.Attributes["estate"]; exists {
		s.EstateRange = attr.Range
		val, valDiags := attr.Expr.Value(nil)
		diags = append(diags, valDiags...)
		switch {
		case valDiags.HasErrors():
			// attr.Expr.Value(nil) fails for anything that is not a constant,
			// which is the rule this argument wants; the diagnostics it
			// produces already name the variable or function involved.
		case val.IsNull() || !val.IsWhollyKnown() || val.Type() != cty.String:
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid estate name",
				Detail:   "The \"estate\" argument names the estate this configuration owns and must be a literal string.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		case val.AsString() == "":
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Empty estate name",
				Detail:   "The \"estate\" argument was set to an empty string. Give the estate a name, or omit the argument entirely to derive the name from the tofu-estate tags this configuration stamps.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		default:
			s.Estate = val.AsString()
			s.EstateSet = true
		}
	}

	// The two snapshot arguments are refused with a removal error rather
	// than decoded: issue #109 removed observational snapshots outright.
	// The live system is authoritative and readable at any time, so a
	// stored snapshot was a stale copy of what every run re-derives; the
	// one load-bearing piece (guided discovery's plan-cost hint) moved into
	// the record_store. The error is authored here, at decode time, so an
	// operator upgrading across the removal reads what happened and what to
	// do, not a generic unsupported-argument message.
	for name, was := range map[string]string{
		"snapshots":     "turned on the observational snapshot's git-branch carrier",
		"snapshot_path": "named the observational snapshot's file",
	} {
		attr, exists := content.Attributes[name]
		if !exists {
			continue
		}
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Observational snapshots were removed",
			Detail: fmt.Sprintf(
				"The %q argument %s, and this build no longer writes observational snapshots at all: the live system is authoritative and readable at any time, so a stored snapshot was a stale copy of what every run re-derives. Remove the argument. Guided discovery's plan-cost hint, the one thing the snapshot fed, now lives in the record_store - a live block with a record_store block keeps that speed-up with nothing else to configure.",
				name, was,
			),
			Subject: attr.Range.Ptr(),
		})
	}

	var policyBlocks, recordStoreBlocks, strictBlocks []*hcl.Block
	for _, block := range content.Blocks {
		switch block.Type {
		case "policy":
			policyBlocks = append(policyBlocks, block)
		case "record_store":
			recordStoreBlocks = append(recordStoreBlocks, block)
		case "strict":
			strictBlocks = append(strictBlocks, block)
		}
	}

	switch len(policyBlocks) {
	case 0:
		// No policy block: Policy stays nil, which internal/live/policy.Build
		// reads as "every quadrant defaults to today's fixed behavior" -
		// issue #67's "existing estates change nothing".
	case 1:
		policy, policyDiags := decodePolicyBlock(policyBlocks[0])
		diags = append(diags, policyDiags...)
		s.Policy = policy
	default:
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Duplicate policy block",
			Detail:   "A live block may have at most one policy block.",
			Subject:  policyBlocks[1].DefRange.Ptr(),
		})
		policy, policyDiags := decodePolicyBlock(policyBlocks[0])
		diags = append(diags, policyDiags...)
		s.Policy = policy
	}

	switch len(recordStoreBlocks) {
	case 0:
		// No record_store block: RecordStore stays nil, which
		// internal/live/lint reads as "GitHub issue #73's RECORD_ADMITTED
		// logical types stay refused" - the existing behavior every
		// configuration written before this block existed keeps getting.
	case 1:
		rs, rsDiags := decodeRecordStoreBlock(recordStoreBlocks[0])
		diags = append(diags, rsDiags...)
		s.RecordStore = rs
	default:
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Duplicate record_store block",
			Detail:   "A live block may have at most one record_store block.",
			Subject:  recordStoreBlocks[1].DefRange.Ptr(),
		})
		rs, rsDiags := decodeRecordStoreBlock(recordStoreBlocks[0])
		diags = append(diags, rsDiags...)
		s.RecordStore = rs
	}

	switch len(strictBlocks) {
	case 0:
		// No strict block: Strict stays nil, which every reader must take
		// as "today's behavior", the same contract Policy and RecordStore
		// have above. See [Live.Strict].
	case 1:
		st, stDiags := decodeStrictBlock(strictBlocks[0])
		diags = append(diags, stDiags...)
		s.Strict = st
	default:
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Duplicate strict block",
			Detail:   "A live block may have at most one strict block.",
			Subject:  strictBlocks[1].DefRange.Ptr(),
		})
		st, stDiags := decodeStrictBlock(strictBlocks[0])
		diags = append(diags, stDiags...)
		s.Strict = st
	}

	return s, diags
}

// decodeStrictBlock decodes a live block's nested "strict" block: GitHub
// issue #365's profile toggles. See [LiveStrict].
func decodeStrictBlock(block *hcl.Block) (*LiveStrict, hcl.Diagnostics) {
	st := &LiveStrict{DeclRange: block.DefRange}

	content, diags := block.Body.Content(liveStrictSchema)

	if attr, exists := content.Attributes["marker_repair"]; exists {
		st.MarkerRepairRange = attr.Range
		val, valDiags := decodeLiteralString(attr, "marker_repair")
		diags = append(diags, valDiags...)
		if !valDiags.HasErrors() {
			st.MarkerRepair = val
			st.MarkerRepairSet = true
		}
	}

	// The markers blocks are collected by LABEL rather than counted, because
	// "record" is one of a family: the inverse selection this grammar leaves
	// room for is `markers "tag"`, and a configuration carrying both must be
	// two blocks rather than a duplicate. Two blocks with the SAME label are
	// the duplicate, and get the same diagnostic the policy and record_store
	// blocks give for theirs.
	seen := make(map[string]*hcl.Block, len(content.Blocks))
	for _, block := range content.Blocks {
		if block.Type != "markers" {
			continue
		}
		label := block.Labels[0]
		if label != string(strictMarkersRecord) {
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid markers selection",
				Detail: fmt.Sprintf(
					"markers %q names a marker carrier this fork does not know. The only one is %q, which means the selected resources hold their identity in the estate's record store instead of in an ownership marker tag.",
					label, strictMarkersRecord,
				),
				Subject: block.LabelRanges[0].Ptr(),
			})
			continue
		}
		if first, dup := seen[label]; dup {
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Duplicate markers block",
				Detail:   fmt.Sprintf("A strict block may have at most one markers %q block. Put every type and address in the one block.", label),
				Subject:  block.DefRange.Ptr(),
			})
			_ = first
			continue
		}
		seen[label] = block

		m, mDiags := decodeStrictMarkersBlock(block)
		diags = append(diags, mDiags...)
		st.MarkersRecord = m
	}

	return st, diags
}

// strictMarkersRecord is the one markers-block label this fork knows. It is
// a literal here and in internal/live/strict, which is the package that says
// what it MEANS; this one only has to recognize the spelling, the same
// division decodeRecordStoreBlock's three backend names already have.
const strictMarkersRecord = "record"

// decodeStrictMarkersBlock decodes one `markers "<kind>"` block: the two
// literal lists that say which resources it covers. See [LiveStrictMarkers].
func decodeStrictMarkersBlock(block *hcl.Block) (*LiveStrictMarkers, hcl.Diagnostics) {
	m := &LiveStrictMarkers{
		Kind:      block.Labels[0],
		KindRange: block.LabelRanges[0],
		DeclRange: block.DefRange,
	}

	content, diags := block.Body.Content(liveStrictMarkersSchema)

	for _, f := range []struct {
		name string
		val  *[]string
		set  *bool
		rng  *hcl.Range
	}{
		{"types", &m.Types, &m.TypesSet, &m.TypesRange},
		{"addresses", &m.Addresses, &m.AddressesSet, &m.AddressesRange},
	} {
		attr, exists := content.Attributes[f.name]
		if !exists {
			continue
		}
		*f.rng = attr.Range
		*f.set = true
		list, listDiags := decodeLiteralStringList(attr, f.name)
		diags = append(diags, listDiags...)
		*f.val = list
	}

	return m, diags
}

// decodeRecordStoreBlock decodes a live block's nested "record_store" block:
// which backend (the block's label) and that backend's own arguments. See
// [LiveRecordStore].
func decodeRecordStoreBlock(block *hcl.Block) (*LiveRecordStore, hcl.Diagnostics) {
	rs := &LiveRecordStore{DeclRange: block.DefRange}

	label := block.Labels[0]
	switch label {
	case "local", "ssm", "s3":
		rs.Type = label
		rs.TypeRange = block.LabelRanges[0]
	default:
		return rs, hcl.Diagnostics{&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid record_store backend",
			Detail:   fmt.Sprintf("record_store %q names a backend this fork does not know. Valid backends are \"local\" (the solo/dev default), \"ssm\" (the zero-infrastructure team default), and \"s3\" (true conditional-write CAS).", label),
			Subject:  block.LabelRanges[0].Ptr(),
		}}
	}

	content, diags := block.Body.Content(recordStoreBlockSchema)

	if attr, exists := content.Attributes["path"]; exists {
		rs.PathRange = attr.Range
		val, valDiags := decodeLiteralString(attr, "path")
		diags = append(diags, valDiags...)
		if !valDiags.HasErrors() {
			if val == "" {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Empty record_store path",
					Detail:   "The \"path\" argument was set to an empty string. Give it a path, or omit the argument entirely to use the default local record directory.",
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else if detail := validateRecordStorePath(val); detail != "" {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid record_store path",
					Detail:   detail,
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else {
				rs.Path = val
				rs.PathSet = true
			}
		}
	}

	if attr, exists := content.Attributes["bucket"]; exists {
		rs.BucketRange = attr.Range
		val, valDiags := decodeLiteralString(attr, "bucket")
		diags = append(diags, valDiags...)
		if !valDiags.HasErrors() {
			if val == "" {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Empty record_store bucket",
					Detail:   "The \"bucket\" argument was set to an empty string. The s3 record store backend needs a real bucket name.",
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else {
				rs.Bucket = val
				rs.BucketSet = true
			}
		}
	}
	if rs.Type == "s3" && !rs.BucketSet {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Missing record_store bucket",
			Detail:   "record_store \"s3\" requires a \"bucket\" argument naming the S3 bucket to store records in, the same way a stock \"s3\" backend block does.",
			Subject:  block.DefRange.Ptr(),
		})
	}

	if attr, exists := content.Attributes["key_prefix"]; exists {
		rs.KeyPrefixRange = attr.Range
		val, valDiags := decodeLiteralString(attr, "key_prefix")
		diags = append(diags, valDiags...)
		if !valDiags.HasErrors() {
			if detail := validateRecordStoreKeyPrefix(val); detail != "" {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid record_store key_prefix",
					Detail:   detail,
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else {
				rs.KeyPrefix = val
				rs.KeyPrefixSet = true
			}
		}
	}
	if rs.Type == "local" && rs.KeyPrefixSet {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid argument for the local record store",
			Detail:   "The \"key_prefix\" argument selects a namespace within a remote store and has no meaning for record_store \"local\", which already scopes every key to its own directory. Use \"path\" instead, or remove \"key_prefix\".",
			Subject:  rs.KeyPrefixRange.Ptr(),
		})
	}

	if attr, exists := content.Attributes["region"]; exists {
		rs.RegionRange = attr.Range
		val, valDiags := decodeLiteralString(attr, "region")
		diags = append(diags, valDiags...)
		if !valDiags.HasErrors() {
			if val == "" {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Empty record_store region",
					Detail:   "The \"region\" argument was set to an empty string. Give it a region, or omit the argument entirely to use the AWS SDK's own default-config chain.",
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else {
				rs.Region = val
				rs.RegionSet = true
			}
		}
	}
	if rs.Type == "local" && rs.RegionSet {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid argument for the local record store",
			Detail:   "The \"region\" argument selects an AWS region and has no meaning for record_store \"local\", which never talks to AWS. Remove it.",
			Subject:  rs.RegionRange.Ptr(),
		})
	}

	return rs, diags
}

// decodePolicyBlock decodes a live block's nested "policy" block: the four
// quadrant verbs, the optional tag_key/tag_value, the optional nested scope
// block, and the optional threshold. See [LivePolicy].
func decodePolicyBlock(block *hcl.Block) (*LivePolicy, hcl.Diagnostics) {
	p := &LivePolicy{DeclRange: block.DefRange}

	content, diags := block.Body.Content(livePolicySchema)

	for _, f := range []struct {
		name  string
		label string
		val   *string
		set   *bool
		rng   *hcl.Range
	}{
		{"declared_tagged", "declared_tagged", &p.DeclaredTagged, &p.DeclaredTaggedSet, &p.DeclaredTaggedRange},
		{"declared_untagged", "declared_untagged", &p.DeclaredUntagged, &p.DeclaredUntaggedSet, &p.DeclaredUntaggedRange},
		{"undeclared_tagged", "undeclared_tagged", &p.UndeclaredTagged, &p.UndeclaredTaggedSet, &p.UndeclaredTaggedRange},
		{"undeclared_untagged", "undeclared_untagged", &p.UndeclaredUntagged, &p.UndeclaredUntaggedSet, &p.UndeclaredUntaggedRange},
		{"tag_key", "tag_key", &p.TagKey, &p.TagKeySet, &p.TagKeyRange},
		{"tag_value", "tag_value", &p.TagValue, &p.TagValueSet, &p.TagValueRange},
	} {
		attr, exists := content.Attributes[f.name]
		if !exists {
			continue
		}
		*f.rng = attr.Range
		val, valDiags := decodeLiteralString(attr, f.label)
		diags = append(diags, valDiags...)
		if !valDiags.HasErrors() {
			*f.val = val
			*f.set = true
		}
	}

	if attr, exists := content.Attributes["threshold"]; exists {
		p.ThresholdRange = attr.Range
		val, valDiags := attr.Expr.Value(nil)
		diags = append(diags, valDiags...)
		switch {
		case valDiags.HasErrors():
			// attr.Expr.Value(nil) already names the variable or function
			// that makes this non-literal.
		case val.IsNull() || !val.IsWhollyKnown() || val.Type() != cty.Number:
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid threshold",
				Detail:   "The \"threshold\" argument is the delete quadrant's first-run guard and must be a literal number.",
				Subject:  attr.Expr.Range().Ptr(),
			})
		default:
			n, acc := val.AsBigFloat().Int64()
			if acc != big.Exact || n < 0 {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Invalid threshold",
					Detail:   "The \"threshold\" argument must be a non-negative whole number: it exists to be raised deliberately once a delete quadrant's roster has been reviewed, not to be fractional or negative.",
					Subject:  attr.Expr.Range().Ptr(),
				})
			} else {
				p.Threshold = int(n)
				p.ThresholdSet = true
			}
		}
	}

	switch len(content.Blocks) {
	case 0:
		// No scope block: Scope stays nil. internal/live/lint refuses this
		// when a quadrant is explicitly assigned "delete".
	case 1:
		scope, scopeDiags := decodePolicyScopeBlock(content.Blocks[0])
		diags = append(diags, scopeDiags...)
		p.Scope = scope
	default:
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Duplicate scope block",
			Detail:   "A policy block may have at most one scope block.",
			Subject:  content.Blocks[1].DefRange.Ptr(),
		})
		scope, scopeDiags := decodePolicyScopeBlock(content.Blocks[0])
		diags = append(diags, scopeDiags...)
		p.Scope = scope
	}

	return p, diags
}

// decodePolicyScopeBlock decodes a policy block's nested "scope" block: the
// services, types and regions lists that narrow a delete-quadrant verb's
// reach. See [LivePolicyScope].
func decodePolicyScopeBlock(block *hcl.Block) (*LivePolicyScope, hcl.Diagnostics) {
	s := &LivePolicyScope{DeclRange: block.DefRange}

	content, diags := block.Body.Content(livePolicyScopeSchema)

	for _, f := range []struct {
		name string
		val  *[]string
	}{
		{"services", &s.Services},
		{"types", &s.Types},
		{"regions", &s.Regions},
	} {
		attr, exists := content.Attributes[f.name]
		if !exists {
			continue
		}
		list, listDiags := decodeLiteralStringList(attr, f.name)
		diags = append(diags, listDiags...)
		*f.val = list
	}

	return s, diags
}

// decodeLiteralString reads attr as a required-literal-string argument,
// exactly the rule [decodeLiveBlock] applies to "estate":
// the value must be a literal (no variables or functions) and a string. label
// names the argument in the diagnostics this produces.
func decodeLiteralString(attr *hcl.Attribute, label string) (string, hcl.Diagnostics) {
	var diags hcl.Diagnostics
	val, valDiags := attr.Expr.Value(nil)
	diags = append(diags, valDiags...)
	if valDiags.HasErrors() {
		// attr.Expr.Value(nil) already names the variable or function that
		// makes this non-literal.
		return "", diags
	}
	if val.IsNull() || !val.IsWhollyKnown() || val.Type() != cty.String {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  fmt.Sprintf("Invalid %s", label),
			Detail:   fmt.Sprintf("The %q argument must be a literal string.", label),
			Subject:  attr.Expr.Range().Ptr(),
		})
		return "", diags
	}
	return val.AsString(), diags
}

// decodeLiteralStringList reads attr as a required-literal list-of-strings
// argument, the same literal-only rule as [decodeLiteralString] extended to a
// list: every element must be a known, non-null string, and the list itself
// must not be built from a variable or function call. label names the
// argument in the diagnostics this produces.
func decodeLiteralStringList(attr *hcl.Attribute, label string) ([]string, hcl.Diagnostics) {
	var diags hcl.Diagnostics
	val, valDiags := attr.Expr.Value(nil)
	diags = append(diags, valDiags...)
	if valDiags.HasErrors() {
		return nil, diags
	}
	if val.IsNull() || !val.IsWhollyKnown() {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  fmt.Sprintf("Invalid %s", label),
			Detail:   fmt.Sprintf("The %q argument must be a literal list of strings.", label),
			Subject:  attr.Expr.Range().Ptr(),
		})
		return nil, diags
	}
	ty := val.Type()
	if !ty.IsTupleType() && !ty.IsListType() && !ty.IsSetType() {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  fmt.Sprintf("Invalid %s", label),
			Detail:   fmt.Sprintf("The %q argument must be a literal list of strings.", label),
			Subject:  attr.Expr.Range().Ptr(),
		})
		return nil, diags
	}
	var out []string
	for it := val.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if ev.IsNull() || !ev.IsKnown() || ev.Type() != cty.String {
			diags = append(diags, &hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  fmt.Sprintf("Invalid %s", label),
				Detail:   fmt.Sprintf("Every element of the %q argument must be a literal string.", label),
				Subject:  attr.Expr.Range().Ptr(),
			})
			continue
		}
		out = append(out, ev.AsString())
	}
	return out, diags
}

// validateRecordStorePath returns the reason a record_store "local" path may
// not be used, or "" when it is fine.
//
// Everything the local store is allowed to do to the filesystem has to be
// judged as "what may this tool write over", and the answer is: a directory
// inside the module directory, that the operator named, that is not
// somebody's record of their infrastructure. These rules were first written
// for the (since-removed, issue #109) snapshot_path argument after an audit
// used the unchecked version to destroy a real terraform.tfstate and to
// write through "../../" into a sibling project; they survive here because
// the local record directory has exactly the same reach to constrain.
//
// The rules are all lexical, because the value is required to be a literal
// string: no filesystem is consulted here, and a configuration that would do
// something destructive is refused when it is read rather than when it is
// run.
func validateRecordStorePath(raw string) string {
	// Windows separators are normalized so that one set of rules covers both
	// spellings; a configuration is meant to be portable, and "..\\victim" is
	// the same request as "../victim".
	norm := strings.ReplaceAll(raw, "\\", "/")

	if strings.HasPrefix(norm, "/") || (len(norm) >= 2 && norm[1] == ':') {
		return "The \"path\" argument must be a relative path inside the module directory. It was given an absolute path, which would let the record store write anywhere on the machine running OpenTofu; the record store is not worth that reach."
	}

	cleaned := path.Clean(norm)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "The \"path\" argument must stay inside the module directory, and this path leaves it. A record directory that can be aimed at a sibling project is a way to overwrite that project's files from this configuration."
	}
	if cleaned == "." || cleaned == "" {
		return "The \"path\" argument names a directory to write records into, and this path names the module directory itself."
	}

	base := path.Base(cleaned)
	lower := strings.ToLower(base)
	if strings.HasPrefix(lower, "terraform.tfstate") || strings.HasSuffix(lower, ".tfstate") {
		return "The \"path\" argument must not name a state file. A record store holds effect records, not state; giving it a state file's name invites a later reader - a person, a script, a different tool - to treat it as the authoritative record this configuration exists to not have. It would also overwrite a real state file that happened to be there."
	}
	if first, _, _ := strings.Cut(cleaned, "/"); first == ".terraform" {
		return "The \"path\" argument must not write inside the .terraform directory. That directory is OpenTofu's own working data, including the record of which backend a directory was initialized against, and a record store does not get to overwrite it."
	}

	return ""
}

// validateRecordStoreKeyPrefix returns the reason a record_store "ssm" or
// "s3" key_prefix may not be used, or "" when it is fine.
//
// The rule that matters: five namespaces beside the records must stay
// unreachable from an override. The receipts pattern (live/RECEIPTS.md)
// owns "/tofu-receipts/<estate>/<effect>"; guided discovery's hint
// (issue #109) owns "tofu-hints/<estate>" in the same store - see
// internal/live/projection's hintNamespaceRoot; and record-located
// identities (issue #270) own "tofu-located/<estate>" - see that package's
// locatedNamespaceRoot. The stakes differ across the three. A record
// landing in the receipts namespace could collide with a receipt. A record
// prefix equal to the hint namespace would make orphan discovery's listing
// of the record namespace see the hint key and try to read it as a resource
// record. A record prefix equal to the LOCATED namespace is the worst of
// the three, because a record key with no configuration behind it is
// proposed for destruction and a located key names a live cloud object the
// record was never authority over. Namespace safety is
// disjoint-by-construction for the default (the caller's own key prefix is
// always rooted at a fourth literal, "tofu-records/<estate>" - see
// internal/live/projection.RecordKeyPrefix), but an operator-supplied
// override is checked here at the segment level, the same "/"-delimited
// hierarchy SSM parameter names and S3 key prefixes both already use: a
// key_prefix whose first segment is exactly "tofu-receipts", "tofu-hints",
// "tofu-located", "tofu-residue", "tofu-provisioned" or "tofu-outputs" is
// refused, whether or not it carries a leading or trailing slash.
//
// Residue (issue #275, internal/live/projection's residueNamespaceRoot) is
// the fourth and joined for "tofu-located"'s reason rather than
// "tofu-receipts"': it names arguments of live cloud objects the record
// namespace has no authority over. The provisioner taint (issue #353,
// internal/live/projection's provisionedNamespaceRoot) is the fifth and
// joined for the same reason: it is a note that a shell command failed
// against a live cloud object, and it must never be readable as a record
// whose absence of configuration means "destroy this". Root output values
// (issue #349, internal/live/projection's rootOutputNamespaceRoot) are the
// sixth, and the reason is the same one turned up a notch: an output names no
// live object whatsoever, so a key of theirs read as a record would propose
// destroying something that never existed.
func validateRecordStoreKeyPrefix(raw string) string {
	norm := strings.Trim(raw, "/")
	if norm == "" {
		return "The \"key_prefix\" argument was set to an empty (or all-slashes) string. Give it a real prefix, or omit the argument entirely to use the default derived from the estate name."
	}
	first, _, _ := strings.Cut(norm, "/")
	if first == "tofu-receipts" {
		return "The \"key_prefix\" argument must not begin with the \"tofu-receipts\" segment: that namespace belongs to the receipts pattern (live/RECEIPTS.md), and a record store's keys must stay disjoint from it so a record can never be mistaken for, or collide with, a receipt."
	}
	if first == "tofu-hints" {
		return "The \"key_prefix\" argument must not begin with the \"tofu-hints\" segment: that namespace belongs to guided discovery's hint, which lives beside the records in the same store, and a record's keys must stay disjoint from it so the listing that finds removed record-backed resources can never mistake the hint for one of them."
	}
	if first == "tofu-located" {
		return "The \"key_prefix\" argument must not begin with the \"tofu-located\" segment: that namespace holds the identities of live objects that have nowhere to carry an ownership marker (GitHub issue #270), and it is the one namespace whose keys must never be enumerable as records. A record key is read as the estate's inventory - a key with no configuration behind it is proposed for destruction - while a located key is only an answer to \"which object is this\". Overlapping the two would let a stale located key drive a cloud deletion."
	}
	if first == "tofu-residue" {
		return "The \"key_prefix\" argument must not begin with the \"tofu-residue\" segment: that namespace holds the argument values a provider's read never gives back (GitHub issue #275), for live objects the estate owns and the records have no authority over. It must stay unenumerable for the same reason \"tofu-located\" must - a record key with no configuration behind it is proposed for destruction, and a residue key is only a note about what was last sent to an object that exists."
	}
	if first == "tofu-outputs" {
		return "The \"key_prefix\" argument must not begin with the \"tofu-outputs\" segment: that namespace holds the value each root-level output block settled on at the last apply, which a stateless plan diffs against instead of calling every output new (GitHub issue #349). It must stay unenumerable for \"tofu-located\"'s reason - a record key with no configuration behind it is proposed for destruction, and an output value names no live object at all."
	}
	if first == "tofu-provisioned" {
		return "The \"key_prefix\" argument must not begin with the \"tofu-provisioned\" segment: that namespace holds the one bit saying a create-time provisioner failed on a live object (GitHub issue #353). It must stay unenumerable for \"tofu-located\"'s reason - a record key with no configuration behind it is proposed for destruction, and a provisioner note is only a record that a command failed against an object that exists."
	}
	return ""
}
