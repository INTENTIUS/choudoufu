// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is issue #51's follow-up to #47: wiring
// [cloudcontrol.Client.GetResources] (the Resource Groups Tagging API's
// estate-wide sweep primitive, tagging.go's TODO) into the sweep behind
// [Request.TaggingSweep].
//
// The piece #47 scoped out and #51 does is the join from one
// TaggedResource's ResourceARN to the (TF type, identifier) pair
// [scanTypeCloudControl]'s per-resource filing already knows what to do
// with: parse the ARN ([cloudcontrol.ParseARN]), join its service and
// resource-type segments to a CFN type ([joinARNToCFNType]), join that to a
// TF type via live/mapping.json ([registry.Roster.TFTypesForCFNType]), and
// hand the resource-id segment to the identity table's Components exactly
// as [resolveCloudControlImportID] already does for a Cloud Control
// ListResources identifier - or, for the types whose identity IS the ARN
// (IdentityAttrs leading with "arn"), hand out the ARN itself, because that
// genuinely is the identifier and composing it through Components would be
// composing an already-final value.
//
// # Why a curated table, not a generic parse
//
// The #47 issue comment that filed this follow-up says why in one sentence:
// "the ARN's service name isn't always the CFN service segment, and the
// resource-id segment's shape varies enough per type that it needs its own
// per-type table rather than a generic parse." acm's ARN service is "acm"
// but its CFN service segment is CertificateManager; states/StepFunctions is
// the same story. A generic normalize-and-match over live/registry.json's
// ~1650 CFN types would also have to guess at cases a hand-curated table
// instead states plainly: an EC2 security group rule's ARN
// (security-group-rule/sgr-...) does not say whether it is an ingress or an
// egress rule, and elasticloadbalancing's "loadbalancer" segment is shared
// by the classic and v2 CFN services, distinguishable only by counting the
// id's own "/"-separated parts - AWS's documented ARN grammar, not a guess.
// [arnJoinTable] states each of these outcomes explicitly rather than
// deriving them, the same way internal/live/identity/table.go states each
// type's Components by hand instead of inferring them from a schema.

// arnJoinEntry is what one (ARN service, ARN resource-type segment) pair
// resolves to.
type arnJoinEntry struct {
	// resolve decides the CFN type(s) this entry's ARNs name: exactly one
	// element is the resolvable case; zero means the id's shape matched
	// none of the grammars this entry knows (elasticloadbalancing's
	// "loadbalancer" is the only entry that can produce this); more than
	// one is named ambiguity - nothing about the ARN says which - and both
	// are reported as unresolved by [joinARNToCFNType], never guessed at.
	resolve func(a cloudcontrol.ARN) []string

	// coverage lists every CFN type this entry could ever produce,
	// statically, whichever candidate resolve actually picks for one ARN.
	// It is what [arnJoinCovers] checks a type's mapped CFN type against,
	// so that a type the join table can never reach is reported as a sweep
	// gap ([SweepGapNoARNJoin]) rather than silently, permanently absent
	// from every tagging-sweep result.
	coverage []string
}

// single is an [arnJoinEntry] whose ARN shape names exactly one CFN type,
// unconditionally - the ordinary case, every entry in [arnJoinTable] but the
// two named in their own doc comments.
func single(cfnType string) arnJoinEntry {
	return arnJoinEntry{
		resolve:  func(cloudcontrol.ARN) []string { return []string{cfnType} },
		coverage: []string{cfnType},
	}
}

// ambiguous is an [arnJoinEntry] whose ARN shape names more than one CFN
// type with nothing in the ARN itself saying which.
func ambiguous(cfnTypes ...string) arnJoinEntry {
	sorted := append([]string(nil), cfnTypes...)
	sort.Strings(sorted)
	return arnJoinEntry{
		resolve:  func(cloudcontrol.ARN) []string { return sorted },
		coverage: sorted,
	}
}

// elbLoadBalancerEntry is elasticloadbalancing's "loadbalancer" segment,
// shared by two CFN services and told apart by the one piece of real,
// documented AWS ARN grammar this join table leans on rather than states
// flatly: a classic ELB's id is just its name (loadbalancer/NAME, no
// further "/"), while an ALB/NLB/GWLB's id is
// loadbalancer/{app,net,gwy}/NAME/HASH - three "/"-separated parts. Neither
// shape is a guess; a shape that matches neither (anything but zero or two
// slashes in the id) resolves to nothing, named as unknown rather than
// forced into one of the two.
//
// AWS::ElasticLoadBalancing::LoadBalancer (the classic case) has no row in
// the committed live/mapping.json, so an ARN that resolves to it still ends
// up reported as unresolved one step later, at the CFN-to-TF join - honestly
// naming "no TF type maps this CFN type" rather than silently joining a
// classic ELB's ARN to aws_lb, which is what a table with only one
// "loadbalancer" entry would do.
func elbLoadBalancerEntry() arnJoinEntry {
	const v2, classic = "AWS::ElasticLoadBalancingV2::LoadBalancer", "AWS::ElasticLoadBalancing::LoadBalancer"
	return arnJoinEntry{
		resolve: func(a cloudcontrol.ARN) []string {
			switch strings.Count(a.ResourceID, "/") {
			case 2:
				return []string{v2}
			case 0:
				return []string{classic}
			default:
				return nil
			}
		},
		coverage: []string{v2, classic},
	}
}

// iamRoleEntry is iam's "role" segment, shared by two CFN types the same
// way elasticloadbalancing's "loadbalancer" segment is shared by two -
// AWS::IAM::Role and AWS::IAM::ServiceLinkedRole, told apart by the same
// kind of real, documented ARN grammar [elbLoadBalancerEntry] leans on: a
// service-linked role's resource id always starts with the literal
// "aws-service-role/" segment IAM itself prepends
// (arn:aws:iam::ACCOUNT:role/aws-service-role/SERVICE/NAME, confirmed
// against a live floci-created role while crossing issue #293's
// service-linked-roles corpus estate), which an ordinary role's id - a bare
// name or an operator-chosen path - never carries by construction: IAM
// reserves that prefix for its own service-linked roles and refuses to let
// a CreateRole call use it.
//
// Getting this wrong is not a cosmetic miscount: before this entry existed,
// every service-linked role's ARN joined to AWS::IAM::Role regardless of
// its own marker, and [fileTaggingCandidate] reported ProblemMalformedMarker
// for every one of them (its tofu-address correctly names
// aws_iam_service_linked_role, "aws_iam_role" is what the join, wrongly,
// went looking for) - an error diagnostic on a resource whose marker was
// never malformed at all.
func iamRoleEntry() arnJoinEntry {
	const role, serviceLinked = "AWS::IAM::Role", "AWS::IAM::ServiceLinkedRole"
	return arnJoinEntry{
		resolve: func(a cloudcontrol.ARN) []string {
			if strings.HasPrefix(a.ResourceID, "aws-service-role/") {
				return []string{serviceLinked}
			}
			return []string{role}
		},
		coverage: []string{role, serviceLinked},
	}
}

// arnJoinTable is the curated ARN-service-and-resource-type -> CFN-type
// join, keyed by ARN service and then by the ARN's resource-type segment
// (the empty string for a bare-id ARN with no type segment at all - an S3
// bucket, an SNS topic). See this file's doc comment for why it is
// hand-curated rather than derived from live/registry.json generically.
//
// Every entry here names a CFN type that is independently taggable and
// carries its own ARN, which the Tagging API's GetResources requires by
// construction - a composite identity (a route, a role-policy attachment, an
// inline IAM policy) never has a standalone ARN to appear in a
// TaggedResource at all, so none of identity/table.go's composite entries
// need a row here.
//
// kms's "alias" segment is deliberately absent: a KMS alias's TF identity is
// its full "alias/NAME" string (identity/table.go's aws_kms_alias entry
// says so explicitly), and [cloudcontrol.ParseARN] has already cut the
// "alias/" prefix into ResourceType by the time a rule here would see it.
// Joining it correctly needs that prefix put back, which no other entry in
// this table needs done for it; left for a follow-up rather than joined
// wrong. The same is true of ssm's "parameter" segment, where the
// parameter's own name conventionally starts with "/" and the ARN's
// "parameter/" divider swallows exactly one of them.
var arnJoinTable = map[string]map[string]arnJoinEntry{
	"iam": {"role": iamRoleEntry()},
	"s3":  {"": single("AWS::S3::Bucket")},
	"sns": {"": single("AWS::SNS::Topic")},
	"ec2": {
		"vpc":              single("AWS::EC2::VPC"),
		"subnet":           single("AWS::EC2::Subnet"),
		"security-group":   single("AWS::EC2::SecurityGroup"),
		"route-table":      single("AWS::EC2::RouteTable"),
		"internet-gateway": single("AWS::EC2::InternetGateway"),
		"elastic-ip":       single("AWS::EC2::EIP"),
		"volume":           single("AWS::EC2::Volume"),
		"launch-template":  single("AWS::EC2::LaunchTemplate"),
		"instance":         single("AWS::EC2::Instance"),
		// natgateway carries no hyphen, unlike most of this table's other
		// two-word ec2 segments (AWS's own ARN grammar, not a typo).
		// aws_nat_gateway joined identity.DefaultTable in the EC2 networking
		// batch (issue #65), so this entry no longer carries the
		// mapped-but-unadmitted test case tagging_test.go's real-artifacts
		// suite needs; carrier-gateway below picked that up instead.
		"natgateway": single("AWS::EC2::NatGateway"),
		// carrier-gateway: aws_ec2_carrier_gateway maps via "name" but is
		// outside every batch's scope so far - Carrier Gateway is one of the
		// EC2 sub-services the EC2 networking batch (issue #65) named as
		// explicitly out of scope, the same "not wired yet" shape
		// aws_instance and aws_nat_gateway held here before their own
		// batches admitted them - so this is the mapped-but-unadmitted case
		// tagging_test.go's real-artifacts suite exercises now.
		"carrier-gateway": single("AWS::EC2::CarrierGateway"),
		// A security group rule's ARN does not say whether it is an ingress
		// or an egress rule - both share this exact shape - so the join
		// cannot pick one. See [ambiguous].
		"security-group-rule": ambiguous("AWS::EC2::SecurityGroupIngress", "AWS::EC2::SecurityGroupEgress"),
	},
	"kms":     {"key": single("AWS::KMS::Key")},
	"route53": {"hostedzone": single("AWS::Route53::HostedZone")},
	// acm's ARN service is "acm"; the CFN service segment is
	// CertificateManager. Exactly the mismatch this file's doc comment
	// names.
	"acm": {"certificate": single("AWS::CertificateManager::Certificate")},
	// states's ARN service is "states"; the CFN service segment is
	// StepFunctions. Same story as acm above.
	"states":   {"stateMachine": single("AWS::StepFunctions::StateMachine")},
	"logs":     {"log-group": single("AWS::Logs::LogGroup")},
	"dynamodb": {"table": single("AWS::DynamoDB::Table")},
	"ecs": {
		"cluster": single("AWS::ECS::Cluster"),
		// A task definition's ARN is task-definition/{family}:{revision}
		// (confirmed against ecs_task_definition.html.markdown's "## Import"
		// section, the same doc issue #298 already read for the identity
		// side of this type). This entry is only the sweep's ARN-to-CFN-type
		// join; composing the import ID itself is a separate concern
		// [importIDFromARN] already handles for this type by reading its
		// ImportSyntax ("TASKDEFINITIONARN") rather than anything in this
		// table.
		"task-definition": single("AWS::ECS::TaskDefinition"),
	},
	"cloudwatch": {"alarm": single("AWS::CloudWatch::Alarm")},
	"lambda":     {"function": single("AWS::Lambda::Function")},
	"elasticloadbalancing": {
		"loadbalancer": elbLoadBalancerEntry(),
		"targetgroup":  single("AWS::ElasticLoadBalancingV2::TargetGroup"),
		"listener":     single("AWS::ElasticLoadBalancingV2::Listener"),
	},
}

// arnJoinCoverage is every CFN type [arnJoinTable] could ever produce,
// built once from the table above.
var arnJoinCoverage = func() map[string]bool {
	out := map[string]bool{}
	for _, byResourceType := range arnJoinTable {
		for _, entry := range byResourceType {
			for _, cfnType := range entry.coverage {
				out[cfnType] = true
			}
		}
	}
	return out
}()

// arnJoinCovers reports whether [arnJoinTable] could ever resolve some ARN
// to cfnType.
func arnJoinCovers(cfnType string) bool { return arnJoinCoverage[cfnType] }

// joinARNToCFNType joins a parsed ARN's service and resource-type segment
// against [arnJoinTable]. cfnType is set only when the join is unique;
// candidates lists what it found instead when it was not (nil for "found
// nothing at all", 2+ for genuine ambiguity) so the caller can name what it
// saw.
func joinARNToCFNType(a cloudcontrol.ARN) (cfnType string, candidates []string) {
	byResourceType, ok := arnJoinTable[a.Service]
	if !ok {
		return "", nil
	}
	entry, ok := byResourceType[a.ResourceType]
	if !ok {
		return "", nil
	}
	got := entry.resolve(a)
	if len(got) == 1 {
		return got[0], nil
	}
	return "", got
}

// providerAliasNoteRe matches the exact substring the AWS provider's own
// documentation states once per canonical type and importdocs-gen quotes
// verbatim into the ratified identity table's Reason field when it clones
// that canonical type's row onto the alias
// (tools/importdocs-gen/alias.go's aliasDeclaredFor, tools/row-gen/
// annotations.json's "the same alias relationship the doc note states"):
//
//	is known as `aws_lb`. The functionality is identical.
//
// This is deliberately the identical anchor aliasDeclaredFor's own knownAsRe
// uses (backticked type name, "The functionality is identical." verbatim),
// not a looser "alias" scan: [identity.TypeIdentity.Reason] is free-form
// ratified prose everywhere else in the table (compare
// aws_kms_external_key's "the same shape as aws_kms_key", which names a
// sibling but makes no such claim), so only the provider's own sentence -
// the one aliasDeclaredFor already required to exist verbatim on the
// canonical type's doc page before cloning anything - counts as this
// package's own alias signal. A Reason that merely mentions another type in
// passing never matches.
var providerAliasNoteRe = regexp.MustCompile("is known as `(aws_[a-z0-9_]+)`\\. The functionality is identical\\.")

// resolveDocumentedAlias looks for a documented-alias relationship inside a
// set of TF types the identity table admits for one CFN type - not a
// generic "these look similar" guess, but the provider's own prose, quoted
// verbatim into the alias's ratified Reason. It returns the canonical type
// name and true only when every non-canonical candidate's Reason carries
// [providerAliasNoteRe] naming another candidate in the same set, and
// exactly one candidate is never itself named that way - the shape a
// two-(or more-)name synonym family always has, canonical row cloned onto
// every alias with nothing pointing anywhere else.
//
// This is what tells a documented alias pair (aws_alb/aws_lb: same object,
// safe to pick either, so this package picks the canonical one to match
// what every other join in this file already returns) apart from two
// admitted TF types that both happen to map to one CFN type but are
// genuinely different resources - aws_kms_external_key and aws_kms_key
// (a customer-managed key vs. a BYOK one), aws_db_instance and
// aws_rds_cluster_instance, and every other multi-candidate CFN type
// live/mapping.json carries. None of those Reason strings contain the
// provider's "is known as" sentence, so resolveDocumentedAlias returns
// false for every one of them and the caller's existing ambiguity refusal
// stands - see this package's tests for the full roster this was checked
// against.
//
// A candidate set with no Reason at all (a composite-identity type never
// carries one - see [identity.TypeIdentity.Reason]'s own doc comment) also
// returns false: nothing here claims those pairs are safe, and none is
// currently reachable through this file's ARN join (no arnJoinTable entry
// resolves to AWS::ElasticLoadBalancingV2::ListenerCertificate, the one
// admitted pair in that shape).
func resolveDocumentedAlias(admitted []string) (canonical string, ok bool) {
	aliasOf := make(map[string]string, len(admitted)) // candidate -> the other candidate its Reason names, if any
	for _, tf := range admitted {
		ti, found := identity.LookupType(tf)
		if !found {
			return "", false
		}
		m := providerAliasNoteRe.FindStringSubmatch(ti.Reason)
		if m == nil {
			continue
		}
		aliasOf[tf] = m[1]
	}

	var canonicals []string
	for _, tf := range admitted {
		if _, isAlias := aliasOf[tf]; !isAlias {
			canonicals = append(canonicals, tf)
		}
	}
	if len(canonicals) != 1 {
		return "", false
	}
	canonical = canonicals[0]

	for _, target := range aliasOf {
		if target != canonical {
			// Points somewhere other than this set's one non-aliased
			// candidate - not the clean synonym-family shape, so this is
			// not trusted as safe.
			return "", false
		}
	}
	return canonical, true
}

// resourceSegmentLabel renders an ARN's resource-type segment for a
// message, naming the bare-id shape explicitly rather than leaving it
// looking like an accidental empty string.
func resourceSegmentLabel(a cloudcontrol.ARN) string {
	if a.ResourceType == "" {
		return "(none - a bare identifier)"
	}
	return a.ResourceType
}

// arnJoinOutcome is what [joinTaggedResource] resolved one ResourceARN to,
// or the reason it could not - named after the ARN's own service and
// resource segments throughout, in the same voice as this package's other
// refusals (see the package doc's "What this package refuses to guess").
type arnJoinOutcome struct {
	typeName     string
	cfnType      string
	importID     string
	identityAttr string
	ok           bool
	reason       string

	// noTableRow distinguishes "this ARN joined to a type the admission
	// table does not carry" from every other reason the join failed. See
	// [ProblemUnsweepableOwnedType].
	noTableRow bool
}

// joinTaggedResource is issue #51's join proper: one Tagging API ARN,
// turned into the (TF type, identifier) pair the marker-bind step needs, or
// an honest reason it could not be. See this file's doc comment for the
// four steps and why the third (the CFN-type join) is a curated table
// rather than a generic parse.
func joinTaggedResource(roster *registry.Roster, arnStr string) arnJoinOutcome {
	a, ok := cloudcontrol.ParseARN(arnStr)
	if !ok {
		return arnJoinOutcome{reason: fmt.Sprintf(
			"%q does not have the arn:partition:service:region:account:resource shape",
			arnStr)}
	}

	cfnType, candidates := joinARNToCFNType(a)
	if cfnType == "" {
		if len(candidates) > 1 {
			return arnJoinOutcome{reason: fmt.Sprintf(
				"ARN service %q and resource segment %q name more than one CFN type (%s), and nothing in the ARN says which",
				a.Service, resourceSegmentLabel(a), strings.Join(candidates, ", "))}
		}
		return arnJoinOutcome{reason: fmt.Sprintf(
			"no CFN type is known for ARN service %q and resource segment %q",
			a.Service, resourceSegmentLabel(a))}
	}

	tfTypes := roster.TFTypesForCFNType(cfnType)
	if len(tfTypes) == 0 {
		return arnJoinOutcome{cfnType: cfnType, reason: fmt.Sprintf(
			"CFN type %s (from ARN service %q, resource segment %q) has no live/mapping.json row naming a TF resource type",
			cfnType, a.Service, resourceSegmentLabel(a))}
	}
	if len(tfTypes) > 1 {
		// TF-side synonym pairs (aws_lb/aws_alb, the sweeps' many-to-one
		// aliases) map several TF names onto one CFN type. The join only
		// ever binds a type the identity table admits, so when exactly one
		// of the candidates is admitted the ARN is not actually ambiguous -
		// the others could never have been the answer.
		var admitted []string
		for _, tf := range tfTypes {
			if _, ok := identity.LookupType(tf); ok {
				admitted = append(admitted, tf)
			}
		}
		if len(admitted) != 1 {
			if canonical, ok := resolveDocumentedAlias(admitted); ok {
				// A genuine synonym pair, not a genuine ambiguity: the
				// provider's own docs state (and this table's ratified
				// Reason quotes verbatim) that the non-canonical name IS
				// the canonical one under a second registered name, so
				// binding either produces the same live object. See
				// [resolveDocumentedAlias]'s own doc comment for why this
				// is safe where aws_kms_external_key/aws_kms_key (also
				// two admitted TF types over one CFN type, never aliases)
				// is not.
				admitted = []string{canonical}
			} else {
				return arnJoinOutcome{cfnType: cfnType, reason: fmt.Sprintf(
					"CFN type %s (from ARN service %q, resource segment %q) is mapped from more than one TF type in live/mapping.json (%s), and the ARN alone does not say which",
					cfnType, a.Service, resourceSegmentLabel(a), strings.Join(tfTypes, ", "))}
			}
		}
		tfTypes = admitted
	}
	tfType := tfTypes[0]

	ti, ok := identity.LookupType(tfType)
	if !ok {
		// The join succeeded and the type is simply outside the generated
		// admission table. That is a different thing from an ARN nobody can
		// place, and GitHub issue #107's population: such a type can still
		// be admitted for planning, by the provider's identity schema or by
		// the configuration's own arguments, so a live resource of it can
		// be carrying this estate's marker with no table row to remove it
		// by. The caller reports it as its own problem rather than as an
		// unplaceable ARN.
		return arnJoinOutcome{cfnType: cfnType, typeName: tfType, noTableRow: true, reason: fmt.Sprintf(
			"%s (CFN type %s) has no entry in internal/live/identity's table, so its ARN's resource id cannot be composed into an import ID",
			tfType, cfnType)}
	}

	// The identity IS the ARN, or the resource-id segment composes through
	// the same path a Cloud Control ListResources identifier would -
	// [importIDFromARN] carries the reasoning for both, factored out so
	// [scanTypeMarkerFallback] (issue #293) can reach it without going
	// through the ARN-to-CFN-type join above, which it does not need: it
	// already knows tfType from the tag itself.
	importID, identityAttr, composed := importIDFromARN(ti, arnStr)
	if !composed {
		return arnJoinOutcome{cfnType: cfnType, typeName: tfType, reason: fmt.Sprintf(
			"%s's identity table entry could not compose an import ID from the ARN's resource id %q",
			tfType, a.ResourceID)}
	}
	return arnJoinOutcome{cfnType: cfnType, typeName: tfType, importID: importID, identityAttr: identityAttr, ok: true}
}

// ---------------------------------------------------------------------------
// Wiring into the sweep
// ---------------------------------------------------------------------------

// taggedCandidate is one joined Tagging API result, grouped by TF type and
// ready for [fileTaggingCandidate].
type taggedCandidate struct {
	importID     string
	identityAttr string
	tags         map[string]string
}

// sweepViaTagging is the sweep's Tagging API path (issue #51): one
// GetResources call, filtered to this estate's tofu-estate tag, replaces
// the per-type ListResources loop [sweepTypes] would otherwise drive. Each
// returned ARN is joined to a (TF type, identifier) pair by
// [joinTaggedResource] before it reaches the same per-resource filing rules
// [scanTypeCloudControl] applies once a type's candidates are in hand:
// malformed-marker checks, decl matching, orphan filing. Only the candidate
// source changes; what a candidate means once found does not.
//
// Unlike the Cloud Control per-type path, tags always arrive with the
// candidate - GetResources returns them inline - so there is no
// GetResource-style refinement step here at all; [TypeScan.Refined] stays
// zero for every scan this produces.
func sweepViaTagging(ctx context.Context, req Request, decl *declared, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	universe := sweepTypes(req, decl)
	if len(universe) == 0 {
		return diags
	}
	inUniverse := make(map[string]bool, len(universe))
	for _, t := range universe {
		inUniverse[t] = true
	}

	// Issue #266 moved this call out of here and into [markerIndex], which
	// [Discover] installs before the config-driven scan runs so that scan
	// can join tags off the same answer. It is still one call: whoever asks
	// first pays for it, and the sweep asks second whenever the scan needed
	// it. Nothing else about this path changed.
	tagged, err := req.markers.resources(ctx)
	if err != nil {
		for _, typeName := range universe {
			diags = diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapListFailed,
				Detail: fmt.Sprintf(
					"The estate-wide tag sweep's GetResources call failed, so the sweep could not look for %s resources this estate owns but no longer declares: %s.",
					typeName, err),
			}))
		}
		return diags
	}

	byType := make(map[string][]taggedCandidate)
	for _, tr := range tagged {
		out := joinTaggedResource(req.Roster, tr.ResourceARN)
		if out.noTableRow && decl.types[out.typeName] == nil {
			// GitHub issue #107. The type is outside the generated
			// admission table, so the sweep's universe - which is that
			// table's keys - never lists it, and a block deleted from the
			// configuration leaves the live resource here with no run that
			// will ever propose removing it. It can still have been
			// admitted for planning, which is what makes this reachable
			// rather than theoretical.
			//
			// This pass cannot destroy it: there is no table row to build
			// an import identity from. What it can do is stop describing
			// the situation as an unplaceable ARN, which is what the
			// message said before and is not what happened.
			diags = diags.Append(problemDiag(res, Problem{
				Kind:    ProblemUnsweepableOwnedType,
				Marker:  out.typeName,
				LiveIDs: liveIDs(tr.ResourceARN),
				Detail: fmt.Sprintf(
					"A %s resource carries estate %q's ownership marker and this configuration no longer declares it, but %s is outside the generated admission table. The estate-wide sweep draws its universe from that table, so this resource is not planned for destruction and no later run will propose one either: remove it by hand, or declare it again. See live/LIMITATIONS.md, \"Owned resource of a type the sweep cannot cover\".",
					out.typeName, req.Estate, out.typeName),
			}))
			continue
		}
		if !out.ok {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:    ProblemUnresolvedTaggedARN,
				Marker:  tr.ResourceARN,
				LiveIDs: liveIDs(tr.ResourceARN),
				Detail: fmt.Sprintf(
					"The estate-wide tag sweep found a resource carrying estate %q whose ARN could not be joined to a resource type: %s. ARN: %s.",
					req.Estate, out.reason, tr.ResourceARN),
			}))
			continue
		}
		if !inUniverse[out.typeName] {
			// A type the config-driven scan already covers. The
			// admitted-but-untabled case is handled above, before the join
			// outcome is even consulted for an identifier.
			continue
		}
		byType[out.typeName] = append(byType[out.typeName], taggedCandidate{
			importID:     out.importID,
			identityAttr: out.identityAttr,
			tags:         tr.Tags,
		})
	}

	for _, typeName := range universe {
		cfnType, mapped := req.Roster.CloudControlType(typeName)
		switch {
		case !mapped || !arnJoinCovers(cfnType):
			diags = diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapNoARNJoin,
				Detail: fmt.Sprintf(
					"%s has no CFN type the ARN join table (internal/live/discovery/tagging.go) recognizes, so the tag sweep cannot tell its resources apart from an ARN alone.",
					typeName),
			}))
			continue
		case !req.Roster.Taggable(cfnType):
			_, known := req.Roster.TaggableKnown(cfnType)
			diags = diags.Append(sweepGapDiag(res, noRegistryRowOrUntaggable(typeName, cfnType, known)))
			continue
		}

		candidates := byType[typeName]
		scan := TypeScan{
			TypeName:  typeName,
			Sweep:     true,
			Source:    SourceTagging,
			CFNType:   cfnType,
			Filtering: FilterServerSide,
			Scope:     ScopeEstate,
			Listed:    len(candidates),
		}
		res.SweepCovered = append(res.SweepCovered, typeName)

		log.Printf("[DEBUG] stateless/discovery: sweeping %s via the Tagging API (%s), %d resources", typeName, cfnType, len(candidates))

		for _, c := range candidates {
			diags = diags.Append(fileTaggingCandidate(req, decl, typeName, c, res))
		}
		res.Scans = append(res.Scans, scan)
	}

	return diags
}

// fileTaggingCandidate applies the same per-resource marker rules
// [scanTypeCloudControl] applies to one Cloud Control ListResources result,
// to one candidate [sweepViaTagging] already joined and grouped by type.
func fileTaggingCandidate(req Request, decl *declared, typeName string, c taggedCandidate, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if c.tags[TagEstate] != req.Estate {
		// GetResources was called with a TagFilter naming this exact
		// estate, so this should be unreachable; a candidate that somehow
		// arrives here anyway is skipped rather than filed under the wrong
		// estate.
		return diags
	}

	raw, corrupt := GatherAddress(c.tags)
	if corrupt {
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemMalformedMarker,
			TypeName: typeName,
			LiveIDs:  liveIDs(c.importID),
			Detail: fmt.Sprintf(
				"A live %s (via the tag sweep) claims estate %q but its tofu-address continuation tags have a gap in them - one of tofu-address-2, tofu-address-3, ... is missing while a later one is present. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
				typeName, req.Estate),
		}))
	}
	escaped := EscapeAddress(raw)
	if !ValidMarkerAddress(escaped) {
		what := "carries no tofu-address tag"
		if raw != "" {
			what = fmt.Sprintf("carries the tofu-address value %q, which is not a well-formed escaped address", raw)
		}
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemMalformedMarker,
			TypeName: typeName,
			Marker:   raw,
			LiveIDs:  liveIDs(c.importID),
			Detail: fmt.Sprintf(
				"A live %s (via the tag sweep) claims estate %q but %s. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
				typeName, req.Estate, what),
		}))
	}

	if markerType := markerTypeOf(escaped); markerType != typeName {
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemMalformedMarker,
			TypeName: typeName,
			Marker:   raw,
			LiveIDs:  liveIDs(c.importID),
			Detail: fmt.Sprintf(
				"A live %s (via the tag sweep) claims estate %q and carries the tofu-address value %q, which names a %s rather than a %s. A marker names the resource it is written on (see live/MARKERS.md). Retag the resource with its own address, or remove the marker to disown it.",
				typeName, req.Estate, raw, markerType, typeName),
		}))
	}

	claim := claimant{
		importID:     c.importID,
		identityAttr: c.identityAttr,
		identity:     cty.NilVal,
		marker:       raw,
		escaped:      escaped,
		normalized:   escaped != raw,
		slot:         c.tags[TagSlot],
		tags:         c.tags,
		noIdentity:   c.importID == "",
	}

	if entry, ok := decl.entryFor(typeName, escaped); ok {
		entry.claimants = append(entry.claimants, claim)
		return diags
	}
	if decl.declares(typeName, escaped) {
		// GitHub issue #244, half 2 - the same check discovery.go's own scan
		// loop makes at the same point, for the same reason. See
		// displaced.go.
		if want, displaced := decl.displacedFrom(typeName, escaped, claim); displaced {
			return diags.Append(problemDiag(res, displacedProblem(req, typeName, escaped, want, claim)))
		}
		return diags
	}
	if cb := decl.countBlockFor(typeName, escaped); cb != nil {
		cb.extra = append(cb.extra, claim)
		return diags
	}
	if blk, ok := decl.blocks[typeName][escaped]; ok && blk.keyed {
		blk.claimants = append(blk.claimants, claim)
		return diags
	}
	res.Orphans = append(res.Orphans, OwnedResource{
		TypeName:     typeName,
		ImportID:     c.importID,
		IdentityAttr: c.identityAttr,
		Marker:       raw,
		Normalized:   escaped,
		Slot:         c.tags[TagSlot],
		Tags:         c.tags,
		Swept:        true,
	})
	return diags
}
