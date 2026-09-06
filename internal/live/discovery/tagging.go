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
	"github.com/intentius/choudoufu/internal/live/listclient"
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
// AWS::ElasticLoadBalancing::LoadBalancer (the classic case) is mapped in the
// committed live/mapping.json only by a former2-provenance row naming aws_elb,
// so until [joinTaggedResource]'s any-provenance fallback existed an ARN that
// resolved to it was reported as unresolved one step later, at the CFN-to-TF
// join. Either way the hazard this entry exists for is unchanged: what it must
// never do is join a classic ELB's ARN to aws_lb, the V2 type, which is what a
// table with only one "loadbalancer" entry would do. It now resolves to aws_elb
// instead, which is the classic load balancer's real Terraform type.
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
	"iam": {
		"role": iamRoleEntry(),
		// A managed policy's ARN resource-type segment is "policy"
		// (arn:aws:iam::ACCOUNT:policy/NAME), unambiguous - IAM has no
		// second CFN type sharing that segment the way "role" shares
		// itself with a service-linked role. live/mapping.json's own row
		// for aws_iam_policy names its CFN type "AWS::IAM::Policy" (via
		// "name"), not the "AWS::IAM::ManagedPolicy" former2 alias also
		// recorded there - the roster's lookup index is built from the
		// "rows" entry, so that is the string this join has to produce.
		// Found renaming module.iam_policy_from_data_source without this
		// entry: the estate-wide sweep read the ARN, could not join it to
		// any CFN type, and so could never propose destroying the live
		// resource left behind at the retired address - the day2_rename
		// stage's own Break control (live/GAUNTLET.md #6) went silent
		// instead of failing loud.
		"policy": single("AWS::IAM::Policy"),
		// An instance profile's ARN resource-type segment is
		// "instance-profile" (arn:aws:iam::ACCOUNT:instance-profile/NAME),
		// unambiguous the same way "policy" above is - IAM has no second
		// CFN type sharing that segment. live/mapping.json's own row for
		// aws_iam_instance_profile names its CFN type
		// "AWS::IAM::InstanceProfile" (via "name"), with no alias to pick
		// between the way aws_iam_policy has.
		//
		// Found building [gauntlet:corpus-ec2-instance-complete/day2_remove]:
		// removing module.ec2_complete's block left its instance profile -
		// a taggable, migrate-stamped type, confirmed carrying its
		// tofu-address marker via both DescribeTags and the Resource
		// Groups Tagging API directly, no tofu in the loop - entirely
		// unswept ([NO_ARN_JOIN]), the exact "type admitted by the
		// provider's identity schema rather than joined here" shape this
		// file's doc comment already names for aws_dynamodb_resource_policy
		// and aws_autoscaling_group. The resource was never invisible to
		// identity resolution or to migrate/apply - only to the
		// estate-wide REMOVAL sweep, which walks ARNs rather than declared
		// blocks.
		"instance-profile": single("AWS::IAM::InstanceProfile"),
	},
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
		// A customer gateway's ARN resource-type segment is "customer-gateway"
		// (arn:aws:ec2:REGION:ACCOUNT:customer-gateway/cgw-...), unambiguous
		// the same way "vpc" and "subnet" above are - EC2 has no second CFN
		// type sharing that segment. live/mapping.json's own row for
		// aws_customer_gateway names "AWS::EC2::CustomerGateway" (via
		// former2), the string this join has to produce.
		//
		// Found building [gauntlet:corpus-vpc-complete/day2_count], and it is
		// the third instance of the shape the iam/policy and
		// cloudfront/distribution entries above already describe: scaling a
		// count block of aws_customer_gateway from 2 down to 1 proposed NO
		// destroy at all, where stock destroys the higher index. The
		// estate-wide tag sweep DID find the orphaned gateway - it carries
		// its tofu-estate and tofu-address markers, confirmed through
		// DescribeCustomerGateways with no tofu in the loop - but could not
		// join its ARN to any CFN type, so it was never classified and its
		// destroy was never proposed. Silent, not loud: the plan read "No
		// changes. Your infrastructure matches the configuration."
		"customer-gateway": single("AWS::EC2::CustomerGateway"),
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
	// A distribution's ARN is arn:aws:cloudfront::ACCOUNT:distribution/ID -
	// unambiguous, the same slash-delimited shape iam's "role"/"policy"
	// entries above already join. Found the same way the iam/policy entry
	// above was: corpus-overture-tiles's day2_remove unit shrinking its
	// aws_cloudfront_distribution block's count to zero. A count-shrunk-to-
	// zero block carries no declared instance ([declared.indexCountBlocks]'s
	// own doc comment), so the type drops out of decl.types entirely and
	// the config-driven per-type scan that would otherwise have found it
	// never runs; the estate-wide tag sweep is then the ONLY remaining route
	// to the live object, and without this entry it could not join the
	// ARN to any CFN type either, so the distribution's own destroy was
	// never proposed - the same silent-instead-of-loud failure the iam/
	// policy entry's own comment describes, reached through a different
	// door (a count shrinking to zero rather than a rename).
	"cloudfront": {"distribution": single("AWS::CloudFront::Distribution")},
	// A CloudWatch composite alarm and a metric alarm share the exact same
	// "alarm" ARN shape (arn:...:cloudwatch:...:alarm:NAME) - CloudWatch
	// treats both as one alarm namespace, and nothing in the ARN says which
	// kind a given name belongs to. Same shape as the security-group-rule
	// case above; see [joinTaggedResource]'s marker-based tiebreak for how a
	// candidate found this way still resolves cleanly when it carries a
	// readable tofu-address marker of its own.
	"cloudwatch": {"alarm": ambiguous("AWS::CloudWatch::Alarm", "AWS::CloudWatch::CompositeAlarm")},
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

// arnJoinReaches reports whether the estate-wide tag sweep can ever tell
// typeName's own resources apart from an ARN alone: it has a CFN type at
// all ([registry.Roster.CloudControlType]) AND that CFN type is one
// [arnJoinTable] actually joins ([arnJoinCovers]) - the SAME two-part test
// [sweepViaTagging]'s own per-type loop already applies before filing a
// candidate, read out here so [partitionSweepTypes] can route a type this
// answers false for to the native per-type sweep BEFORE the tagging leg
// ever runs, rather than after it has already reported the gap and moved on
// with nothing found. A type failing this predicate is never a defect in
// the type; [arnJoinTable] is a curated, per-ARN-resource-type mapping for
// thirteen services today, so most admitted types answer false here, and
// that is expected, not a gap to close type by type.
//
// The unserved-service term ([taggingAPIUnservedType], issue #692) is a
// ROUTING preference, not a fact about the join, and it is conditional on
// the leg it routes to being a route at all. schemas is the provider's own
// list-protocol surface, so [listclient.Schemas.Supports] is the whole
// question "can the native per-type leg enumerate this type" - and for a
// type it answers false for, routing away from the tagging leg leaves the
// sweep with NO enumeration of the type whatsoever. That is the charter's
// rule 2 read backwards, and issue #881 measured what it costs: deleting a
// declared, marked, taggable instance profile's block proposed no destroy
// at all, because the provider serves no list resource for the type (8 of
// IAM's types have one at provider 6.59.0, and that is not one of them) and
// #692's prefix had taken the type out of the one GetResources call the
// sweep makes anyway. Falling back to the tagging leg cannot cost a wrong
// marker: on an account where the API really does not serve the service
// the candidate list is empty and [sweepViaTagging] reports its own gap,
// loudly, where the native leg reported [SweepGapNotListable] before.
func arnJoinReaches(req Request, schemas listclient.Schemas, typeName string) bool {
	cfnType, mapped := arnJoinCFNType(req.Roster, typeName)
	if !mapped || !arnJoinCovers(cfnType) {
		return false
	}
	if taggingAPIUnservedType(typeName) {
		return !schemas.Supports(typeName)
	}
	return true
}

// taggingAPIUnservedServices is the set of ARN service segments the
// Resource Groups Tagging API does not index at all, keyed by resource-type
// name prefix: GetResources never
// returns their resources, no matter how they are tagged. Probed against
// real AWS 2026-09-01 - an IAM role tagged at create never appeared in
// us-east-1 or us-east-2, with a tag filter and with a bare
// resource-type filter (recorded on issue #692) - and floci matches real
// AWS here. Being parseable by [arnJoinTable] is not the same fact as
// being SERVED by GetResources, and conflating the two routed IAM to the
// tagging universe where its sightings simply never happened: the
// terralith's client-named IAM majority went unvouched (6 of 38
// instances), and the state cache - which may only serve what the sweep
// vouches for - was structurally useless for exactly the estates it
// helps most. A type in an unserved service sweeps through the native
// per-type leg instead.
var taggingAPIUnservedServices = map[string]bool{
	"aws_iam_": true,
}

// taggingAPIUnservedType reports whether typeName lives in a service
// GetResources never serves, by the resource type's own name prefix - the
// one spelling every caller has with no roster in hand. One prefix per
// entry in [taggingAPIUnservedServices]; the two lists grow together.
func taggingAPIUnservedType(typeName string) bool {
	for prefix := range taggingAPIUnservedServices {
		if strings.HasPrefix(typeName, prefix) {
			return true
		}
	}
	return false
}

// arnJoinCFNType is the CFN type the tag sweep should reason about for
// typeName: the WIDER of the roster's two joins
// ([registry.Roster.CloudControlTypeOrService]), not the enumerability one
// ([registry.Roster.CloudControlType]).
//
// The two joins differ only in which mapping-row provenances they accept,
// and the narrow one's extra condition is about Cloud Control being able to
// LIST the type on its own. The tagging leg lists nothing: the Resource
// Groups Tagging API returns objects the estate already tagged, ARN and
// markers together, and this lookup only decides whether an ARN of that
// type could be recognised at all ([arnJoinCovers]). The roster's own doc
// comment draws the same line - CloudControlTypeOrService is "for a caller
// that wants identity or relationship facts rather than enumerability".
//
// Read out as one function because two places apply this same test and must
// not drift: [arnJoinReaches], which routes a type to the tagging or the
// native leg, and [sweepViaTagging]'s own universe guard, which reports a
// type that reached it anyway.
//
// Measured at this commit: this widening moves exactly TWO types from the
// native leg to the tagging leg - aws_customer_gateway, which it was found
// through, and aws_elb, whose classic-load-balancer CFN type sits in
// [elbLoadBalancerEntry]'s coverage and whose mapping row is former2 for the
// same reason. It can only ever move a type whose mapping row's provenance
// the narrow join rejects AND whose CFN type [arnJoinTable] covers; see
// TestArnJoinWideningMovesOnlyProvenanceGapTypes, which recomputes that set
// from the committed artifacts rather than restating it, so a third type
// arriving is a named diff and not a silent one. Neither type is used by any
// estate in live/corpus-manifest.json or live/e2e.
//
// Found building [gauntlet:corpus-vpc-complete/day2_count]: scaling an
// aws_customer_gateway count block from 2 down to 1 proposed no destroy at
// all, because the removal sweep sent the type to the native leg, which
// found no way to list it and reported TYPE_NOT_LISTABLE - a claim
// live/registry.json contradicts for AWS::EC2::CustomerGateway (handlers.list
// true with no required input, taggable true). Stock destroys the higher
// index. Nothing about Cloud Control enumeration changes here: a type this
// predicate now places goes to the TAGGING leg, which never calls
// ListResources, so the "enumerate the wrong CFN type and plan a create for
// something that already exists" hazard the narrow join guards against is
// not on this path.
func arnJoinCFNType(roster *registry.Roster, typeName string) (cfnType string, ok bool) {
	if roster == nil {
		return "", false
	}
	return roster.CloudControlTypeOrService(typeName)
}

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

// markerTFType reads the resource's own tofu-address marker off the tags
// GetResources returned inline for it - no second lookup - and returns the
// TF type it names, when the marker is well-formed enough to name one. It
// is the one piece both disambiguation steps below need: which type this
// object's own marker, ground truth written by choudoufu itself, claims to
// be. A missing, corrupt, or malformed marker returns false and settles
// nothing, on purpose - a marker this package cannot even read is not
// evidence for any type.
func markerTFType(tags map[string]string) (tfType string, ok bool) {
	raw, corrupt := GatherAddress(tags)
	if corrupt || raw == "" {
		return "", false
	}
	escaped := EscapeAddress(raw)
	if !ValidMarkerAddress(escaped) {
		return "", false
	}
	return markerTypeOf(escaped), true
}

// disambiguateByMarker breaks an ARN-shape tie using the resource's own
// tofu-address marker. The marker names the resource it is written on
// (live/MARKERS.md), so when it is well-formed and its own TF type maps to
// one of the ARN join's candidate CFN types, that candidate is the answer -
// the same trust this package already places in a marker everywhere else it
// reads one, just consulted a step earlier here, before the ARN's
// structural ambiguity would otherwise refuse the whole object.
//
// Every other outcome - no usable marker, or one naming a type outside the
// candidate set - returns false and changes nothing: the caller's existing
// "nothing in the ARN says which" refusal stands. This never turns a wrong
// marker into a quiet acceptance; it only lets a correct one settle a tie
// the ARN alone cannot.
func disambiguateByMarker(roster *registry.Roster, tags map[string]string, candidates []string) (cfnType string, ok bool) {
	tfType, ok := markerTFType(tags)
	if !ok {
		return "", false
	}
	markerCFNType, mapped := roster.CloudControlType(tfType)
	if !mapped {
		return "", false
	}
	for _, c := range candidates {
		if c == markerCFNType {
			return markerCFNType, true
		}
	}
	return "", false
}

// disambiguateTFTypeByMarker breaks a mapping-side tie the same way, one
// join stage later than [disambiguateByMarker]: the ARN resolved to exactly
// one CFN type, but live/mapping.json maps that CFN type from more than one
// admitted TF type with nothing about the ARN itself saying which
// (aws_kms_key and aws_kms_external_key both map from AWS::KMS::Key; a
// key's Origin decides which one a live key is, and the ARN does not carry
// it). [resolveDocumentedAlias] already settles the safe case of this
// shape, a documented synonym family where either name reaches the same
// object; this is its counterpart for a genuine two-resource split, using
// the same ground truth the step above does: the object's own marker,
// trusted when it names one of the admitted candidates, ignored otherwise.
func disambiguateTFTypeByMarker(tags map[string]string, admitted []string) (tfType string, ok bool) {
	marked, ok := markerTFType(tags)
	if !ok {
		return "", false
	}
	for _, tf := range admitted {
		if tf == marked {
			return marked, true
		}
	}
	return "", false
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

// admittedOnly keeps the candidates the identity table admits, in the order
// given. The ARN join only ever binds an admitted type, so a candidate the
// table does not carry could never have been the answer - which is what
// makes it safe to read a set of candidates down to one, both when the
// reverse index itself returned several and when it returned none and the
// wider, any-provenance index was consulted instead.
func admittedOnly(candidates []string) []string {
	var admitted []string
	for _, tf := range candidates {
		if _, ok := identity.LookupType(tf); ok {
			admitted = append(admitted, tf)
		}
	}
	return admitted
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
func joinTaggedResource(roster *registry.Roster, arnStr string, tags map[string]string) arnJoinOutcome {
	a, ok := cloudcontrol.ParseARN(arnStr)
	if !ok {
		return arnJoinOutcome{reason: fmt.Sprintf(
			"%q does not have the arn:partition:service:region:account:resource shape",
			arnStr)}
	}

	cfnType, candidates := joinARNToCFNType(a)
	if cfnType == "" && len(candidates) > 1 {
		// The ARN shape alone cannot tell these CFN types apart, but the
		// object carries its own tofu-address marker inline (GetResources
		// returns tags with the ARN, no second call needed) - ground truth
		// about which resource this is, written by choudoufu itself, not a
		// guess. If that marker names an admitted type whose own CFN type
		// is one of the ARN's candidates, the tie is broken; a missing,
		// malformed marker, or one naming something outside the candidate
		// set changes nothing; the refusal below still fires exactly as it
		// did before this existed.
		if resolved, ok := disambiguateByMarker(roster, tags, candidates); ok {
			cfnType = resolved
		}
	}
	if cfnType == "" {
		if len(candidates) > 1 {
			return arnJoinOutcome{reason: fmt.Sprintf(
				"ARN service %q and resource segment %q name more than one CFN type (%s), and nothing in the ARN or the object's own tofu-address marker says which",
				a.Service, resourceSegmentLabel(a), strings.Join(candidates, ", "))}
		}
		return arnJoinOutcome{reason: fmt.Sprintf(
			"no CFN type is known for ARN service %q and resource segment %q",
			a.Service, resourceSegmentLabel(a))}
	}

	tfTypes := roster.TFTypesForCFNType(cfnType)
	if len(tfTypes) == 0 {
		// The narrow reverse index only carries rows whose provenance lets
		// Cloud Control ENUMERATE the type ("name", "alias", "service-alias"
		// - the registry package doc's "What counts as mapped"). Nothing is
		// enumerated here: the Tagging API has already returned this object,
		// ARN and ownership markers together, and the only open question is
		// which TF type it is. So a row this join can use is one that names
		// the type unambiguously among ADMITTED candidates, whatever its
		// provenance - the same admission filter the len>1 branch below
		// already leans on, applied one step earlier.
		//
		// Found building [gauntlet:corpus-vpc-complete/day2_count]: an
		// aws_customer_gateway count block scaled from 2 down to 1 proposed
		// NO destroy at all where stock destroys the higher index, because
		// live/mapping.json's aws_customer_gateway row carries via "former2"
		// and so was invisible to the narrow index - even though
		// live/registry.json states AWS::EC2::CustomerGateway is listable and
		// taggable with a single primary identifier. Silent, not loud: the
		// plan read "No changes. Your infrastructure matches the
		// configuration."
		if admitted := admittedOnly(roster.TFTypesForCFNTypeAnyProvenance(cfnType)); len(admitted) == 1 {
			tfTypes = admitted
		}
	}
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
		admitted := admittedOnly(tfTypes)
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
			} else if marked, ok := disambiguateTFTypeByMarker(tags, admitted); ok {
				// Not a synonym family - a genuine split (a customer-managed
				// KMS key and an external/BYOK one both map from
				// AWS::KMS::Key, and nothing in a key's ARN carries its
				// Origin). Same tiebreak as the ARN-to-CFN step above, one
				// join stage later: the object's own marker names which of
				// the two admitted TF types it actually is.
				admitted = []string{marked}
			} else {
				return arnJoinOutcome{cfnType: cfnType, reason: fmt.Sprintf(
					"CFN type %s (from ARN service %q, resource segment %q) is mapped from more than one TF type in live/mapping.json (%s), and neither the ARN nor the object's own tofu-address marker says which",
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
//
// universe is [sweepTypes]' answer with one further cut ([partitionSweepTypes],
// issue #394): a type [typeNeedsResourceObjectToRecompose] answers true for
// is excluded, because a GetResources candidate carries only the joined
// ARN's own importID and the object's tags, never the schema-typed resource
// [importIdentityFromResource] needs to recompose a mismatched-identity
// companion's identity (aws_default_route_table's vpc_id, issue #332). The
// caller sweeps those types the native way instead
// ([scanTypeReporting]), which does have that resource object.
func sweepViaTagging(ctx context.Context, req Request, decl *declared, res *Result, universe []string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

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
		out := joinTaggedResource(req.Roster, tr.ResourceARN, tr.Tags)
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
		cfnType, mapped := arnJoinCFNType(req.Roster, typeName)
		switch {
		case !mapped || !arnJoinCovers(cfnType):
			// Not reachable through this call's own only caller today:
			// [partitionSweepTypes] builds universe by excluding exactly
			// what this test excludes ([arnJoinReaches], the same
			// mapped-and-covered check), so a type failing it never
			// reaches the tagging leg at all any more - it goes to the
			// native per-type sweep instead (see [partitionSweepTypes]'s
			// own doc comment, corpus-rds-complete-postgres's day2_remove
			// unit). Kept as an invariant guard, the same discipline
			// [bindCountBySlot]'s own Deficit loop applies to its
			// record-backed check: a future caller of [sweepViaTagging]
			// that builds its own universe without going through
			// [partitionSweepTypes] must not silently lose this type
			// rather than report why.
			diags = diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapNoARNJoin,
				Detail: fmt.Sprintf(
					"%s has no CFN type the ARN join table (internal/live/discovery/tagging.go) recognizes, so the tag sweep cannot tell its resources apart from an ARN alone.",
					typeName),
			}))
			continue
		case !req.Roster.Taggable(cfnType) && len(byType[typeName]) == 0:
			// live/registry.json's "taggable" flag is CloudFormation's own
			// claim about whether ITS update-tags API can write this type's
			// tags - a narrower fact than whether the Resource Groups
			// Tagging API can read them. With candidates already joined
			// from a real GetResources response, the registry's claim is
			// refuted empirically for this run; only a type with NO
			// candidates reports the gap. See
			// TestTaggingSweepFindsCandidatesDespiteUntaggableRegistryRow
			// and TestSweepViaTagging_untaggableRegistryRowDoesNotDiscardJoinedCandidates.
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
			diags = diags.Append(fileTaggingCandidate(ctx, req, decl, typeName, c, res))
		}
		res.Scans = append(res.Scans, scan)
	}

	return diags
}

// fileTaggingCandidate applies the same per-resource marker rules
// [scanTypeCloudControl] applies to one Cloud Control ListResources result,
// to one candidate [sweepViaTagging] already joined and grouped by type.
func fileTaggingCandidate(ctx context.Context, req Request, decl *declared, typeName string, c taggedCandidate, res *Result) tfdiags.Diagnostics {
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

	// bindType is the type every declared-set lookup and reported record
	// below uses to find where this object belongs. It starts as typeName -
	// the ARN join's own wire-shape answer - and is corrected to the
	// marker's own type only for the cases [sweepBindType] knows safe: see
	// its own doc comment for the three-way answer and issue #394 for the
	// bug this closes (aws_default_route_table/aws_default_security_group
	// reported malformed when this estate-wide sweep, rather than the
	// config-driven scan, found the shared object first).
	bindType := typeName
	if markerType := markerTypeOf(escaped); markerType != typeName {
		// recompose is nil: this leg carries only the joined ARN and the
		// object's tags, never a raw identifier to recompose an identity
		// from, and typeNeedsResourceObjectToRecompose already keeps every
		// pair that would need one out of this universe - see
		// [sweepBindType]'s own doc comment.
		corrected, fixedImportID, skip := sweepBindType(decl, markerType, typeName, escaped, nil)
		if skip {
			// The marker's own type is declared and was already visited,
			// correctly, by its own config-driven scan pass before this
			// sweep ran - this is the same live object surfacing a second
			// time under the ARN join's generic type name, not a second
			// object. Nothing to file.
			return diags
		}
		if corrected == typeName {
			// sweep is true unconditionally: this leg IS the estate-wide
			// tag sweep, and [partitionSweepTypes] is the only thing that
			// routes a type to it.
			return diags.Append(problemDiag(res, crossTypeMarkerProblem(
				decl, req.Estate, typeName, markerType, raw, liveIDs(c.importID), " (via the tag sweep)", true)))
		}
		bindType = corrected
		if fixedImportID != "" {
			c.importID = fixedImportID
		}
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

	// GitHub issue #906: this object's marker names a declared address,
	// which is exactly the condition the next two branches share. The
	// sighting is filed with the provider configuration that address's
	// block uses, in scope or out, for [Merge] to read across passes.
	noteDeclaredSighting(req, decl, res, bindType, escaped, c.importID)

	if entry, ok := decl.entryFor(bindType, escaped); ok {
		entry.claimants = append(entry.claimants, claim)
		return diags
	}
	if decl.declares(bindType, escaped) {
		// GitHub issue #244, half 2 - the same check discovery.go's own scan
		// loop makes at the same point, for the same reason. See
		// displaced.go.
		switch want, verdict := decl.displacedFrom(ctx, bindType, escaped, claim); verdict {
		case verdictDisplaced:
			return diags.Append(problemDiag(res, displacedProblem(req, bindType, escaped, want, claim)))
		case verdictOwnObject:
			// Issue #692: see Result.VerifiedDeclared - the sighting
			// vouches for the declared instance instead of being
			// discarded.
			if addr, ok := decl.vouchAddr(bindType, escaped); ok {
				res.VerifiedDeclared = append(res.VerifiedDeclared, addr)
			}
		case verdictIdentityChanging:
			// Issue #885: neither reported nor vouched. See
			// [verdictIdentityChanging].
		}
		return diags
	}
	if cb := decl.countBlockFor(bindType, escaped); cb != nil {
		cb.extra = append(cb.extra, claim)
		return diags
	}
	if blk, ok := decl.blocks[bindType][escaped]; ok && blk.keyed {
		blk.claimants = append(blk.claimants, claim)
		return diags
	}
	if orphanAlreadyPresent(res.Orphans, bindType, escaped, c.importID) {
		// See [orphanAlreadyPresent]'s own doc comment. Not reached by
		// rdsClusterInstanceSibling today - typeNeedsResourceObjectToRecompose
		// keeps that pair out of this leg's own sweep universe entirely -
		// but a future sibling pair reaching here through this leg (a
		// registered pair whose ratified rows DO agree,
		// [sameRatifiedIdentity] true, so [typeNeedsResourceObjectToRecompose]
		// would answer false for it) must not be double-filed either.
		return diags
	}
	res.Orphans = append(res.Orphans, OwnedResource{
		TypeName:     bindType,
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
