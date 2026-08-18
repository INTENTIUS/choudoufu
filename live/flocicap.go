// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// This file (package residue, colocated with residue.go and tagverbs.go for
// the same go:embed reason: an embed directive can only name files in its
// own package's directory, and live/floci-capabilities.json lives here) is
// the floci capability manifest: which AWS services and resource types the
// floci emulator this checkout pins actually implements, keyed by the
// image's content digest rather than its mutable tag.
//
// The problem this closes: every batch of e2e/floci-gated work that hits an
// unimplemented service rediscovers the gap from scratch - a Create call
// that returns UnknownOperationException, a service missing from
// /_localstack/health, a router that dispatches one service's calls to
// another's handler - and writes the finding up in prose, once, in that
// batch's own cohort README (see live/e2e/estates/databases/README.md and
// live/e2e/estates/stragglers/README.md's own "Floci coverage" sections) or
// in a hand-written t.Skip message (internal/live/discovery's
// tagging_live_test.go and cloudcontrol_live_test.go). The next batch that
// reaches the same type pays the same discovery cost again, because nothing
// upstream of "read every README" could have told it the answer was already
// known. live/floci-capabilities.json is that answer, structured and keyed
// by the exact digest the finding was made against, so a test can look it
// up instead of a human re-deriving it, and a bumped image's stale entries
// are visibly stale (no entry for the new digest) rather than silently
// wrong.
//
// The manifest has two grains:
//
//   - Services (botocore/AWS CLI service ids, e.g. "networkmanager",
//     "transfer"): whether floci implements the service at all, read off
//     floci's own /_localstack/health endpoint by
//     tools/floci-capability-gen's default probe mode. Note the grain's
//     coverage is its own watchlist rather than the health response: the
//     probe re-checks every service already carrying a row and records
//     nothing for the rest, so the four rows here sit against 82 service
//     names the pinned image reports (#276).
//   - Types (Terraform provider-local resource types, e.g.
//     "aws_redshift_cluster"), each optionally scoped to a discovery
//     mechanism other than the ordinary create/read path
//     ("tagging-sweep", "cloudcontrol-list", "cloudcontrol-list-scoped" -
//     internal/live/flocitest's *CapabilityGate helpers use the same
//     vocabulary). "cloudcontrol-list-scoped" is "cloudcontrol-list"'s
//     counterpart for a type whose Cloud Control list handler requires
//     scoping input (registry.Roster.EnumerationSourceScoped, the exact
//     population EnumerationSource excludes) - tools/floci-capability-gen's
//     -mode=cloudcontrol-scoped probes it the same way, with a synthetic
//     placeholder scope rather than a real parent object, because floci's
//     ListResources ignores ResourceModel scoping entirely regardless of
//     which mechanism sent it (issue #277). This grain is finer
//     than a health-endpoint probe can answer by itself: a service can
//     report "running" while a specific operation it exposes is still
//     unimplemented, or routed to the wrong handler, or reaches a handler
//     that crashes rather than erroring cleanly. Most entries here are
//     curated from a real `terraform apply` (or a real Cloud
//     Control/tagging call) run against the exact pinned digest and written
//     up once, the same hand-verification the two cohort READMEs above
//     already did - this file just gives that verification a permanent,
//     structured, queryable home instead of only prose a later reader has
//     to find and re-read. tools/floci-capability-gen's -mode=cloudcontrol
//     regenerates the "cloudcontrol-list" mechanism's entries mechanically,
//     over every registry-ratified type, by round trip: create a resource
//     of the type through Cloud Control, then list and look for the
//     identifier the create just named. That round trip is the only way to
//     tell a list that answers from one that returns - floci's ListResources
//     answers an empty ResourceDescriptions, cleanly, for a type whose
//     objects demonstrably exist, and an earlier sweep that recorded a bare
//     successful call as "implemented" filled this manifest with 645 rows
//     that said so. Re-probed as a round trip against the same image, seven
//     of 610 hold (#279). The rest of the grain stays hand data, extended
//     the same way tools/estate-gen/overrides.go's typeOverrides table is.
//
// The three mechanisms are not interchangeable evidence for "does floci
// support this type", and reading one as a substitute for another is
// exactly the mistake issue #278 named:
//
//   - mechanism="" (the ordinary create/read path) is the only grain that
//     answers what a real `tofu apply` needs: a create call that succeeds
//     and a read/import call that returns usable properties. It is also by
//     far the narrowest of the three - see live/floci-capabilities.json's
//     own row counts per mechanism for the current pin, rather than a
//     number here that would go stale - because there is no derivable
//     minimal create recipe per type and every row is hand-curated one type
//     at a time (issue #278's second and third options, neither attempted
//     yet).
//   - "cloudcontrol-list" is the broadest grain and the weakest: its probe
//     (tools/floci-capability-gen -mode=cloudcontrol) never creates
//     anything and never reads a populated object. "implemented" means only
//     that a bare ListResources call against an arbitrary (possibly empty)
//     collection did not return UnsupportedOperation or an unparseable
//     error - nothing about whether Create would succeed, whether the
//     properties a real read would need come back populated, or whether the
//     provider's own API calls (which are not Cloud Control's) work at all.
//     Two documented cases where this grain and the ordinary path disagree:
//     aws_athena_named_query and aws_cloudwatch_query_definition both record
//     "implemented" here on ListResources evidence, while the API the
//     provider itself calls for each returns UnsupportedOperation; and
//     CloudFront's list-public-keys answers with every item's Name unset,
//     so a "successful" list is not a usable one. Treat a "cloudcontrol-list"
//     row as evidence for internal/live/discovery's Cloud Control fallback
//     working, never as evidence that a plain `resource` block for that type
//     will apply.
//   - "tagging-sweep" sits in between: its probe creates one real, tagged
//     resource through the AWS CLI directly (never through Terraform or
//     Cloud Control) and confirms it independently through that service's
//     own native read, so "implemented" here is real evidence the type's
//     native create/read path exists in floci - but it only ever proves
//     that for the small, hand-curated set of types tagging.go's
//     taggingRecipes covers, and it says nothing about whether the
//     resourcegroupstaggingapi sweep itself (the thing this mechanism
//     actually gates) would find a *different* type's resources.
//
// A digest with no entry at all, a service/type with no row under a digest
// that does have entries, or a row recorded under a *different* mechanism
// than the one a caller is about to exercise, all mean "not yet
// investigated" for the question actually being asked - never "confirmed
// working". [FlociServiceCapability] and [FlociTypeCapability] report
// ok=false only for the first two; picking the right mechanism argument for
// the third is the caller's job, and [FlociTypeCapability]'s own doc comment
// says which mechanism answers which question.
package residue

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed floci-capabilities.json
var flociCapabilitiesJSONBytes []byte

// FlociStatus is floci's own implementation status for one AWS service or
// resource type, as recorded against one exact image digest.
type FlociStatus string

const (
	// FlociImplemented is a service or type floci implements well enough
	// that the finding recorded it working, possibly with caveats spelled
	// out in the Evidence text.
	FlociImplemented FlociStatus = "implemented"

	// FlociUnimplemented is a service or type floci's router refuses
	// outright (UnknownOperationException, UnsupportedOperation, or a
	// service missing from /_localstack/health entirely).
	FlociUnimplemented FlociStatus = "unimplemented"

	// FlociBroken is a type whose call reaches a router-recognized handler
	// that then fails in a way no client can recover from - floci's own
	// implementation bug, not an absent one (the HTML-error-page shape
	// live/e2e/estates/databases/README.md's "Floci coverage" section
	// documents for aws_docdbelastic_cluster and aws_qldb_ledger is the
	// running example).
	FlociBroken FlociStatus = "broken"

	// FlociPartial is a service or type that works only under a condition
	// this harness does not meet by default (aws_opensearch_domain's own
	// Docker-socket-mount requirement is the running example), or that
	// works for some but not all of its own operations.
	FlociPartial FlociStatus = "partial"

	// FlociUnverified is a probe that reached a real handler and got an
	// ordinary answer back, without that answer establishing anything: the
	// call returned, and nothing showed whether the service actually
	// answered it. tools/floci-capability-gen's cloudcontrol-list sweep
	// writes this when it could not create a resource of the type to then
	// look for in the list, which leaves an empty ResourceDescriptions
	// indistinguishable between "nothing exists" and "this list handler is
	// a stub".
	//
	// Read it exactly the way an absent row is read - not yet established,
	// never a clearance - which is why the *CapabilityGate helpers leave a
	// test running rather than skipping it. It exists as a distinct status
	// so a reader can tell "probed, and the probe settled nothing" from
	// "never probed".
	FlociUnverified FlociStatus = "unverified"
)

// FlociCapability is one manifest entry: what happened when this repo tried
// to use a service or type against one specific floci image, and where that
// finding is written up in full.
type FlociCapability struct {
	// Status is floci's implementation status for this digest.
	Status FlociStatus

	// Evidence is the concrete signal observed - an error code, a response
	// shape, a health-endpoint absence - not a guess.
	Evidence string

	// Source cites where the finding was made: a cohort README's "Floci
	// coverage" section, a Go test's own name, or "live probe" for an entry
	// tools/floci-capability-gen wrote mechanically.
	Source string
}

type flociServiceRow struct {
	Service  string `json:"service"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	Source   string `json:"source"`
}

type flociTypeRow struct {
	Type      string `json:"type"`
	Mechanism string `json:"mechanism,omitempty"`
	Status    string `json:"status"`
	Evidence  string `json:"evidence"`
	Source    string `json:"source"`
}

type flociImageArtifact struct {
	Digest   string            `json:"digest"`
	Ref      string            `json:"ref"`
	Services []flociServiceRow `json:"services"`
	Types    []flociTypeRow    `json:"types"`
}

type flociCapabilitiesArtifact struct {
	GeneratedBy string               `json:"generated_by"`
	Images      []flociImageArtifact `json:"images"`
}

type flociState struct {
	services map[string]map[string]flociServiceRow // digest -> service -> row
	types    map[string]map[string]flociTypeRow    // digest -> (type,mechanism) -> row
}

var (
	flociOnce   sync.Once
	flociGlobal flociState
)

// flociValidStatus is the closed vocabulary every embedded row's status
// must belong to - a typo here should fail loud at load time, not read back
// as silently unrecognized.
var flociValidStatus = map[string]bool{
	string(FlociImplemented):   true,
	string(FlociUnimplemented): true,
	string(FlociBroken):        true,
	string(FlociPartial):       true,
	string(FlociUnverified):    true,
}

func loadFlociCapabilities() *flociState {
	flociOnce.Do(func() {
		var art flociCapabilitiesArtifact
		if err := json.Unmarshal(flociCapabilitiesJSONBytes, &art); err != nil {
			panic(fmt.Sprintf("residue: decoding embedded floci-capabilities.json: %v", err))
		}
		flociGlobal.services = make(map[string]map[string]flociServiceRow, len(art.Images))
		flociGlobal.types = make(map[string]map[string]flociTypeRow, len(art.Images))
		for _, img := range art.Images {
			if img.Digest == "" {
				panic("residue: floci-capabilities.json has an image entry with no digest")
			}
			svcIdx := make(map[string]flociServiceRow, len(img.Services))
			for _, row := range img.Services {
				if !flociValidStatus[row.Status] {
					panic(fmt.Sprintf("residue: floci-capabilities.json: image %s, service %s has unrecognized status %q", img.Digest, row.Service, row.Status))
				}
				svcIdx[row.Service] = row
			}
			flociGlobal.services[img.Digest] = svcIdx

			typeIdx := make(map[string]flociTypeRow, len(img.Types))
			for _, row := range img.Types {
				if !flociValidStatus[row.Status] {
					panic(fmt.Sprintf("residue: floci-capabilities.json: image %s, type %s has unrecognized status %q", img.Digest, row.Type, row.Status))
				}
				typeIdx[flociTypeKey(row.Type, row.Mechanism)] = row
			}
			flociGlobal.types[img.Digest] = typeIdx
		}
	})
	return &flociGlobal
}

func flociTypeKey(tfType, mechanism string) string {
	return tfType + "\x00" + mechanism
}

// FlociServiceCapability reports what the capability manifest says about
// one AWS service - a botocore/AWS CLI service id such as "networkmanager"
// or "transfer" - against the given floci image digest (a bare
// "sha256:<hex>", the same form internal/live/flocitest's pinned image and
// FLOCI_IMAGE override resolve to). ok is false when the manifest carries
// no entry for this digest+service at all, which callers must treat as
// "not yet investigated", never as "confirmed working" or "confirmed
// missing" - silence here means nobody has recorded a finding.
func FlociServiceCapability(digest, service string) (FlociCapability, bool) {
	s := loadFlociCapabilities()
	byService, ok := s.services[digest]
	if !ok {
		return FlociCapability{}, false
	}
	row, ok := byService[service]
	if !ok {
		return FlociCapability{}, false
	}
	return FlociCapability{Status: FlociStatus(row.Status), Evidence: row.Evidence, Source: row.Source}, true
}

// FlociTypeCapability is [FlociServiceCapability]'s finer-grained twin: a
// Terraform provider-local resource type (e.g. "aws_redshift_cluster"),
// scoped to exactly one discovery mechanism via mechanism. Same ok=false
// "not yet investigated" caveat as [FlociServiceCapability] - and here it
// also covers a mechanism mismatch: a row recorded under
// "cloudcontrol-list" is invisible to a call with mechanism="", and vice
// versa. That is deliberate, not a gap to work around by trying every
// mechanism until one returns ok=true - see this file's package doc for why
// (issue #278). The three values mean:
//
//   - "" - the ordinary create/read path, the one a plain `resource` block
//     and a real `tofu apply` actually take. This is the only mechanism
//     whose "implemented" answers "does floci support this type" in the
//     sense most callers mean by that question. Pass this when deciding
//     whether to attempt or skip driving floci through a type's normal
//     lifecycle.
//   - "cloudcontrol-list" - internal/live/discovery's Cloud Control
//     enumeration fallback (SourceCloudControl). Its "implemented" proves
//     only that Cloud Control's ListResources call did not error for that
//     type; it is not evidence the type's own create/read path works, and
//     two types are on record disagreeing (see the package doc). Pass this
//     only when the code under test is itself the Cloud Control discovery
//     path, e.g. internal/live/flocitest.CloudControlListCapabilityGate.
//   - "tagging-sweep" - internal/live/discovery's tagging-sweep enumeration
//     path (SourceTagging). Pass this only when the code under test is
//     itself that path, e.g.
//     internal/live/flocitest.TaggingSweepCapabilityGate.
//
// A caller outside internal/live/flocitest's three named *CapabilityGate
// wrappers should have a specific reason to call this directly with a
// mechanism other than "" - and that reason should be "the code under test
// takes that mechanism's own path", never "that's the mechanism with a row
// for this type".
func FlociTypeCapability(digest, tfType, mechanism string) (FlociCapability, bool) {
	s := loadFlociCapabilities()
	byType, ok := s.types[digest]
	if !ok {
		return FlociCapability{}, false
	}
	row, ok := byType[flociTypeKey(tfType, mechanism)]
	if !ok {
		return FlociCapability{}, false
	}
	return FlociCapability{Status: FlociStatus(row.Status), Evidence: row.Evidence, Source: row.Source}, true
}
