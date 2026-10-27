// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/adoption"
)

// GitHub issue #2287: live-import -adopt over a fake cloud. Nothing here
// touches a network. The fake answers ImportResourceState with an object
// carrying only the id it was given, and ReadResource from its own object
// map, so what a refusal is decided on is the fake's live object and never
// the index node's tags.

type adoptCloud struct {
	objects map[string]cty.Value // "type/id" -> live object
	imports []string             // "type/id" of every ImportResourceState
	reads   []string
	applied []string
}

func adoptSchemas() map[string]providers.Schema {
	tagged := func() providers.Schema {
		return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		}}}
	}
	return map[string]providers.Schema{
		"aws_s3_bucket": tagged(),
		"aws_lb":        tagged(),
		"aws_alb":       tagged(),
		"aws_key_pair":  tagged(),
	}
}

func adoptObject(id string, tags map[string]string) cty.Value {
	t := cty.MapValEmpty(cty.String)
	if len(tags) > 0 {
		m := map[string]cty.Value{}
		for k, v := range tags {
			m[k] = cty.StringVal(v)
		}
		t = cty.MapVal(m)
	}
	return cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal(id), "tags": t})
}

func (c *adoptCloud) provider() *tofu.MockProvider {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: adoptSchemas(),
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		c.imports = append(c.imports, r.TypeName+"/"+r.Target.ID)
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"id":   cty.StringVal(r.Target.ID),
				"tags": cty.NullVal(cty.Map(cty.String)),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		key := r.TypeName + "/" + r.PriorState.GetAttr("id").AsString()
		c.reads = append(c.reads, key)
		obj, ok := c.objects[key]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: obj}
	}
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		key := r.TypeName + "/" + r.PriorState.GetAttr("id").AsString()
		c.objects[key] = r.PlannedState
		c.applied = append(c.applied, key)
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return p
}

const adoptAccount = "000000000000"

func adoptNode(cfnType, native string, tags map[string]string) *adoption.Node {
	return &adoption.Node{
		ID:         "aws/" + adoptAccount + "/us-east-1/" + cfnType + "/" + native,
		Kind:       "resource",
		Lexicon:    "aws",
		ListedType: cfnType,
		Location:   "us-east-1",
		NativeID:   native,
		Tags:       tags,
	}
}

func adoptionOf(entries ...adoption.Entry) *adoption.Adoption {
	return &adoption.Adoption{
		File: &adoption.File{
			Adopt: adoption.FormatVersion,
			Index: adoption.IndexRef{
				ScopeKey: "cloud/aws/" + adoptAccount + "/9bd41033bb42",
				Version:  "20261026T120000Z-e2f92b265a4d.json",
				Digest:   "e2f92b265a4dd604e013b3f55eafe5cf317ea1f5b7c54b4c5187120d0dcfe10d",
			},
		},
		Entries: entries,
	}
}

func entry(node *adoption.Node, as, typ string) adoption.Entry {
	return adoption.Entry{Selection: adoption.Selection{Node: node.ID, As: as, Type: typ}, Node: node}
}

func ratifyAdoption(t *testing.T, cloud *adoptCloud, a *adoption.Adoption) *AdoptRatification {
	t.Helper()
	rat, diags := RatifyAdoption(context.Background(), AdoptRequest{
		Request:  Request{Estate: testEstate, Providers: &fakeProviders{provider: cloud.provider()}},
		Adoption: a,
	})
	if diags.HasErrors() {
		t.Fatalf("RatifyAdoption: %s", diags.Err())
	}
	if len(cloud.applied) != 0 {
		t.Fatalf("RatifyAdoption applied %v; it must never write", cloud.applied)
	}
	return rat
}

func refusalFor(rat *AdoptRatification, as string) (AdoptRefusal, bool) {
	for _, r := range rat.Refused {
		if r.As == as {
			return r, true
		}
	}
	return AdoptRefusal{}, false
}

// The ordinary case: a clean bucket is imported by its Cloud Control
// identifier, verified, listed as one tags-only write, and stamped at
// -approve by one write.
func TestAdopt_cleanBucketIsOneTagsOnlyWrite(t *testing.T) {
	cloud := &adoptCloud{objects: map[string]cty.Value{
		"aws_s3_bucket/web-logs": adoptObject("web-logs", map[string]string{"team": "web"}),
	}}
	node := adoptNode("AWS::S3::Bucket", "web-logs", nil)
	rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_s3_bucket.web_logs", "")))

	if len(rat.Refused) != 0 {
		t.Fatalf("refused %+v", rat.Refused)
	}
	if len(cloud.imports) != 1 || cloud.imports[0] != "aws_s3_bucket/web-logs" {
		t.Fatalf("imports = %v, want the bucket by its name", cloud.imports)
	}
	if len(rat.Entries) != 1 || rat.Entries[0].Status != StatusVerified {
		t.Fatalf("entries = %+v, want one VERIFIED", rat.Entries)
	}
	writes := rat.MarkerWrites()
	if len(writes) != 1 {
		t.Fatalf("marker writes = %+v, want one", writes)
	}
	w := writes[0]
	if w.ImportID != "web-logs" || w.Node != node.ID {
		t.Errorf("write = %+v", w)
	}
	if len(w.Tags) != 2 || w.Tags["tofu-estate"] != testEstate || w.Tags["tofu-address"] != "aws_s3_bucket.web_logs" {
		t.Errorf("write tags = %v, want tofu-estate and tofu-address only", w.Tags)
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Outcome != OutcomeStamped {
		t.Fatalf("outcomes = %+v, want one STAMPED", rep.Outcomes)
	}
	if len(cloud.applied) != 1 {
		t.Fatalf("applied %v, want exactly one write", cloud.applied)
	}
	tags := cloud.objects["aws_s3_bucket/web-logs"].GetAttr("tags").AsValueMap()
	if tags["team"].AsString() != "web" || tags["tofu-estate"].AsString() != testEstate {
		t.Errorf("live tags after approve = %v", tags)
	}
}

// A type whose tags can be set only at create is refused by name before any
// provider call.
func TestAdopt_refusesCreateOnlyTags(t *testing.T) {
	cloud := &adoptCloud{objects: map[string]cty.Value{
		"aws_key_pair/deployer": adoptObject("deployer", nil),
	}}
	node := adoptNode("AWS::EC2::KeyPair", "deployer", nil)
	rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_key_pair.deployer", "")))

	r, ok := refusalFor(rat, "aws_key_pair.deployer")
	if !ok {
		t.Fatalf("not refused; entries %+v", rat.Entries)
	}
	if !strings.Contains(r.Reason, "AWS::EC2::KeyPair") || !strings.Contains(r.Reason, "only when it is created") {
		t.Errorf("reason = %q", r.Reason)
	}
	if len(cloud.imports)+len(cloud.reads) != 0 {
		t.Errorf("provider was called (imports %v, reads %v); the refusal comes first", cloud.imports, cloud.reads)
	}
	if len(rat.Entries) != 0 || len(rat.MarkerWrites()) != 0 {
		t.Errorf("a refused selection reached ratification: %+v", rat.Entries)
	}
}

// An inventory type that joins to more than one Terraform type is refused
// with every candidate unless the selection names one.
func TestAdopt_ambiguousTypeNeedsTheEngineType(t *testing.T) {
	const arn = "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/web/50dc6c495c0c9188"
	node := adoptNode("AWS::ElasticLoadBalancingV2::LoadBalancer", arn, nil)

	cloud := &adoptCloud{objects: map[string]cty.Value{}}
	rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_lb.web", "")))
	r, ok := refusalFor(rat, "aws_lb.web")
	if !ok {
		t.Fatalf("not refused; entries %+v", rat.Entries)
	}
	if !strings.Contains(r.Reason, "aws_alb") || !strings.Contains(r.Reason, "aws_lb") {
		t.Errorf("reason %q does not name every candidate", r.Reason)
	}
	if len(cloud.imports) != 0 {
		t.Errorf("provider was called for an ambiguous type: %v", cloud.imports)
	}

	// Named, it goes through the ordinary read.
	cloud = &adoptCloud{objects: map[string]cty.Value{}}
	rat = ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_lb.web", "aws_lb")))
	if r, ok := refusalFor(rat, "aws_lb.web"); ok && strings.Contains(r.Reason, "must name one") {
		t.Errorf("refused as ambiguous with its type named: %q", r.Reason)
	}
	if len(cloud.imports) != 1 {
		t.Errorf("imports = %v, want one read of the named type", cloud.imports)
	}

	// A named type the listed type does not join to is refused too.
	cloud = &adoptCloud{objects: map[string]cty.Value{}}
	rat = ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_s3_bucket.web", "aws_s3_bucket")))
	if _, ok := refusalFor(rat, "aws_s3_bucket.web"); !ok {
		t.Errorf("a type the listed type does not join to was not refused")
	}
}

// The live read decides: an index node recorded clean whose live object now
// carries another owner's marker is refused, for each engine's marker.
func TestAdopt_refusesAnotherOwnersLiveMarker(t *testing.T) {
	for name, tags := range map[string]map[string]string{
		"another estate":               {"tofu-estate": "other-team", "tofu-address": "aws_s3_bucket.x"},
		"plunge stack":                 {"plunge-stack": "acme/prod", "plunge-urn": "urn:pulumi:prod::acme::aws:s3/bucket:Bucket::x"},
		"chant stack":                  {"chant:managed-by": "chant", "chant:stack": "web"},
		"this estate, another address": {"tofu-estate": testEstate, "tofu-address": "aws_s3_bucket.elsewhere"},
	} {
		t.Run(name, func(t *testing.T) {
			cloud := &adoptCloud{objects: map[string]cty.Value{
				"aws_s3_bucket/stale": adoptObject("stale", tags),
			}}
			// The index saw no markers on it.
			node := adoptNode("AWS::S3::Bucket", "stale", map[string]string{})
			rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_s3_bucket.stale", "")))
			r, ok := refusalFor(rat, "aws_s3_bucket.stale")
			if !ok {
				t.Fatalf("not refused; entries %+v", rat.Entries)
			}
			if r.ImportID != "stale" || r.Reason == "" {
				t.Errorf("refusal = %+v", r)
			}
			if len(rat.MarkerWrites()) != 0 {
				t.Errorf("a refused object is listed for a marker write")
			}
			rep, _ := rat.Approve(context.Background())
			if len(cloud.applied) != 0 {
				t.Errorf("Approve wrote %v over another owner's marker (outcomes %+v)", cloud.applied, rep.Outcomes)
			}
		})
	}
}

// The other direction: the index recorded another estate's marker, the live
// object no longer carries it, and the live read is what counts.
func TestAdopt_liveReadOverridesTheIndexTags(t *testing.T) {
	cloud := &adoptCloud{objects: map[string]cty.Value{
		"aws_s3_bucket/freed": adoptObject("freed", nil),
	}}
	node := adoptNode("AWS::S3::Bucket", "freed", map[string]string{"tofu-estate": "other-team"})
	rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_s3_bucket.freed", "")))
	if len(rat.Refused) != 0 {
		t.Fatalf("refused on the index's tags: %+v", rat.Refused)
	}
	if len(rat.MarkerWrites()) != 1 {
		t.Fatalf("marker writes = %+v, want one", rat.MarkerWrites())
	}
}

// An object the index listed and the live system no longer has is refused.
func TestAdopt_refusesAnObjectThatIsGone(t *testing.T) {
	cloud := &adoptCloud{objects: map[string]cty.Value{}}
	node := adoptNode("AWS::S3::Bucket", "gone", nil)
	rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "aws_s3_bucket.gone", "")))
	r, ok := refusalFor(rat, "aws_s3_bucket.gone")
	if !ok || !strings.Contains(r.Reason, "no longer exists") {
		t.Fatalf("refusal = %+v, %v", r, ok)
	}
}

// An address whose type is not the settled type, and a data address, are
// refused before any provider call.
func TestAdopt_refusesAMismatchedAddress(t *testing.T) {
	cloud := &adoptCloud{objects: map[string]cty.Value{}}
	node := adoptNode("AWS::S3::Bucket", "web-logs", nil)
	other := adoptNode("AWS::S3::Bucket", "web-data", nil)
	rat := ratifyAdoption(t, cloud, adoptionOf(
		entry(node, "aws_lb.web_logs", ""),
		entry(other, "data.aws_s3_bucket.web_data", ""),
	))
	if len(rat.Refused) != 2 {
		t.Fatalf("refused %+v, want both", rat.Refused)
	}
	if len(cloud.imports) != 0 {
		t.Errorf("provider was called: %v", cloud.imports)
	}
}

// Non-aws substrates have no reverse type join, so the selection names the
// type; the import id is the native id as listed.
func TestAdopt_nonAWSNeedsTheType(t *testing.T) {
	node := &adoption.Node{
		ID:         "azure/sub/eastus/microsoft.storage%2Fstorageaccounts//subscriptions/sub/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/acct",
		ListedType: "microsoft.storage/storageaccounts",
		NativeID:   "/subscriptions/sub/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/acct",
	}
	cloud := &adoptCloud{objects: map[string]cty.Value{}}
	rat := ratifyAdoption(t, cloud, adoptionOf(entry(node, "azurerm_storage_account.acct", "")))
	r, ok := refusalFor(rat, "azurerm_storage_account.acct")
	if !ok || !strings.Contains(r.Reason, "must name one") {
		t.Fatalf("refusal = %+v, %v", r, ok)
	}

	sub, scope := nodeSubstrate(node.ID)
	if sub != "azure" || scope != "sub" {
		t.Errorf("nodeSubstrate = %q, %q", sub, scope)
	}
	id, why := adoptImportID("azure", scope, "azurerm_storage_account", "", node, nil)
	if why != "" || id != node.NativeID {
		t.Errorf("azure import id = %q (%s), want the ARM id as listed", id, why)
	}
}

func TestSplitKubernetesListedType(t *testing.T) {
	for in, want := range map[string][2]string{
		"apps/v1/Deployment": {"apps/v1", "Deployment"},
		"v1/ConfigMap":       {"v1", "ConfigMap"},
	} {
		if a, k := splitKubernetesListedType(in); a != want[0] || k != want[1] {
			t.Errorf("%s: got %s, %s", in, a, k)
		}
	}
}
