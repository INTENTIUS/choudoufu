// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/stateless/discovery"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// Request is one classification pass.
type Request struct {
	// Estate is the estate whose ownership boundary this classification is
	// drawn around. A live resource carrying this value in its tofu-estate
	// tag is owned and never reaches this package.
	Estate string

	// Config is the configuration the discovery pass ran against, which is
	// where the declared instances and their identity-bearing arguments are
	// read from.
	Config *configs.Config

	// Discovery is the result of one discovery pass. It must have been
	// gathered with [discovery.Request.CollectUnclaimed] set, or there is
	// nothing to classify - and the scan rows will say so, which is what
	// [Result.Unswept] reports.
	Discovery *discovery.Result

	// Region is the region the discovery pass listed in, as the provider
	// configuration knows it - the same value handed to
	// [discovery.Request.Region]. It travels into the adoption hint as a
	// --region flag, so that the printed command talks to the same region
	// the resource was found in rather than whatever the operator's CLI
	// profile defaults to. Empty leaves the flag off the hint.
	Region string

	// EndpointURL is the custom endpoint the provider configuration reaches
	// the cloud through, when one is configured - a LocalStack-style
	// emulator, a VPC endpoint. It travels into the adoption hint as an
	// --endpoint-url flag for the same reason Region does. Empty leaves the
	// flag off the hint.
	EndpointURL string
}

// matchTable is the per-type list of identity-bearing arguments a content
// match is allowed to use, and the whole extent of what this package will
// match on. A type absent from it can never produce a bind candidate.
//
// The entries are the arguments that name a specific resource within its
// scope, not merely arguments that happen to be in configuration: a security
// group's name is unique within a VPC, a VPC's CIDR is the thing that makes
// it that VPC, a subnet is its CIDR within its availability zone. Tags are
// deliberately absent - matching on a Name tag is how a tool ends up
// adopting the wrong resource.
//
// The marker-path types missing from this table are missing on purpose:
// aws_route_table and aws_internet_gateway have nothing in configuration
// that distinguishes one from another (both are "the one attached to this
// VPC", and vpc_id is a live ID rather than a configuration value),
// aws_eip instances are explicitly fungible, and aws_kms_key is the same
// shape as the route table - a key's arguments are its description, its
// usage and its policy, none of which names one key rather than another,
// and adopting a key by its description is exactly the guess this table
// exists to refuse. Adoption for a KMS key is refuse-only: it is reported
// as foreign with the marker pair to stamp, and never offered.
//
// The types #20 and #21 add split the same way. aws_lb and
// aws_lb_target_group join the table on their name argument: ELBv2 requires
// a load balancer name to be unique within an account and region, and a
// target group name likewise, which is the same guarantee a security
// group's name carries within its VPC. aws_sqs_queue and aws_sns_topic join
// on name for a stronger reason still - a queue name and a topic name are
// each unique within an account and region by construction, since the URL
// and the ARN are built out of them, so a name match there is not a
// heuristic at all.
//
// aws_lb_listener and aws_lb_target_group_attachment are refuse-only, and
// for the route table's reason rather than the KMS key's. A listener's
// arguments are its port, its protocol and its default action, which
// describe one listener of a load balancer whose identity is a live ARN
// this package has no configuration value for; two load balancers with an
// HTTP listener on 80 are indistinguishable here. An attachment is the same
// shape one level down, and is derived rather than discovered anyway: its
// identity comes out of its parent's live ARN, so there is never an
// unclaimed one waiting to be matched.
var matchTable = map[string][]string{
	"aws_security_group": {"name"},
	"aws_vpc":            {"cidr_block"},
	"aws_subnet":         {"cidr_block", "availability_zone"},
	// A hosted zone's domain name is what makes it that zone, the same way
	// a security group's name is. Route 53 does permit two zones with the
	// same name, which is why the identity table says the name is not the
	// zone's identity - but two live zones matching one declared block is
	// the ambiguity the one-to-one rule below already refuses rather than
	// resolves, so matching on it is safe. The provider stores the name
	// with the trailing dot trimmed, so a configuration written
	// "example.com." reads as a near miss and says so.
	"aws_route53_zone": {"name"},
	// ELBv2 names, unique per account and region by the API's own rule.
	"aws_lb":              {"name"},
	"aws_lb_target_group": {"name"},
	// Queue and topic names, unique per account and region because the
	// identity is built out of them.
	"aws_sqs_queue": {"name"},
	"aws_sns_topic": {"name"},
}

// ec2Types are the types whose adoption command this package can write down.
// Everything in the v0 subset that carries EC2-style tags is adopted with
// one create-tags call; a type outside this set still has a marker pair,
// which is the actual contract, but no one-liner. The two marker types
// added in #20 are outside it: a KMS key is tagged with kms:TagResource and
// a hosted zone with route53:ChangeTagsForResource, each with its own flag
// spelling, and neither is an ec2 create-tags call.
var ec2Types = map[string]bool{
	"aws_vpc":              true,
	"aws_subnet":           true,
	"aws_security_group":   true,
	"aws_route_table":      true,
	"aws_internet_gateway": true,
	"aws_eip":              true,
}

// Classify sorts every unclaimed live resource discovery found into foreign,
// bind candidate, or other estate, and records which resource types the
// classification can speak for at all.
//
// It reads nothing, writes nothing, and calls no provider: the whole input
// is the discovery result and the configuration. Error diagnostics mean the
// inputs were unusable, not that anything about the live system is wrong -
// there is no live-system condition this package treats as an error, because
// "somebody else's resource exists" is not one.
func Classify(ctx context.Context, req Request) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	res := &Result{Estate: req.Estate}

	switch {
	case req.Discovery == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No discovery result to classify",
			"Foreign classification runs over the output of a discovery pass, and none was given.",
		))
	case req.Config == nil || req.Config.Module == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to classify against",
			"Foreign classification needs the configuration the discovery pass ran against, so that a live resource can be compared with the instances that are actually declared, and none was given.",
		))
	}

	c := &classifier{req: req, res: res}
	c.sweepCoverage()
	c.buildSlots(ctx)
	c.classify()
	c.finish()
	// The rename pass reads the owned side of the discovery result - the
	// orphans and the unbound declared instances - rather than the unclaimed
	// side everything above works on, so it runs on its own and changes
	// neither.
	c.renames(ctx)
	// Removal reporting runs last because it is the one part of this pass
	// that has to know what the rename pairing concluded: nothing discovery
	// withheld for a possible rename is in it.
	c.removals()

	return res, diags
}

// ---------------------------------------------------------------------------
// The classifier
// ---------------------------------------------------------------------------

type classifier struct {
	req Request
	res *Result

	// slots holds the declared, unbound instances a live resource could be
	// offered for, by type.
	slots map[string][]*slot

	// unmatchable records why a type can never produce a bind candidate, so
	// that a foreign line can say so rather than being silent about it.
	unmatchable map[string]string

	// pairs holds every (live resource, slot) exact match found, before
	// uniqueness is decided.
	pairs []pair
}

// slot is one declared instance waiting to be found, with the values of its
// identity-bearing arguments as configuration gives them.
type slot struct {
	addr   addrs.AbsResourceInstance
	values []AttrMatch

	// why is non-empty when this instance cannot take part in matching at
	// all, and says which argument could not be read.
	why string
}

// pair is one exact content match between a live resource and a slot.
type pair struct {
	live *discovery.UnclaimedResource
	slot *slot
	on   []AttrMatch
}

// sweepCoverage works out which types this classification can speak for.
func (c *classifier) sweepCoverage() {
	swept := make(map[string]bool)

	for _, s := range c.req.Discovery.Scans {
		if s.Sweep {
			// A sweep row is a list of a type the configuration does not
			// declare, made to find resources this estate owns and no longer
			// declares. It never looked at unclaimed resources and no
			// declared instance of that type exists to offer one to, so it
			// says nothing either way about foreign resources and does not
			// belong in this coverage report. Its own coverage is reported
			// as [Result.SweepGaps] and [Result.SweepCovered].
			continue
		}
		switch {
		case c.problemFor(s.TypeName, discovery.ProblemTypeNotListable) != nil:
			c.res.Unswept = append(c.res.Unswept, Unswept{
				TypeName: s.TypeName,
				Reason:   UnsweptNotListable,
				Detail: fmt.Sprintf(
					"The provider cannot list %s at all, so no resource of that type was looked at. Nothing below says anything about whether foreign %s resources exist.",
					s.TypeName, s.TypeName),
			})
		case c.problemFor(s.TypeName, discovery.ProblemListFailed) != nil:
			c.res.Unswept = append(c.res.Unswept, Unswept{
				TypeName: s.TypeName,
				Reason:   UnsweptListFailed,
				Detail: fmt.Sprintf(
					"Listing %s failed, so no resource of that type was looked at. A foreign %s could exist and would not appear below.",
					s.TypeName, s.TypeName),
			})
		case s.Scope != discovery.ScopeAll:
			c.res.Unswept = append(c.res.Unswept, Unswept{
				TypeName: s.TypeName,
				Reason:   UnsweptEstateScoped,
				Detail: fmt.Sprintf(
					"%s was listed with the server-side estate filter on, so only this estate's own resources crossed the wire and unclaimed ones were never visible. This says nothing about whether any exist.",
					s.TypeName),
			})
		default:
			swept[s.TypeName] = true
			c.res.Swept = append(c.res.Swept, s.TypeName)
		}
	}

	// Types the configuration declares that discovery never listed, because
	// nothing of that type needed discovering. Their identities come out of
	// configuration, so the run never enumerates them and a foreign one is
	// invisible to this pass.
	for _, rc := range c.req.Config.Module.ManagedResources {
		typeName := rc.Type
		if swept[typeName] || c.hasScan(typeName) {
			continue
		}
		if _, exists := c.res.UnsweptOf(typeName); exists {
			continue
		}
		c.res.Unswept = append(c.res.Unswept, Unswept{
			TypeName: typeName,
			Reason:   UnsweptNotScanned,
			Detail: fmt.Sprintf(
				"No instance of %s needed marker discovery, so nothing of that type was listed on this configuration's behalf. The removal sweep may have listed it, but a sweep asks the provider for this estate's own resources and no others, so no unclaimed %s ever crossed the wire. Foreign resources of this type cannot appear below.",
				typeName, typeName),
		})
	}
}

// buildSlots reads the identity-bearing arguments of every declared instance
// that is unbound and eligible for matching.
func (c *classifier) buildSlots(ctx context.Context) {
	c.slots = make(map[string][]*slot)
	c.unmatchable = make(map[string]string)

	for _, addr := range c.req.Discovery.Unbound {
		typeName := addr.Resource.Resource.Type

		attrs, ok := matchTable[typeName]
		if !ok {
			c.unmatchable[typeName] = fmt.Sprintf(
				"no argument of a declared %s distinguishes one live resource of that type from another, so nothing here could be offered for adoption without guessing",
				typeName)
			continue
		}
		if addr.Resource.Key != addrs.NoKey {
			c.unmatchable[typeName] = fmt.Sprintf(
				"the declared %s instances waiting to be found are count or for_each members, whose membership is decided by slot markers rather than by content",
				typeName)
			continue
		}

		rc, ok := c.req.Config.Module.ManagedResources[addr.Resource.Resource.String()]
		if !ok {
			// Discovery already refuses to run against resolutions and a
			// configuration that disagree, so this is unreachable in
			// practice; treating it as "not matchable" keeps it harmless.
			continue
		}

		s := &slot{addr: addr}
		for _, name := range attrs {
			v, why := staticString(ctx, c.req.Config.Module, rc, name)
			if why != "" {
				s.why = why
				break
			}
			s.values = append(s.values, AttrMatch{Attr: name, Value: v})
		}
		c.slots[typeName] = append(c.slots[typeName], s)
	}
}

// classify walks the unclaimed resources, buckets the other-estate ones, and
// records every exact match for the uniqueness pass in finish.
func (c *classifier) classify() {
	byEstate := make(map[string]*EstateCount)
	typesSeen := make(map[string]map[string]bool)

	for i := range c.req.Discovery.Unclaimed {
		u := &c.req.Discovery.Unclaimed[i]

		// Discovery only ever puts resources with no tofu-estate tag in this
		// list, but classification is the place where "whose is it" is
		// answered, so it answers it from the tags rather than from where
		// the resource happened to arrive from.
		switch estate := u.Tags[discovery.TagEstate]; {
		case estate == c.req.Estate:
			// Ours. Discovery bound it or reported it as malformed; either
			// way it is not unclaimed and not this package's business.
			continue
		case estate != "":
			e := byEstate[estate]
			if e == nil {
				e = &EstateCount{Estate: estate}
				byEstate[estate] = e
				typesSeen[estate] = make(map[string]bool)
			}
			e.Count++
			if !typesSeen[estate][u.TypeName] {
				typesSeen[estate][u.TypeName] = true
				e.Types = append(e.Types, u.TypeName)
			}
			continue
		}

		for _, s := range c.slots[u.TypeName] {
			if s.why != "" || len(s.values) == 0 {
				continue
			}
			if on, ok := matches(u, s); ok {
				c.pairs = append(c.pairs, pair{live: u, slot: s, on: on})
			}
		}
	}

	// The per-type tally discovery keeps for resources it filtered out
	// without recording their estate. Those resources never reach
	// [discovery.Result.Unclaimed], so this count is the only trace of them,
	// and dropping it would under-report how crowded the account is.
	unnamed := &EstateCount{}
	for _, s := range c.req.Discovery.Scans {
		// Sweep rows are excluded for the same reason they are excluded from
		// the coverage report above: this section is about the types the
		// configuration declares, and a count that appeared or vanished
		// depending on which admitted types happen to be filterable would be
		// a number nobody could act on.
		if s.OtherEstate == 0 || s.Sweep {
			continue
		}
		unnamed.Count += s.OtherEstate
		unnamed.Types = append(unnamed.Types, s.TypeName)
	}

	for _, e := range byEstate {
		sort.Strings(e.Types)
		c.res.OtherEstates = append(c.res.OtherEstates, *e)
	}
	if unnamed.Count > 0 {
		sort.Strings(unnamed.Types)
		c.res.OtherEstates = append(c.res.OtherEstates, *unnamed)
	}
	sort.Slice(c.res.OtherEstates, func(i, j int) bool {
		return c.res.OtherEstates[i].Estate < c.res.OtherEstates[j].Estate
	})
}

// finish decides which matches survive the one-to-one rule and fills in the
// foreign list with everything that did not.
func (c *classifier) finish() {
	perLive := make(map[string][]pair)
	perSlot := make(map[string][]pair)
	for _, p := range c.pairs {
		perLive[liveKey(p.live)] = append(perLive[liveKey(p.live)], p)
		perSlot[p.slot.addr.String()] = append(perSlot[p.slot.addr.String()], p)
	}

	adopted := make(map[string]Candidate)

	for i := range c.req.Discovery.Unclaimed {
		u := &c.req.Discovery.Unclaimed[i]
		if estate := u.Tags[discovery.TagEstate]; estate != "" {
			continue
		}

		key := liveKey(u)
		ps := perLive[key]

		switch {
		case len(ps) == 1 && len(perSlot[ps[0].slot.addr.String()]) == 1:
			p := ps[0]
			adopted[p.slot.addr.String()] = Candidate{
				Resource:      c.resource(u, ""),
				Addr:          p.slot.addr,
				Matched:       p.on,
				MarkerEstate:  c.req.Estate,
				MarkerAddress: discovery.EscapeAddress(p.slot.addr.String()),
				Hint:          adoptionHint(c.req, u, p.slot.addr),
			}
		case len(ps) == 1:
			var other []string
			for _, q := range perSlot[ps[0].slot.addr.String()] {
				if liveKey(q.live) == key {
					continue
				}
				other = append(other, liveID(q.live))
			}
			sort.Strings(other)
			c.res.Foreign = append(c.res.Foreign, c.resource(u, fmt.Sprintf(
				"it matches the declared %s, but so does %s, and adopting one of several identical-looking resources would be a guess",
				ps[0].slot.addr, strings.Join(other, " and "))))
		case len(ps) > 1:
			addrsMatched := make([]string, 0, len(ps))
			for _, q := range ps {
				addrsMatched = append(addrsMatched, q.slot.addr.String())
			}
			sort.Strings(addrsMatched)
			c.res.Foreign = append(c.res.Foreign, c.resource(u, fmt.Sprintf(
				"it matches %s equally well, so which one it would be adopted as is not something its content answers",
				strings.Join(addrsMatched, " and "))))
		default:
			c.res.Foreign = append(c.res.Foreign, c.resource(u, c.whyNoMatch(u)))
		}
	}

	for _, cand := range adopted {
		c.res.Candidates = append(c.res.Candidates, cand)
	}

	sort.Slice(c.res.Foreign, func(i, j int) bool {
		if c.res.Foreign[i].TypeName != c.res.Foreign[j].TypeName {
			return c.res.Foreign[i].TypeName < c.res.Foreign[j].TypeName
		}
		return c.res.Foreign[i].LiveID < c.res.Foreign[j].LiveID
	})
	sort.Slice(c.res.Candidates, func(i, j int) bool {
		return c.res.Candidates[i].Addr.String() < c.res.Candidates[j].Addr.String()
	})
	sort.Strings(c.res.Swept)
	sort.Slice(c.res.Unswept, func(i, j int) bool {
		return c.res.Unswept[i].TypeName < c.res.Unswept[j].TypeName
	})
}

// removals reports the live resources this estate owns and no longer
// declares, which the plan proposes destroying.
//
// It decides nothing. Discovery already settled which orphans are removals
// and which are withheld for a possible rename, and settled it before the
// projection was built, because that is the only order in which a rename
// cannot become a destroy. This pass reads that decision and writes the
// section an operator reads it in.
func (c *classifier) removals() {
	declared := c.req.Config.Module.ManagedResources

	for _, o := range c.req.Discovery.Orphans {
		if !o.Removal {
			continue
		}
		_, blockExists := declared[o.Addr.Resource.Resource.String()]
		c.res.Removals = append(c.res.Removals, Removal{
			Resource: Resource{
				TypeName:    o.TypeName,
				LiveID:      o.ImportID,
				DisplayName: o.DisplayName,
				Tags:        sortTags(o.Tags),
				Why:         removalWhy(o.TypeName, o.Normalized, !blockExists),
			},
			Addr:       o.Addr,
			Marker:     o.Marker,
			Normalized: o.Normalized,
			BlockGone:  !blockExists,
			Swept:      o.Swept,
		})
	}
	sort.Slice(c.res.Removals, func(i, j int) bool {
		return c.res.Removals[i].Addr.String() < c.res.Removals[j].Addr.String()
	})

	for _, g := range c.req.Discovery.SweepGaps {
		c.res.SweepGaps = append(c.res.SweepGaps, SweepGap{
			TypeName: g.TypeName,
			Reason:   SweepGapReason(g.Reason),
			Detail:   g.Detail,
		})
	}
	c.res.SweepCovered = append(c.res.SweepCovered, c.req.Discovery.SweepCovered...)
}

func removalWhy(typeName, marker string, blockGone bool) string {
	if blockGone {
		return fmt.Sprintf(
			"it carries this estate's marker for %s, and the configuration declares no %s block by that name any more",
			marker, typeName)
	}
	return fmt.Sprintf(
		"it carries this estate's marker for %s, and the resource block still there no longer expands to that instance",
		marker)
}

// whyNoMatch explains, for one live resource that matched nothing, what the
// obstacle was: the type is not matchable, nothing of that type is waiting
// to be found, an argument could not be read from configuration, or the
// values simply differ.
func (c *classifier) whyNoMatch(u *discovery.UnclaimedResource) string {
	slots := c.slots[u.TypeName]
	if len(slots) == 0 {
		if why, ok := c.unmatchable[u.TypeName]; ok {
			return why
		}
		if _, matchable := matchTable[u.TypeName]; !matchable {
			return fmt.Sprintf(
				"no argument of a declared %s distinguishes one live resource of that type from another, so nothing here could be offered for adoption without guessing",
				u.TypeName)
		}
		return fmt.Sprintf(
			"no declared %s is waiting to be found: every one in this configuration is already bound to a live resource",
			u.TypeName)
	}

	for _, s := range slots {
		if s.why != "" {
			return fmt.Sprintf("the declared %s could not be compared with it: %s", s.addr, s.why)
		}
	}

	// The ordinary case: a real live resource whose content is simply not
	// the declared one's. Naming the values is what makes a near miss
	// legible as a near miss rather than as a mystery.
	var parts []string
	for _, s := range slots {
		var pairs []string
		for _, want := range s.values {
			got, ok := liveString(u.Resource, want.Attr)
			if !ok {
				got = "(not set)"
			}
			pairs = append(pairs, fmt.Sprintf("%s %q != %q", want.Attr, got, want.Value))
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", s.addr, strings.Join(pairs, ", ")))
	}
	return "its content matches no declared instance waiting to be found: " + strings.Join(parts, "; ")
}

func (c *classifier) resource(u *discovery.UnclaimedResource, why string) Resource {
	return Resource{
		TypeName:    u.TypeName,
		LiveID:      u.ImportID,
		DisplayName: u.DisplayName,
		Tags:        sortTags(u.Tags),
		Why:         why,
	}
}

func (c *classifier) problemFor(typeName string, kind discovery.ProblemKind) *discovery.Problem {
	for i, p := range c.req.Discovery.Problems {
		if p.Kind == kind && p.TypeName == typeName {
			return &c.req.Discovery.Problems[i]
		}
	}
	return nil
}

// hasScan reports whether a type was listed on the configuration's behalf.
// A sweep row does not count: the question this answers is "was anything of
// this type enumerated for the foreign report", and a sweep enumerates for
// the removal report.
func (c *classifier) hasScan(typeName string) bool {
	for _, s := range c.req.Discovery.Scans {
		if s.TypeName == typeName && !s.Sweep {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Matching
// ---------------------------------------------------------------------------

// matches reports whether one live resource carries exactly the values one
// declared instance's identity-bearing arguments have. Every argument must
// be present and equal; an attribute the provider did not send is a
// non-match, never a wildcard.
func matches(u *discovery.UnclaimedResource, s *slot) ([]AttrMatch, bool) {
	on := make([]AttrMatch, 0, len(s.values))
	for _, want := range s.values {
		got, ok := liveString(u.Resource, want.Attr)
		if !ok || got != want.Value {
			return nil, false
		}
		on = append(on, want)
	}
	return on, true
}

// liveString reads one string attribute off a listed resource object.
func liveString(obj cty.Value, attr string) (string, bool) {
	if obj == cty.NilVal || obj.IsNull() {
		return "", false
	}
	ty := obj.Type()
	if !ty.IsObjectType() || !ty.HasAttribute(attr) {
		return "", false
	}
	v, marks := obj.GetAttr(attr).Unmark()
	if len(marks) > 0 || v.IsNull() || !v.IsKnown() {
		return "", false
	}
	s, err := convert.Convert(v, cty.String)
	if err != nil {
		return "", false
	}
	return s.AsString(), true
}

// staticString reads one argument of a declared resource as configuration
// gives it, through the module's static evaluator - constants, variables,
// locals and functions, the same subset identity resolution admits. The
// second return is empty on success and a reason on failure; a value this
// package cannot read is never a wildcard, it disqualifies the instance from
// matching entirely.
func staticString(ctx context.Context, mod *configs.Module, rc *configs.Resource, name string) (string, string) {
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() {
		return "", fmt.Sprintf("its %s argument could not be read from configuration", name)
	}
	attr, ok := content.Attributes[name]
	if !ok {
		return "", fmt.Sprintf("it sets no %s argument, and that is the argument a content match would have to be made on", name)
	}

	for _, trav := range attr.Expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform":
			// Statically evaluable.
		default:
			return "", fmt.Sprintf(
				"its %s argument refers to %s, which is not known until the run is under way, so there is no configuration value to compare against",
				name, trav.RootName())
		}
	}

	if mod.StaticEvaluator == nil {
		return "", fmt.Sprintf("its %s argument could not be evaluated: the configuration carries no static evaluator", name)
	}
	val, evalDiags := mod.StaticEvaluator.Evaluate(ctx, attr.Expr, configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("%s.%s", rc.Addr(), name),
		DeclRange: attr.Range,
	})
	if evalDiags.HasErrors() {
		return "", fmt.Sprintf("its %s argument could not be evaluated from configuration alone", name)
	}
	if val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", fmt.Sprintf("its %s argument is not a plain known value", name)
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", fmt.Sprintf("its %s argument is not usable as a string", name)
	}
	if str.AsString() == "" {
		return "", fmt.Sprintf("its %s argument is empty, which matches nothing", name)
	}
	return str.AsString(), ""
}

// adoptionHint is the one-line command that adopts a live resource: stamp
// the two marker tags on it. There is no adopt subcommand to point at - the
// marker spec is deliberately the whole contract, so the hint is the tag
// write itself, which any tool honoring stateless/MARKERS.md can perform.
//
// The hint is built to be pasted verbatim, the same standard renameCommand
// holds itself to: every interpolated value goes through shellQuote (a
// for_each key can carry a space or a bracket), and the region and endpoint
// the provider configuration listed through ride along as --region and
// --endpoint-url, so the command lands where the resource actually is
// rather than wherever the operator's CLI profile points.
func adoptionHint(req Request, u *discovery.UnclaimedResource, addr addrs.AbsResourceInstance) string {
	if !ec2Types[u.TypeName] || u.ImportID == "" {
		return ""
	}
	cmd := fmt.Sprintf(
		"aws ec2 create-tags --resources %s --tags %s %s",
		shellQuote(u.ImportID),
		shellQuote(fmt.Sprintf("Key=%s,Value=%s", discovery.TagEstate, req.Estate)),
		shellQuote(fmt.Sprintf("Key=%s,Value=%s", discovery.TagAddress, discovery.EscapeAddress(addr.String()))),
	)
	if req.Region != "" {
		cmd += " --region " + shellQuote(req.Region)
	}
	if req.EndpointURL != "" {
		cmd += " --endpoint-url " + shellQuote(req.EndpointURL)
	}
	return cmd
}

func liveKey(u *discovery.UnclaimedResource) string {
	return u.TypeName + "/" + liveID(u)
}

func liveID(u *discovery.UnclaimedResource) string {
	if u.ImportID != "" {
		return u.ImportID
	}
	return "(no identity) " + u.DisplayName
}
