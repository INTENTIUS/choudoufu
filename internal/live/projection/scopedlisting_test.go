// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #2232. These drive the cache-hit rule's third arm against a
// provider that speaks the list protocol for aws_route53_record the way
// hashicorp/aws 6.29+ does: list config {zone_id}, identity {zone_id,
// name, type, set_identifier, account_id}, and with include_resource the
// object its read would have produced.

var r53Block = &configschema.Block{
	Attributes: map[string]*configschema.Attribute{
		"id":              {Type: cty.String, Computed: true},
		"zone_id":         {Type: cty.String, Required: true},
		"name":            {Type: cty.String, Required: true},
		"fqdn":            {Type: cty.String, Computed: true},
		"type":            {Type: cty.String, Required: true},
		"ttl":             {Type: cty.Number, Optional: true},
		"records":         {Type: cty.Set(cty.String), Optional: true},
		"set_identifier":  {Type: cty.String, Optional: true},
		"allow_overwrite": {Type: cty.Bool, Optional: true},
		"alias": {Type: cty.List(cty.Object(map[string]cty.Type{
			"name": cty.String, "zone_id": cty.String, "evaluate_target_health": cty.Bool,
		})), Optional: true},
	},
}

var r53Identity = &configschema.Object{
	Attributes: map[string]*configschema.Attribute{
		"zone_id":        {Type: cty.String, Required: true},
		"name":           {Type: cty.String, Required: true},
		"type":           {Type: cty.String, Required: true},
		"set_identifier": {Type: cty.String, Optional: true},
		"account_id":     {Type: cty.String, Optional: true},
	},
	Nesting: configschema.NestingSingle,
}

type r53Record struct {
	zone, name, typ, setID string
	ttl                    int64
	records                []string
	alias                  string
}

func (r r53Record) object() cty.Value {
	recs := make([]cty.Value, len(r.records))
	for i, v := range r.records {
		recs[i] = cty.StringVal(v)
	}
	rs := cty.SetValEmpty(cty.String)
	if len(recs) > 0 {
		rs = cty.SetVal(recs)
	}
	aliasTy := r53Block.Attributes["alias"].Type
	alias := cty.ListValEmpty(aliasTy.ElementType())
	if r.alias != "" {
		alias = cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name": cty.StringVal(r.alias), "zone_id": cty.StringVal("ZALB"), "evaluate_target_health": cty.True,
		})})
	}
	id := r.zone + "_" + r.name + "_" + r.typ
	if r.setID != "" {
		id += "_" + r.setID
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":              cty.StringVal(id),
		"zone_id":         cty.StringVal(r.zone),
		"name":            cty.StringVal(r.name),
		"fqdn":            cty.StringVal(r.name),
		"type":            cty.StringVal(r.typ),
		"ttl":             cty.NumberIntVal(r.ttl),
		"records":         rs,
		"set_identifier":  cty.StringVal(r.setID),
		"allow_overwrite": cty.NullVal(cty.Bool),
		"alias":           alias,
	})
}

func (r r53Record) identity() cty.Value {
	setID := cty.NullVal(cty.String)
	if r.setID != "" {
		setID = cty.StringVal(r.setID)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"zone_id": cty.StringVal(r.zone), "name": cty.StringVal(r.name), "type": cty.StringVal(r.typ),
		"set_identifier": setID, "account_id": cty.StringVal(""),
	})
}

func (r r53Record) key() string {
	return r.zone + "/" + r.name + "/" + r.typ + "/" + r.setID
}

// r53Cloud is a Route 53 account behind a list-capable provider handle.
type r53Cloud struct {
	*tofu.MockProvider

	mu sync.Mutex
	// live is what both the read and the listing see.
	live map[string]r53Record
	// listed, when non-nil, replaces live for the listing only: the
	// "two objects answer to one identity" shape no real zone produces.
	listed    []r53Record
	listError string

	reads    int
	listings []string
}

func newR53Cloud(records ...r53Record) *r53Cloud {
	c := &r53Cloud{live: map[string]r53Record{}}
	for _, r := range records {
		c.live[r.key()] = r
	}
	p := &tofu.MockProvider{GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
		Provider: providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{
			"aws_route53_record": {Block: r53Block, IdentitySchema: r53Identity},
		},
		ListResourceTypes: map[string]providers.Schema{
			"aws_route53_record": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"zone_id": {Type: cty.String, Required: true},
			}}},
		},
	}}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		id := r.Target.Identity
		stub := map[string]cty.Value{}
		for name, at := range r53Block.Attributes {
			stub[name] = cty.NullVal(at.Type)
		}
		for _, n := range []string{"zone_id", "name", "type", "set_identifier"} {
			stub[n] = id.GetAttr(n)
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName, State: cty.ObjectVal(stub), Identity: id,
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.reads++
		k := r53Record{
			zone: scopedAttrString(r.PriorState, "zone_id"), name: scopedAttrString(r.PriorState, "name"),
			typ: scopedAttrString(r.PriorState, "type"), setID: scopedAttrString(r.PriorState, "set_identifier"),
		}.key()
		rec, ok := c.live[k]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r53Block.ImpliedType())}
		}
		return providers.ReadResourceResponse{NewState: rec.object()}
	}
	c.MockProvider = p
	return c
}

func (c *r53Cloud) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	zone := req.Config.GetAttr("zone_id").AsString()
	c.mu.Lock()
	c.listings = append(c.listings, zone)
	recs := c.listed
	if recs == nil {
		for _, r := range c.live {
			recs = append(recs, r)
		}
	}
	listErr := c.listError
	c.mu.Unlock()
	if listErr != "" {
		return diags.Append(fmt.Errorf("%s", listErr))
	}
	for _, r := range recs {
		if r.zone != zone {
			continue
		}
		ev := providers.ListResourceEvent{Identity: r.identity()}
		if req.IncludeResourceObject {
			obj := r.object()
			// The list resource sets name to the FQDN and leaves id the
			// lower-cased form; neither is compared.
			ev.ResourceObject = obj
		}
		if !emit(ev) {
			break
		}
	}
	return diags
}

// r53Cache is a state cache holding each record as the last apply wrote it.
func r53Cache(t *testing.T, recs map[string]r53Record) *states.State {
	t.Helper()
	s := states.NewState()
	for addrStr, r := range recs {
		a := mustAddr(t, addrStr)
		raw, err := ctyjson.Marshal(r.object(), r53Block.ImpliedType())
		if err != nil {
			t.Fatal(err)
		}
		idRaw, err := json.Marshal(map[string]any{"zone_id": r.zone, "name": r.name, "type": r.typ})
		if err != nil {
			t.Fatal(err)
		}
		s.RootModule().SetResourceInstanceCurrent(a.Resource, &states.ResourceInstanceObjectSrc{
			AttrsJSON: raw, IdentityJSON: idRaw, Status: states.ObjectReady,
		}, awsProvider, addrs.NoKey)
	}
	return s
}

var (
	r53WWW      = r53Record{zone: "Z1", name: "www.example.com", typ: "A", ttl: 300, records: []string{"10.0.0.1"}}
	r53Weighted = r53Record{zone: "Z1", name: "api.example.com", typ: "A", setID: "blue", ttl: 60, records: []string{"10.0.0.2"}}
	r53Other    = r53Record{zone: "Z2", name: "mail.example.org", typ: "MX", ttl: 300, records: []string{"10 mx.example.org"}}
)

func r53Resolutions(t *testing.T) []identity.Resolution {
	res := func(addr string, r r53Record) identity.Resolution {
		vals := map[string]string{"zone_id": r.zone, "name": r.name, "type": r.typ}
		id := r.zone + "_" + r.name + "_" + r.typ
		if r.setID != "" {
			vals["set_identifier"] = r.setID
			id += "_" + r.setID
		}
		return identity.Resolution{Addr: mustAddr(t, addr), Class: identity.ClassConcrete, ImportID: id, IdentityValues: vals}
	}
	return []identity.Resolution{
		res(`aws_route53_record.www`, r53WWW),
		res(`aws_route53_record.weighted`, r53Weighted),
		res(`aws_route53_record.other_zone`, r53Other),
	}
}

func r53Cached(t *testing.T) *states.State {
	return r53Cache(t, map[string]r53Record{
		`aws_route53_record.www`:        r53WWW,
		`aws_route53_record.weighted`:   r53Weighted,
		`aws_route53_record.other_zone`: r53Other,
	})
}

func buildR53(t *testing.T, cloud *r53Cloud, cache *states.State, planShaped bool) *Result {
	t.Helper()
	cfg := loadConfig(t, "testdata/route53-zone-listing")
	res, diags := BuildWith(context.Background(), cfg, r53Resolutions(t), SingleProvider(awsProvider, cloud), Options{
		Ownership:           &Ownership{Estate: ownershipEstate, Verified: map[string]bool{}},
		StateCache:          cache,
		CacheServesReads:    true,
		EnvelopeVouchServes: planShaped,
	})
	assertNoErrors(t, diags)
	return res
}

func liveAttr(t *testing.T, res *Result, addr, attr string) cty.Value {
	t.Helper()
	a := mustAddr(t, addr)
	is := res.State.ResourceInstance(a)
	if is == nil || is.Current == nil {
		t.Fatalf("%s is not in the projection", addr)
	}
	obj, err := is.Current.Decode(r53Block.ImpliedType())
	if err != nil {
		t.Fatal(err)
	}
	return obj.Value.GetAttr(attr)
}

// TestZoneListingServesEveryRecordInIt: three records over two zones, all
// unchanged since the cache was written. One listing per zone, no read.
func TestZoneListingServesEveryRecordInIt(t *testing.T) {
	cloud := newR53Cloud(r53WWW, r53Weighted, r53Other)
	res := buildR53(t, cloud, r53Cached(t), true)

	if got := res.CacheHits(); got != 3 {
		t.Errorf("CacheHits = %d, want 3", got)
	}
	if cloud.reads != 0 {
		t.Errorf("%d reads, want 0: every record was vouched by its zone's listing", cloud.reads)
	}
	if len(cloud.listings) != 2 {
		t.Errorf("listings = %v, want one per zone (Z1, Z2)", cloud.listings)
	}
	if got := liveAttr(t, res, `aws_route53_record.weighted`, "set_identifier"); got.AsString() != "blue" {
		t.Errorf("weighted set_identifier = %#v", got)
	}
}

// TestZoneListingNeverServesARecordChangedOutOfBand is the BREAK case: a
// record's value was changed outside this tool since the cache was
// written. The listing shows it, so the record is read and the projection
// carries the live value, never the stale cached one. Its unchanged
// neighbour is still served.
func TestZoneListingNeverServesARecordChangedOutOfBand(t *testing.T) {
	changed := r53WWW
	changed.records = []string{"10.9.9.9"}
	retimed := r53Weighted
	retimed.ttl = 120
	cloud := newR53Cloud(changed, retimed, r53Other)
	res := buildR53(t, cloud, r53Cached(t), true)

	if got := res.CacheHits(); got != 1 {
		t.Errorf("CacheHits = %d, want 1 (only the unchanged Z2 record)", got)
	}
	if cloud.reads != 2 {
		t.Errorf("%d reads, want 2: both drifted records must be read", cloud.reads)
	}
	if got := liveAttr(t, res, `aws_route53_record.www`, "records"); !got.Equals(cty.SetVal([]cty.Value{cty.StringVal("10.9.9.9")})).True() {
		t.Errorf("www records = %#v, want the live 10.9.9.9; the stale cached value was served", got)
	}
	if got := liveAttr(t, res, `aws_route53_record.weighted`, "ttl"); !got.Equals(cty.NumberIntVal(120)).True() {
		t.Errorf("weighted ttl = %#v, want the live 120; the stale cached value was served", got)
	}
}

// TestZoneListingNeverServesADeletedRecord: gone from the zone, so not
// vouched, read, and absent from the projection.
func TestZoneListingNeverServesADeletedRecord(t *testing.T) {
	cloud := newR53Cloud(r53Weighted, r53Other)
	res := buildR53(t, cloud, r53Cached(t), true)

	if got := res.CacheHits(); got != 2 {
		t.Errorf("CacheHits = %d, want 2", got)
	}
	if res.Has(mustAddr(t, `aws_route53_record.www`)) {
		t.Error("a record deleted out of band is in the projection, so the plan would report it unchanged")
	}
}

// TestZoneListingMatchesTheSetIdentifier: a weighted record whose set
// identifier changed is a different record set; the old one is gone.
func TestZoneListingMatchesTheSetIdentifier(t *testing.T) {
	green := r53Weighted
	green.setID = "green"
	cloud := newR53Cloud(r53WWW, green, r53Other)
	res := buildR53(t, cloud, r53Cached(t), true)

	if res.Has(mustAddr(t, `aws_route53_record.weighted`)) {
		t.Error("a listed record with another set identifier vouched for the declared one")
	}
	if got := res.CacheHits(); got != 2 {
		t.Errorf("CacheHits = %d, want 2", got)
	}
}

// TestZoneListingFailsTowardReading covers the legs that must all fall
// back to an ordinary read: the gate off (a default plan or an apply), a
// listing error, an ambiguous listing, and an alias record.
func TestZoneListingFailsTowardReading(t *testing.T) {
	t.Run("not plan-shaped", func(t *testing.T) {
		cloud := newR53Cloud(r53WWW, r53Weighted, r53Other)
		res := buildR53(t, cloud, r53Cached(t), false)
		if res.CacheHits() != 0 || cloud.reads != 3 || len(cloud.listings) != 0 {
			t.Errorf("hits=%d reads=%d listings=%v; want 0, 3, none", res.CacheHits(), cloud.reads, cloud.listings)
		}
	})
	t.Run("listing error", func(t *testing.T) {
		cloud := newR53Cloud(r53WWW, r53Weighted, r53Other)
		cloud.listError = "Throttling: Rate exceeded"
		res := buildR53(t, cloud, r53Cached(t), true)
		if res.CacheHits() != 0 || cloud.reads != 3 {
			t.Errorf("hits=%d reads=%d; want 0, 3", res.CacheHits(), cloud.reads)
		}
		if len(cloud.listings) != 2 {
			t.Errorf("listings = %v, want each zone tried once and not retried per record", cloud.listings)
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		cloud := newR53Cloud(r53WWW, r53Weighted, r53Other)
		cloud.listed = []r53Record{r53WWW, r53WWW, r53Weighted, r53Other}
		res := buildR53(t, cloud, r53Cached(t), true)
		if res.CacheHits() != 2 || cloud.reads != 1 {
			t.Errorf("hits=%d reads=%d; want 2, 1 (www listed twice reads)", res.CacheHits(), cloud.reads)
		}
	})
	t.Run("alias", func(t *testing.T) {
		aliased := r53WWW
		aliased.alias = "lb.example.com"
		aliased.records = nil
		aliased.ttl = 0
		cloud := newR53Cloud(aliased, r53Weighted, r53Other)
		cache := r53Cache(t, map[string]r53Record{
			`aws_route53_record.www`:        aliased,
			`aws_route53_record.weighted`:   r53Weighted,
			`aws_route53_record.other_zone`: r53Other,
		})
		res := buildR53(t, cloud, cache, true)
		if res.CacheHits() != 2 || cloud.reads != 1 {
			t.Errorf("hits=%d reads=%d; want 2, 1 (the alias record reads)", res.CacheHits(), cloud.reads)
		}
	})
}

// TestLooseEqual pins the comparison's one leniency, null against a zero
// value, and that it is not leniency about anything else.
func TestLooseEqual(t *testing.T) {
	for _, tc := range []struct {
		a, b cty.Value
		want bool
	}{
		{cty.NullVal(cty.String), cty.StringVal(""), true},
		{cty.NullVal(cty.Number), cty.Zero, true},
		{cty.NullVal(cty.List(cty.String)), cty.ListValEmpty(cty.String), true},
		{cty.StringVal("a"), cty.StringVal("b"), false},
		{cty.NullVal(cty.String), cty.StringVal("x"), false},
		{cty.NumberIntVal(60), cty.NumberIntVal(120), false},
		{cty.SetVal([]cty.Value{cty.StringVal("1"), cty.StringVal("2")}), cty.SetVal([]cty.Value{cty.StringVal("2"), cty.StringVal("1")}), true},
		{cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{"w": cty.NumberIntVal(10)})}), cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{"w": cty.NumberIntVal(20)})}), false},
		{cty.UnknownVal(cty.String), cty.UnknownVal(cty.String), false},
	} {
		if got := looseEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("looseEqual(%#v, %#v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
