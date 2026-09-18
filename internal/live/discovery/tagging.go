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
		// A service's ARN is service/cluster-name/service-name (the modern,
		// long ARN format ECS has minted since 2019; see
		// ecs_service.html.markdown's "## Import" section) - unambiguous,
		// no other CFN type shares the "service" segment. [ParseARN] cuts
		// at the FIRST "/" only, so ResourceID keeps the embedded
		// "cluster-name/service-name" slash, which is exactly the shape
		// internal/live/identity's generated table already composes an
		// import ID from (ImportSyntax "CLUSTER/NAME"). GitHub issue #1039:
		// without this row arnJoinReaches is false for aws_ecs_service (no
		// CFN type this table covers), so partitionSweepTypes sent it
		// through the native per-type leg even though the Resource Groups
		// Tagging API - unlike IAM's ("aws_iam_" is
		// taggingAPIUnservedServices) - genuinely does index ECS: the type
		// paid a whole-account, client-side-filtered ListServices instead of
		// riding the sweep's one estate-filtered GetResources call for
		// free. Adding the row moves it there, the same way the iam/policy
		// and cloudfront/distribution rows above closed the identical gap
		// for their own types.
		"service": single("AWS::ECS::Service"),
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
	if taggingAPIUnservedTypeInRegion(req.Region, typeName) {
		return !nativeSweepReaches(req, schemas, typeName)
	}
	return true
}

// nativeSweepReaches reports whether the native per-type sweep leg has any
// route to enumerate typeName at all - the question [arnJoinReaches]'s
// unserved-service term is actually asking, and the one it used to ask as
// [listclient.Schemas.Supports] alone.
//
// Supports is only the FIRST of [scanType]'s routes. When the provider
// offers no list resource, scanType falls through to issue #272's
// content-match leg and then to [cloudControlSource]/[scanTypeCloudControl]
// (issue #47) before it gives up, and a type either of those reaches is one
// the native leg enumerates perfectly well. Reading Supports alone answered
// "no route" for a type Cloud Control lists, sent it back to the tagging
// leg, and - since floci's 2026-09-11 repin stopped answering GetResources
// for IAM at all (lex00/floci#202, live/flociimage_test.go; real AWS serves
// two of the three IAM types this matters for, see [taggingAPIUnservedServices])
// - left the sweep with NO enumeration of aws_iam_instance_profile
// whatsoever. That is issue #881 reopened: the terralith's stage J deletes
// a live, marked, taggable instance profile's block and the plan proposed
// no destroy for it, silently, while the untaggable inline policy beside it
// was destroyed correctly because it derives from its still-declared parent
// role and needs no enumeration at all.
//
// The two [scanType] routes this deliberately does NOT count are its
// marker-index fallback (issue #293) and its located/record fallback
// (#341): both are `!sweep`-gated, so neither is a route for the caller
// this predicate serves. A type no route here answers true for genuinely
// has nowhere to be found, and [sweepViaTagging] now says so rather than
// recording it as covered - see its unserved-with-no-candidates case.
func nativeSweepReaches(req Request, schemas listclient.Schemas, typeName string) bool {
	if schemas.Supports(typeName) {
		return true
	}
	if req.CloudControl != nil {
		if _, ok := identity.ContentMatchTypes[typeName]; ok {
			return true
		}
	}
	_, ccOK := cloudControlSource(req, typeName)
	return ccOK
}

// taggingAPICoverage says where the Resource Groups Tagging API's search
// index holds a resource type's objects. Two fields, because the truth has
// two dimensions and issue #1144 is entirely about the second one: Indexed
// answers "ever, anywhere", and Regions answers "and if so, visible from
// which caller region".
//
// The second dimension is not a refinement. A global service is the case
// that forced it: IAM's objects are account-wide, and GetResources returns
// them in us-east-1 ONLY, whatever region the caller's client is pointed
// at. A shape with no room for that can say "IAM is unserved" or "IAM is
// served" and both are wrong somewhere.
type taggingAPICoverage struct {
	// Indexed is false when GetResources never returns this type's
	// resources in ANY region, however they are tagged.
	Indexed bool

	// Regions are the ONLY caller regions whose GetResources index holds
	// the type. Empty with Indexed true means every region - the ordinary
	// case, and the reason a type with no entry at all needs no entry.
	// Meaningless when Indexed is false.
	Regions []string

	// Evidence is the measurement this row stands on, so a reader can tell
	// a probed row from an inherited default without leaving this file.
	Evidence string
}

// taggingAPIServiceCoverage is the DEFAULT coverage for every type in a
// service, keyed by resource-type name prefix - the one spelling every
// caller has with no roster in hand. A type with its own row in
// [taggingAPITypeCoverage] overrides it.
//
// The prefix was the whole representation before issue #1144. It was probed
// against real AWS 2026-09-01 with a ROLE - an IAM role tagged at create
// never appeared in us-east-1 or us-east-2, with a tag filter and with a
// bare resource-type filter (issue #692) - and generalised from that one
// type to the service. Issue #1134 re-measured a live account at scale 50
// and found the role does not speak for the service: RGTA returns 0 for
// iam:role in every region, and 500 each for iam:policy and
// iam:instance-profile in us-east-1. So the prefix survives here as the
// conservative default for the twenty-odd aws_iam_ types nobody has
// measured, and the three that HAVE been measured say so themselves below.
//
// Being parseable by [arnJoinTable] is not the same fact as being SERVED by
// GetResources, and conflating the two routed IAM to the tagging universe
// where its sightings simply never happened: the terralith's client-named
// IAM majority went unvouched (6 of 38 instances), and the state cache -
// which may only serve what the sweep vouches for - was structurally
// useless for exactly the estates it helps most.
var taggingAPIServiceCoverage = map[string]taggingAPICoverage{
	"aws_iam_": {
		Indexed: false,
		Evidence: "issue #692, probed against real AWS 2026-09-01 with an aws_iam_role and generalised to the " +
			"service. Kept as the default for the aws_iam_ types nobody has measured since; the three #1134 did " +
			"measure carry their own rows in taggingAPITypeCoverage",
	},
}

// taggingAPITypeCoverage is the per-TYPE truth, and it wins over
// [taggingAPIServiceCoverage]'s prefix default for the type it names.
//
// Every row here is a measurement against a real AWS account, and two of
// the three contradict the service default they sit under. That is issue
// #1144: one prefix cannot say "roles no, policies yes, and policies only
// from us-east-1".
//
// aws_iam_role is listed even though it AGREES with the default. Recording
// only the rows that disagree would leave the next reader unable to tell a
// measured agreement from an unmeasured inheritance - and this row is the
// one the other two were wrongly generalised from, so its provenance is
// worth carrying explicitly.
var taggingAPITypeCoverage = map[string]taggingAPICoverage{
	"aws_iam_role": {
		Indexed: false,
		Evidence: "issues #692 and #1134: GetResources returns 0 for iam:role in every region while " +
			"iam:ListRoleTags shows the tags sitting on the objects (550 of 550 at scale 50, stable over 35 " +
			"minutes). Faithfully emulated - the pinned floci serves no iam:role through GetResources either " +
			"(live/floci-capabilities.json's tagging-sweep row for the type)",
	},
	"aws_iam_policy": {
		Indexed: true,
		Regions: []string{"us-east-1"},
		Evidence: "issue #1134: 500 returned in us-east-1 at scale 50, 0 in us-east-2, reproduced on two " +
			"estates. IAM is global and the index holds it in us-east-1 only. lex00/floci#205 (tracked as " +
			"#1152) taught the emulator the same split, so this row is exercisable without an AWS account - " +
			"see TestPerRegionTaggingRoutingAgainstFloci",
	},
	"aws_iam_instance_profile": {
		Indexed: true,
		Regions: []string{"us-east-1"},
		Evidence: "issue #1134, measured in the same pass as aws_iam_policy above and with the same result: " +
			"500 in us-east-1, 0 in us-east-2. This is the type issue #881's silently-omitted destroy is about",
	},
}

// taggingAPICoverageFor returns typeName's recorded coverage, and whether
// there is one at all. Most types have none, which means the ordinary case:
// GetResources indexes them, in every region.
//
// The type's own row wins over the service prefix; among prefixes the
// longest match wins, so a future "aws_iam_group_" default could sit under
// "aws_iam_" without either of them depending on Go's map order - the
// hazard this repository has been bitten by before.
func taggingAPICoverageFor(typeName string) (taggingAPICoverage, bool) {
	if cov, ok := taggingAPITypeCoverage[typeName]; ok {
		return cov, true
	}
	var best string
	var bestCov taggingAPICoverage
	for prefix, cov := range taggingAPIServiceCoverage {
		if strings.HasPrefix(typeName, prefix) && len(prefix) > len(best) {
			best, bestCov = prefix, cov
		}
	}
	if best == "" {
		return taggingAPICoverage{}, false
	}
	return bestCov, true
}

// taggingAPIUnservedTypeInRegion reports whether an estate-wide
// GetResources issued from region can never return typeName's resources,
// however they are tagged.
//
// region is the caller's own region - [Request.Region], the one region a
// provider pass lists in. An EMPTY region is not "every region": it is an
// unknown one (the provider resolves its own, and this package never sees
// the answer), and a type the index holds in us-east-1 alone is
// unreachable from an unknown region as far as anything here can prove. So
// empty answers the conservative way, which is also exactly what this
// predicate answered for every IAM type before #1144 - a fixture that sets
// no Region keeps the behaviour it had.
func taggingAPIUnservedTypeInRegion(region, typeName string) bool {
	cov, ok := taggingAPICoverageFor(typeName)
	if !ok {
		return false
	}
	if !cov.Indexed {
		return true
	}
	if len(cov.Regions) == 0 {
		return false
	}
	if region == "" {
		return true
	}
	for _, r := range cov.Regions {
		if r == region {
			return false
		}
	}
	return true
}

// taggingAPIRestrictedType reports whether typeName's index coverage is
// anything other than the ordinary "every region" one - unserved
// everywhere, or served from some regions only.
//
// This is deliberately REGION-BLIND, and the one caller that wants it is
// [sweepTypes]: its question is "is this a type whose live objects some
// pass might fail to see through the one estate-wide GetResources call",
// which is about the type, not about today's pass. Answering it per region
// would drop a declared aws_iam_policy out of the sweep universe on a
// us-east-1 run and take its tagging-leg coverage with it, because
// [sweepViaTagging] only files candidates for types in the universe it was
// handed.
func taggingAPIRestrictedType(typeName string) bool {
	_, ok := taggingAPICoverageFor(typeName)
	return ok
}

// tagIndexHeldNothingGap is issue #1318's third verdict, and the whole of it
// is one question asked of the right source: can this type carry a tag?
//
// [sweepViaTagging]'s registry-untaggable arm reaches two situations that
// look identical from where it stands - an empty candidate list for a type
// live/registry.json calls untaggable - and are not the same fact at all:
//
//	the type has no tags argument     -> nothing was ever there to find
//	the type has one and none came back -> something may well have been there
//
// live/registry.json cannot settle it, because its taggable flag is
// CloudFormation's claim about whether ITS update-tags API writes the type's
// tags; AWS::IAM::Policy and AWS::IAM::InstanceProfile are false there while
// the provider gives both a tags argument and [internal/live/stamp] writes
// this estate's marker onto every object of both. [typeTaggable] asks the
// provider's own resource schema instead, which is the same source
// live/survey-full.json's signals.taggable column and stamping itself use,
// and it is the whole distinction.
//
// Two further terms, and both NARROW rather than widen:
//
//   - [taggingAPIRestrictedType]. Measured over the admitted table at
//     provider 6.59.0, six types reach this arm at all with a taggable
//     provider schema, and three of them (aws_launch_template and the two
//     aws_vpc_security_group_*_rule types) have ordinary index coverage -
//     the index holds them in every region. For those, an empty answer is
//     the same evidence the entire tagging leg rests on; it is what "this
//     estate owns none of this type" looks like for every one of the
//     hundreds of types in the universe, and raising it to a per-run
//     diagnostic would bury the case where the index is known NOT to behave
//     ordinarily. They keep [SweepGapNotTaggable] and its suppression,
//     unchanged by #1318 - their wording is still wrong about them, and the
//     repair for that is live/registry.json's generator (#1318's candidate
//     1), not this arm.
//
//   - the region term. A restricted type is only reported this way from a
//     region its coverage row says DOES serve it. From any other region the
//     loud [SweepGapNoEnumerationRoute] arm above has already taken the run
//     and said something stronger; asking the question here again would
//     depend on that arm's ordering rather than on its own terms.
//
// What is left is exactly #1318's case: a type that carries a marker, in a
// region whose index holds that type, where the index answered and held none
// of this estate's. Issue #1046 measured that answer being wrong on a real
// account - 104 of 1,655 stamped objects, about 21 minutes after migrate had
// verified every one of them.
func tagIndexHeldNothingGap(schemas listclient.Schemas, region, typeName, cfnType string) (SweepGap, bool) {
	if !typeTaggable(schemas, typeName) {
		return SweepGap{}, false
	}
	cov, restricted := taggingAPICoverageFor(typeName)
	if !restricted || taggingAPIUnservedTypeInRegion(region, typeName) {
		return SweepGap{}, false
	}
	where := "this caller region"
	if len(cov.Regions) > 0 {
		where = strings.Join(cov.Regions, ", ")
	}
	return SweepGap{
		TypeName: typeName,
		Reason:   SweepGapTagIndexHeldNothing,
		Detail: fmt.Sprintf(
			"live/registry.json records %s (Cloud Control type %s) as untaggable, but that flag is CloudFormation's claim about its own update-tags API rather than a fact about the live object: the provider gives %s a tags argument and choudoufu writes this estate's ownership marker onto it. So the estate-wide tag sweep did search for it, and the Resource Groups Tagging API - whose index holds this type from %s alone - answered holding none of this estate's. An empty answer there is not evidence this estate owns no undeclared %s: this index has been measured lagging its own marker writes by around twenty minutes at volume on a real account, and this is the one type family whose index is also region-scoped. This run therefore claims no coverage for %s - if a block of it was deleted, no destroy is proposed for the live resource, and none will be until the index holds it. Re-run the plan, or remove it by hand.",
			typeName, cfnType, typeName, where, typeName, typeName),
	}, true
}

// taggingAPIListDropsTags reports whether this repository has MEASURED the
// provider's own list route for typeName returning objects with their tags
// stripped, so that an untagged listing is evidence the route is blind
// rather than evidence the objects are untagged.
//
// This used to be the same predicate as the index question above, and
// [sweepMarkerReadGap]'s own doc comment said in as many words that two
// facts riding one list is a shape that has misled this repository before,
// that widening it needs a second measured tag-dropping list call, and that
// "find one in a service GetResources DOES index and this gate is the wrong
// gate for it - the predicate has to split". Issue #1144 is that case
// arriving: GetResources does index iam:policy in us-east-1, and
// iam:ListPolicies drops tags there all the same. So the predicate split,
// and this half kept the prefix, because the prefix is what the tag-
// dropping measurements actually cover: iam:ListRoles returns no tags at
// all (issue #266, and [stripTags]), iam:ListPolicies likewise
// (directread.go, issue #1046), and #1134 re-confirmed on a real account
// that the tags sit on the objects while neither route shows them.
//
// Widening this past the prefix still needs a second measured tag-dropping
// list call, named and quoted. It is not an inference from an absence, and
// it is not the index table above.
func taggingAPIListDropsTags(typeName string) bool {
	for prefix := range taggingAPIListDropsTagsServices {
		if strings.HasPrefix(typeName, prefix) {
			return true
		}
	}
	return false
}

// taggingAPIListDropsTagsServices is [taggingAPIListDropsTags]'s data: the
// services whose provider list calls this repository has measured returning
// objects without their tags. One prefix per measured service; the doc
// comment above carries the citations.
var taggingAPIListDropsTagsServices = map[string]bool{
	"aws_iam_": true,
}

// TaggingAPIUnservedTypeInRegion is [taggingAPIUnservedTypeInRegion]
// exported for callers outside this package that need the same routing
// preference without re-deriving it - today tools/survey-gen/classify.go
// (issue #1133), which stops the survey from asserting a tag-filtered-list
// recovery route this package's own sweep does not take.
//
// It takes the region rather than hiding it, which is the point of issue
// #1144's change: the survey has no region and passes "", and that is a
// fact about the survey worth being made to state. See
// [TaggingAPIIndexRegions] for what such a caller can say instead of
// nothing.
func TaggingAPIUnservedTypeInRegion(region, typeName string) bool {
	return taggingAPIUnservedTypeInRegion(region, typeName)
}

// TaggingAPIIndexRegions returns the regions GetResources indexes typeName
// in, for a caller that has no region of its own and has to describe the
// coverage rather than apply it.
//
// Three answers, and they are distinguishable: a nil slice with ok true
// means "indexed everywhere", a nil slice with ok false means "indexed
// nowhere, in any region", and a non-empty slice means "only from these".
func TaggingAPIIndexRegions(typeName string) (regions []string, indexed bool) {
	cov, ok := taggingAPICoverageFor(typeName)
	if !ok {
		return nil, true
	}
	if !cov.Indexed {
		return nil, false
	}
	return cov.Regions, true
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
func sweepViaTagging(ctx context.Context, req Request, schemas listclient.Schemas, decl *declared, res *Result, universe []string) tfdiags.Diagnostics {
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
		case taggingAPIUnservedTypeInRegion(req.Region, typeName) && len(byType[typeName]) == 0 && typeTaggable(schemas, typeName):
			// Issue #881, reopened. A type in a service GetResources does
			// not index only reaches this leg as a LAST RESORT:
			// [arnJoinReaches] sends it here precisely when
			// [nativeSweepReaches] found no native route for it. So zero
			// candidates here does not mean "the estate owns none of this
			// type" the way it does for any other type in this universe -
			// it means every leg looked nowhere it could have been found,
			// and a deleted block's live object goes unproposed.
			//
			// It sits ABOVE the registry-untaggable case deliberately.
			// That one files [SweepGapNotTaggable], which [sweepGapDiag]
			// SUPPRESSES - correct for a type that could never carry a
			// marker, wrong for aws_iam_instance_profile, whose registry
			// row says taggable:false while the provider schema and every
			// stamped object in the account say otherwise. Ordered the
			// other way round, the loud gap below would be swallowed by
			// the quiet one. [typeTaggable] is the tiebreaker, read from
			// the provider's own schema; a type BOTH sources call
			// untaggable still falls through to the quiet case.
			//
			// Before this, a taggable type with no candidates fell
			// straight through to a TypeScan with Listed:0 and
			// res.SweepCovered recording it as covered, with no gap at
			// all. [arnJoinReaches]'s own doc comment already promised
			// this gap was reported "loudly" - this is the code that
			// keeps that promise.
			diags = diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapNoEnumerationRoute,
				Detail: fmt.Sprintf(
					"Nothing in this run can enumerate %s: the Resource Groups Tagging API does not index its service (so the estate-wide tag sweep's one GetResources call never returns its resources, no matter how they are tagged), the provider offers it no list resource, and no Cloud Control listing was available for it either. If this estate owns a %s that the configuration no longer declares, this run did not look for it and proposes no destroy for it - remove it by hand, or re-declare it. Configuring Cloud Control (TOFU_LIVE_CLOUDCONTROL) gives the sweep a route to this type where one exists.",
					typeName, typeName),
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
			//
			// Issue #1144 made [taggingAPIUnservedTypeInRegion] region-aware
			// above, which opened a door these two cases could not both be
			// behind before: in us-east-1 aws_iam_instance_profile and
			// aws_iam_policy are now SERVED, so the loud case declines and
			// a run whose index answered with nothing for them lands here
			// instead - registry taggable:false, provider schema
			// taggable:true, gap suppressed. Adding [typeTaggable] to this
			// condition to close that was tried and REVERTED, because it
			// made the case worse rather than better: it turned the type
			// into an ordinary covered scan with Listed:0, which claims the
			// sweep established the estate owns none, and dropped it out of
			// [Result.SweepGaps] where internal/live/foreign reads it to
			// decline calling such objects foreign (#1153). A recorded,
			// suppressed gap under-claims; "covered, nothing found"
			// over-claims, and over-claiming is the one this project's
			// safety rule forbids.
			//
			// Issue #1318 is that door, closed the other way: the case
			// still matches and the type still stays out of
			// [Result.SweepCovered], so nothing is over-claimed and no
			// [Result.SweepGaps] entry is lost. What changes is WHICH gap
			// is filed. [tagIndexHeldNothingGap] answers with a third
			// verdict for the type this arm cannot honestly call
			// untaggable, and only for it; every other type reaching here
			// keeps exactly the gap it had.
			if g, ok := tagIndexHeldNothingGap(schemas, req.Region, typeName, cfnType); ok {
				diags = diags.Append(sweepGapDiag(res, g))
				continue
			}
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
		// [declaredEntry.addClaimant], not a bare append: this leg and the
		// config-driven scan can both enumerate one declared type in the
		// same pass, and they then see the same live object.
		entry.addClaimant(claim)
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
