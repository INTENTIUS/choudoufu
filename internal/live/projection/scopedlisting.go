// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #2232: the state cache's third eligibility arm, for a
// declaration-carried type whose provider lists it one parent scope at a
// time. aws_route53_record is the one such type today.
//
// The record has no tags, so the estate sweep never sees a marker on it,
// and the provider imports it by identity object (zone, name, type, set
// identifier), so [builder.envelopeVouched]'s import-ID sighting never
// matches. Before this arm every declared record was read live on every
// plan, two Route 53 requests each, against an account limit of five
// requests per second.
//
// The arm makes one listing per (provider configuration, scope) pair - per
// hosted zone, through the record's own provider configuration - with the
// provider's own list resource and include_resource set, and serves a
// cached record only when:
//
//   - the listing holds exactly one object whose identity is exactly the
//     declared one (zone, name, type, and set identifier where one applies);
//   - the cached object names that same identity; and
//   - every attribute the listed object carries, other than the ones the
//     provider's read never sets from the live object, equals the cached
//     value.
//
// The third leg is what keeps the cache from changing the answer (claim 3).
// The AWS provider's list resource flattens each record set through
// resourceRecordFlatten, the same function its read uses, so a listed
// object carries exactly the values a read would have set. Equal values
// mean the read would have returned the cached object; any difference is
// drift, and the record is read and shown like any other drift.
//
// Ownership needs nothing extra. The record's identity is its declaration,
// and the read this replaces imports that same declared identity: there is
// no marker to check on the read path either. [builder.checkOwnership] runs
// on the served object exactly as it would on a read one.
//
// Every leg fails toward reading: a gate off, a type with no spec, an
// identity that is not wholly known, a provider handle that cannot list, a
// provider version without the list resource, a listing error, a zone with
// no such record, two listed objects answering to one identity, an alias
// record, or one differing value.

// scopedListSpec says how one type is listed per scope and compared.
type scopedListSpec struct {
	// scopeAttr is both the list resource's required configuration
	// argument and the identity attribute that supplies its value.
	scopeAttr string

	// keyAttrs are the identity attributes that must match exactly. A
	// null and an empty string are the same answer: the provider's own
	// identity leaves set_identifier null on a record that has none.
	keyAttrs []string

	// ignore names the attributes of the listed object that the
	// provider's read does not set from the live object, so a listed value
	// says nothing about what a read would return. name is here because
	// the list resource sets it to the record's FQDN where the read keeps
	// the declared spelling; keyAttrs already compared it.
	ignore map[string]bool

	// declines names a record shape this arm does not vouch for, with the
	// reason, or "" to proceed. Checked on both the cached and the listed
	// object.
	declines func(obj cty.Value) string
}

// scopedListSpecs is keyed by resource type, and the list resource type is
// the same name.
var scopedListSpecs = map[string]scopedListSpec{
	"aws_route53_record": {
		scopeAttr: "zone_id",
		keyAttrs:  []string{"zone_id", "name", "type", "set_identifier"},
		ignore: map[string]bool{
			"id":              true,
			"name":            true,
			"allow_overwrite": true,
			"timeouts":        true,
		},
		declines: func(obj cty.Value) string {
			if nonEmptyAttr(obj, "alias") {
				return "an alias record, whose target is another resource's DNS name the zone listing does not vouch for"
			}
			return ""
		},
	},
}

// scopedListings memoizes one listing per (provider configuration, type,
// scope) for one build. It is shared by [builder.cacheWouldServe] on the
// prefetch launcher and [builder.cacheHit] on the consumer, which run on
// different goroutines and must reach the same answer, so each key is
// fetched exactly once.
type scopedListings struct {
	mu      sync.Mutex
	entries map[string]*scopedListing
}

type scopedListing struct {
	once sync.Once

	// failed is set when the listing could not be trusted as complete: no
	// list capability, no list resource for the type, a call error or an
	// error on any one result. Nothing it holds vouches for anything.
	failed bool

	// byKey maps an identity key to every listed object answering to it.
	// More than one is ambiguity, and ambiguity vouches nothing.
	byKey map[string][]cty.Value

	// listed counts the results, for the log line.
	listed int
}

func (s *scopedListings) get(ctx context.Context, providerAddr addrs.AbsProviderConfig, provider any, typeName string, spec scopedListSpec, scope string) *scopedListing {
	key := providerAddr.String() + "\x00" + typeName + "\x00" + scope
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]*scopedListing{}
	}
	l, ok := s.entries[key]
	if !ok {
		l = &scopedListing{}
		s.entries[key] = l
	}
	s.mu.Unlock()

	l.once.Do(func() {
		l.fetch(ctx, providerAddr, provider, typeName, spec, scope)
	})
	return l
}

func (l *scopedListing) fetch(ctx context.Context, providerAddr addrs.AbsProviderConfig, provider any, typeName string, spec scopedListSpec, scope string) {
	l.failed = true
	schemas, diags := listclient.ListSchemas(ctx, provider)
	if diags.HasErrors() {
		log.Printf("[DEBUG] projection: cannot list %s in %s %q through %s (%s); its cached instances read instead", typeName, spec.scopeAttr, scope, providerAddr, diags.Err())
		return
	}
	ts, ok := schemas.Get(typeName)
	if !ok {
		log.Printf("[DEBUG] projection: %s offers no list resource for %s; its cached instances read instead", providerAddr, typeName)
		return
	}
	config, diags := ts.BuildConfig(map[string]cty.Value{spec.scopeAttr: cty.StringVal(scope)})
	if diags.HasErrors() {
		log.Printf("[DEBUG] projection: cannot build a %s list configuration for %s %q (%s); its cached instances read instead", typeName, spec.scopeAttr, scope, diags.Err())
		return
	}
	byKey := map[string][]cty.Value{}
	listed := 0
	resultFailed := false
	diags = listclient.Stream(ctx, provider, listclient.Request{
		TypeName:        typeName,
		Config:          config,
		IncludeResource: true,
	}, func(r listclient.Result) bool {
		if r.Diagnostics.HasErrors() {
			resultFailed = true
			return false
		}
		listed++
		if r.Resource == cty.NilVal || r.Resource.IsNull() || !r.Resource.IsWhollyKnown() {
			// Listed, but with nothing to compare a cached object
			// against. It cannot vouch, and it still counts toward
			// ambiguity for its key.
			if k, ok := scopedKeyOf(r.Identity, spec); ok {
				byKey[k] = append(byKey[k], cty.NilVal)
			}
			return true
		}
		if k, ok := scopedKeyOf(r.Identity, spec); ok {
			byKey[k] = append(byKey[k], r.Resource)
		}
		return true
	})
	if diags.HasErrors() || resultFailed {
		log.Printf("[DEBUG] projection: listing %s in %s %q through %s failed (%v); its cached instances read instead", typeName, spec.scopeAttr, scope, providerAddr, diags.Err())
		return
	}
	l.failed = false
	l.byKey = byKey
	l.listed = listed
	log.Printf("[DEBUG] projection: listed %d %s in %s %q through %s, one listing for every cached instance there", listed, typeName, spec.scopeAttr, scope, providerAddr)
}

// scopedListingVouched is the third arm of the cache-hit rule (GitHub issue
// #2232): see this file's header. cached is the cache candidate
// [builder.cacheCandidate] decoded.
func (b *builder) scopedListingVouched(ctx context.Context, w wanted, prep readPrep, cached *states.ResourceInstanceObject) bool {
	if !b.opts.EnvelopeVouchServes || w.undeclared || cached == nil || prep.entry == nil {
		return false
	}
	typeName := w.addr.Resource.Resource.Type
	spec, ok := scopedListSpecs[typeName]
	if !ok {
		return false
	}
	declared, ok := declaredIdentityValues(w, prep, spec)
	if !ok {
		return false
	}
	scope := declared[spec.scopeAttr]
	if scope == "" {
		return false
	}
	cachedVal, _ := cached.Value.UnmarkDeep()
	if !cachedVal.Type().IsObjectType() || !cachedVal.IsWhollyKnown() {
		return false
	}
	for _, attr := range spec.keyAttrs {
		if scopedAttrString(cachedVal, attr) != declared[attr] {
			log.Printf("[DEBUG] projection: the cached %s names %s %q, the declaration %q; reading", w.addr, attr, scopedAttrString(cachedVal, attr), declared[attr])
			return false
		}
	}
	if why := spec.declines(cachedVal); why != "" {
		log.Printf("[DEBUG] projection: %s is %s; reading", w.addr, why)
		return false
	}

	listing := b.scopedListings.get(ctx, prep.providerAddr, prep.entry.provider, typeName, spec, scope)
	if listing.failed {
		return false
	}
	matches := listing.byKey[scopedKey(declared, spec)]
	if len(matches) != 1 || matches[0] == cty.NilVal {
		log.Printf("[DEBUG] projection: the %s %q listing holds %d object(s) answering to %s; reading", spec.scopeAttr, scope, len(matches), w.addr)
		return false
	}
	listed, _ := matches[0].UnmarkDeep()
	if why := spec.declines(listed); why != "" {
		log.Printf("[DEBUG] projection: %s is live as %s; reading", w.addr, why)
		return false
	}
	if attr, differs := firstDifference(listed, cachedVal, spec.ignore); differs {
		log.Printf("[DEBUG] projection: %s's %s differs between the zone listing and the cache, so it has drifted; reading", w.addr, attr)
		return false
	}
	return true
}

// declaredIdentityValues is the identity the read would have imported by,
// one string per key attribute, null read as "". It prefers the identity
// object [importTarget] built, and falls back to the configuration's own
// identity values. Anything not wholly known declines.
func declaredIdentityValues(w wanted, prep readPrep, spec scopedListSpec) (map[string]string, bool) {
	out := make(map[string]string, len(spec.keyAttrs))
	id := prep.target.Identity
	if id != cty.NilVal && !id.IsNull() && id.IsWhollyKnown() && id.Type().IsObjectType() {
		for _, attr := range spec.keyAttrs {
			if !id.Type().HasAttribute(attr) {
				return nil, false
			}
			out[attr] = scopedAttrString(id, attr)
		}
	} else if len(w.values) > 0 {
		for _, attr := range spec.keyAttrs {
			out[attr] = w.values[attr]
		}
	} else {
		return nil, false
	}
	// zone, name and type are always present; only set_identifier may be
	// empty.
	for _, attr := range spec.keyAttrs[:len(spec.keyAttrs)-1] {
		if out[attr] == "" {
			return nil, false
		}
	}
	return out, true
}

func scopedKeyOf(identity cty.Value, spec scopedListSpec) (string, bool) {
	if identity == cty.NilVal || identity.IsNull() || !identity.IsWhollyKnown() || !identity.Type().IsObjectType() {
		return "", false
	}
	identity, _ = identity.UnmarkDeep()
	vals := make(map[string]string, len(spec.keyAttrs))
	for _, attr := range spec.keyAttrs {
		if !identity.Type().HasAttribute(attr) {
			return "", false
		}
		vals[attr] = scopedAttrString(identity, attr)
	}
	return scopedKey(vals, spec), true
}

func scopedKey(vals map[string]string, spec scopedListSpec) string {
	parts := make([]string, len(spec.keyAttrs))
	for i, attr := range spec.keyAttrs {
		parts[i] = vals[attr]
	}
	return strings.Join(parts, "\x00")
}

// attrString is obj's string attribute attr, with null, absent and
// non-string all reading as "".
func scopedAttrString(obj cty.Value, attr string) string {
	if obj.IsNull() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(attr) {
		return ""
	}
	v := obj.GetAttr(attr)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

func nonEmptyAttr(obj cty.Value, attr string) bool {
	if obj.IsNull() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(attr) {
		return false
	}
	return !zeroish(obj.GetAttr(attr))
}

// firstDifference compares every attribute of listed, other than ignore,
// against cached, and names the first (in sorted order) that differs.
func firstDifference(listed, cached cty.Value, ignore map[string]bool) (string, bool) {
	names := make([]string, 0, len(listed.Type().AttributeTypes()))
	for name := range listed.Type().AttributeTypes() {
		if !ignore[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var c cty.Value
		if cached.Type().HasAttribute(name) {
			c = cached.GetAttr(name)
		} else {
			c = cty.NullVal(listed.Type().AttributeType(name))
		}
		if !looseEqual(listed.GetAttr(name), c) {
			return name, true
		}
	}
	return "", false
}

// looseEqual is value equality that reads null and the zero value of a
// type as the same answer: an SDKv2 provider cannot tell them apart, and
// its read writes one where its list writes the other. Anything unknown
// is unequal, which reads.
func looseEqual(a, b cty.Value) bool {
	if zeroish(a) && zeroish(b) {
		return true
	}
	if a.IsNull() || b.IsNull() || !a.IsWhollyKnown() || !b.IsWhollyKnown() {
		return false
	}
	at, bt := a.Type(), b.Type()
	switch {
	case at.IsObjectType() && bt.IsObjectType():
		names := map[string]bool{}
		for n := range at.AttributeTypes() {
			names[n] = true
		}
		for n := range bt.AttributeTypes() {
			names[n] = true
		}
		for n := range names {
			av, bv := cty.NullVal(cty.DynamicPseudoType), cty.NullVal(cty.DynamicPseudoType)
			if at.HasAttribute(n) {
				av = a.GetAttr(n)
			}
			if bt.HasAttribute(n) {
				bv = b.GetAttr(n)
			}
			if !looseEqual(av, bv) {
				return false
			}
		}
		return true
	case (at.IsListType() || at.IsTupleType()) && (bt.IsListType() || bt.IsTupleType()):
		if a.LengthInt() != b.LengthInt() {
			return false
		}
		ai, bi := a.ElementIterator(), b.ElementIterator()
		for ai.Next() && bi.Next() {
			_, av := ai.Element()
			_, bv := bi.Element()
			if !looseEqual(av, bv) {
				return false
			}
		}
		return true
	}
	eq := a.Equals(b)
	return eq.IsKnown() && eq.True()
}

// zeroish reports a null, or a known zero value: "", 0, false, or an empty
// collection.
func zeroish(v cty.Value) bool {
	if v == cty.NilVal || v.IsNull() {
		return true
	}
	if !v.IsKnown() {
		return false
	}
	ty := v.Type()
	switch {
	case ty == cty.String:
		return v.AsString() == ""
	case ty == cty.Number:
		return v.Equals(cty.Zero).True()
	case ty == cty.Bool:
		return v.False()
	case ty.IsListType() || ty.IsSetType() || ty.IsMapType() || ty.IsTupleType():
		return v.LengthInt() == 0
	}
	return false
}
