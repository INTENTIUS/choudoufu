// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/pins"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #2141's census: how many admitted types can
// reach [SummaryUntaggableUnrecorded] at all. Issue #855 measured the
// sensitive-identity part of writeBackRecordEnvelopes' unrecordable branch
// for one slice of it (TestUntaggableUnlistableSensitivePopulation); nobody
// had counted the "cannot hold in full" part.
//
// The population is a type that is
//
//   - admitted, by a ratified row ([identity.LookupType]) or a synthesized
//     one ([identity.SynthesizeTypeIdentity], which is also where an
//     azurerm type's ARM id template admits it), and not record-backed
//     (writeBackRecordEnvelopes skips those);
//   - not served by the located route ([identity.LocatedType]): an instance
//     of such a type takes the loud "Cannot record a located identity" arm
//     instead of this branch;
//   - without a marker surface, by its family's own answer
//     ([unmarkedUnrecorded], the same predicate the warning uses);
//   - one whose identity [LocatedRecordFromIn] cannot hold, judged from the
//     type alone ([locatedRecordTypeHolds]).
//
// The last condition is type-level. LocatedRecordFromIn can also fail for
// one object (a component the object does not carry, an azurerm id that
// does not rebuild), and the warning catches those at apply; no census can
// count them without objects.

// census pins, measured 2026-10-20 against the providers the refusal probe
// and the Azure lane pin. A provider bump that moves any of them fails here
// by name.
const (
	censusAWSVersion     = "6.59.0"
	censusAzurermVersion = "5.10.0"
	censusAzapiVersion   = "2.13.0"
)

// wantAWSCensus and wantAzurermCensus are the measured populations. Every
// one of them draws [SummaryUntaggableUnrecorded] on an apply that is not
// routed through a record door.
var (
	wantAWSCensus = []string{
		"aws_bedrock_foundation_model_agreement",
		"aws_cloudwatch_log_delivery_destination_policy",
		"aws_cloudwatch_log_index_policy",
		"aws_cognito_log_delivery_configuration",
		"aws_organizations_aws_service_access",
		"aws_osis_resource_policy",
		"aws_ram_resource_share_associations_exclusive",
		"aws_s3_bucket_metadata_configuration",
		"aws_s3control_multi_region_access_point_routes",
		"aws_s3files_synchronization_configuration",
		"aws_s3tables_table_bucket_replication",
		"aws_s3tables_table_replication",
		"aws_workmail_default_domain",
		"aws_xray_indexing_rule",
	}
	wantAzurermCensus = []string{}
	wantAzapiCensus   = []string{}
)

// The pinned counts, beside the lists so a reader of a failure sees both.
const (
	wantAWSCensusCount     = 14
	wantAzurermCensusCount = 0
	wantAzapiCensusCount   = 0
)

// locatedRecordTypeHolds reports whether [LocatedRecordFromIn] can hold
// resourceType's identity for an object that carries every attribute the
// identity reads: its two routes are the recordable identity schema
// ([identity.RecordableIdentitySchema], which already folds in the
// documented-grammar and bare-id answers and the sensitivity refusal) and,
// when that refuses, the ratified row's components
// ([ratifiedComponentsRecordable]). The same two calls, in the same order,
// so it cannot answer for the type differently than the function does.
func locatedRecordTypeHolds(resourceType string, schema providers.Schema) bool {
	if identity.RecordableIdentitySchema(resourceType, schema) {
		return true
	}
	_, ok := ratifiedComponentsRecordable(resourceType, schema)
	return ok
}

// unrecordedCensus measures the population over one provider's schemas.
func unrecordedCensus(providerType string, schemas map[string]providers.Schema) (population []string, admitted, unmarked int) {
	for name, schema := range schemas {
		if schema.Block == nil {
			continue
		}
		ti, ratified := identity.LookupType(name)
		if ratified && ti.RecordBacked {
			continue
		}
		if !ratified {
			if _, ok := identity.SynthesizeTypeIdentity(name, schemas, nil); !ok {
				continue
			}
		}
		if identity.LocatedType(name, schemas) {
			continue
		}
		admitted++
		if !unmarkedUnrecorded(providerType, schema) {
			continue
		}
		unmarked++
		if locatedRecordTypeHolds(name, schema) {
			continue
		}
		population = append(population, name)
	}
	sort.Strings(population)
	return population, admitted, unmarked
}

// TestUnrecordedUntaggableCensusIsAtThePin is the census's offline half: a
// bump of the AWS pin fails here, in every CI run, until the census below is
// re-measured at the new version and its pin moved with it.
func TestUnrecordedUntaggableCensusIsAtThePin(t *testing.T) {
	if pins.AWSProviderVersion != censusAWSVersion {
		t.Fatalf("hashicorp/aws is pinned at %s but issue #2141's census was measured at %s: re-run "+
			"TestUnrecordedUntaggableCensus with CHOUDOUFU_LIVE_SCHEMAS=1, update wantAWSCensus by its "+
			"diff, and move censusAWSVersion", pins.AWSProviderVersion, censusAWSVersion)
	}
}

// censusProvider is one provider the census measures.
type censusProvider struct {
	provider, version string
	want              []string
}

// source is the provider's registry address: azapi is published by Azure,
// the other two by HashiCorp.
func (c censusProvider) source() string {
	if c.provider == "azapi" {
		return "azure/azapi"
	}
	return "hashicorp/" + c.provider
}

// TestUnrecordedUntaggableCensusCountsMatchTheirLists keeps the pinned
// counts and the pinned lists from drifting apart by hand.
func TestUnrecordedUntaggableCensusCountsMatchTheirLists(t *testing.T) {
	for _, c := range []struct {
		name  string
		list  []string
		count int
	}{
		{"aws", wantAWSCensus, wantAWSCensusCount},
		{"azurerm", wantAzurermCensus, wantAzurermCensusCount},
		{"azapi", wantAzapiCensus, wantAzapiCensusCount},
	} {
		if len(c.list) != c.count {
			t.Errorf("%s: the census pins %d types by name but its count says %d", c.name, len(c.list), c.count)
		}
	}
}

// TestUnrecordedUntaggableCensus is the measurement, against the real
// provider schemas.
func TestUnrecordedUntaggableCensus(t *testing.T) {
	if os.Getenv("CHOUDOUFU_LIVE_SCHEMAS") == "" {
		t.Skip("set CHOUDOUFU_LIVE_SCHEMAS=1 to install hashicorp/aws, hashicorp/azurerm and azure/azapi and measure issue #2141's census")
	}
	for _, tc := range []censusProvider{
		{"aws", censusAWSVersion, wantAWSCensus},
		{"azurerm", censusAzurermVersion, wantAzurermCensus},
		{"azapi", censusAzapiVersion, wantAzapiCensus},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
				InitBin:  "terraform",
				WorkDir:  t.TempDir(),
				Source:   tc.source(),
				Version:  tc.version,
				Provider: addrs.MustParseProviderSourceString(tc.source()),
			})
			if err != nil {
				t.Fatalf("acquiring %s %s schemas: %s", tc.source(), tc.version, err)
			}
			got, admitted, unmarked := unrecordedCensus(tc.provider, schemas)
			var sensitive []string
			for _, name := range got {
				if identity.SensitiveIdentityAttr(name, schemas[name]) != "" {
					sensitive = append(sensitive, name)
				}
			}
			t.Logf("%s %s: resource types=%d admitted(not located)=%d no-marker-surface=%d census=%d (of which sensitive identity=%d)",
				tc.source(), tc.version, len(schemas), admitted, unmarked, len(got), len(sensitive))
			t.Logf("census (%d): %s", len(got), strings.Join(got, ", "))
			if added, removed := diffNames(tc.want, got); added != "" || removed != "" {
				t.Errorf("issue #2141's census for %s %s moved: added=[%s] removed=[%s] (pinned %d, measured %d). "+
					"Each added type is one whose removal would be lost silently but for the warning: read why before re-pinning",
					tc.source(), tc.version, added, removed, len(tc.want), len(got))
			}
		})
	}
}

// diffNames names exactly what moved between a pinned population and a
// measured one, the shape internal/live/identity's diffPinned takes.
func diffNames(want, got []string) (added, removed string) {
	in := func(list []string) map[string]bool {
		m := make(map[string]bool, len(list))
		for _, s := range list {
			m[s] = true
		}
		return m
	}
	wantSet, gotSet := in(want), in(got)
	var a, r []string
	for _, g := range got {
		if !wantSet[g] {
			a = append(a, g)
		}
	}
	for _, w := range want {
		if !gotSet[w] {
			r = append(r, w)
		}
	}
	sort.Strings(a)
	sort.Strings(r)
	return strings.Join(a, ", "), strings.Join(r, ", ")
}
