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
//     ("tagging-sweep", "cloudcontrol-list" - internal/live/flocitest's
//     *CapabilityGate helpers use the same vocabulary). This grain is finer
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
// A digest with no entry at all, or a service/type with no row under a
// digest that does have entries, means "not yet investigated" - never
// "confirmed working". [FlociServiceCapability] and [FlociTypeCapability]
// both report ok=false for that case, and callers must not read that as a
// clean bill of health.
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
// optionally scoped to a discovery mechanism other than the ordinary
// create/read path via mechanism ("tagging-sweep", "cloudcontrol-list" -
// internal/live/flocitest's *CapabilityGate helpers use the same
// vocabulary). mechanism "" means the ordinary path. Same ok=false
// "not yet investigated" caveat as [FlociServiceCapability].
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
