// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "sort"

// TypeIdentity is what this package knows about one resource type's
// identity: whether the identity exists in configuration at all, how to
// build it from configuration arguments if it does, and which of the type's
// attributes carry that identity when another resource refers to it.
type TypeIdentity struct {
	// Type is the resource type name, e.g. "aws_route".
	Type string

	// ServerAssigned is true when the identity is minted by the provider's
	// API at create time and appears nowhere in configuration. Instances of
	// such a type always classify as ClassNeedsDiscovery, whatever their
	// arguments say.
	ServerAssigned bool

	// Reason is the operator-facing explanation attached to every
	// ClassNeedsDiscovery resolution of this type. Required when
	// ServerAssigned, unused otherwise.
	Reason string

	// Components build the import identity by concatenation, in order.
	// Required unless ServerAssigned.
	Components []Component

	// ImportSyntax documents the provider's import-ID grammar for this
	// type, in the provider documentation's own notation (e.g.
	// "ROUTETABLEID_DESTINATION"). Documentation only: Components is what
	// the code follows.
	ImportSyntax string

	// IdentityAttrs are the attribute names whose value equals this type's
	// identity, so that a reference to one of them from another resource
	// can be resolved without a cloud read. A reference to any other
	// attribute of this type is an error rather than a guess.
	//
	// An empty list means no attribute of this type is usable as an
	// identity source, which is the honest answer for types whose "id"
	// attribute is a provider-synthesized value distinct from the import
	// ID.
	IdentityAttrs []string

	// Synthesized is true when this entry was not written by hand but built
	// from the provider's own identity schema at resolution time. See
	// [SynthesizeTypeIdentity].
	Synthesized bool

	// Admits records what backed a synthesized entry: the schemas alone
	// ([AdmitSchema]) or the schemas plus this configuration
	// ([AdmitConfigSignal]). Empty for a hand-written row, which is asserted
	// rather than derived.
	Admits Admission
}

// Component is one piece of an import identity: a fixed separator (Literal,
// with Attrs empty and Cloud unset), a resource argument (Attrs, first
// present wins), or a property of the cloud the run is against (Cloud).
type Component struct {
	// Literal is a fixed fragment of the import ID, used for separators.
	Literal string

	// Attrs names the resource arguments to read, in preference order. The
	// first one present in configuration supplies the value; if none is
	// present, resolution fails naming all of them.
	Attrs []string

	// Cloud names a value that comes from the cloud the run is pointed at
	// rather than from the configuration: the AWS account ID, the region.
	// [CloudNone] - the zero value - means this component is a literal or an
	// argument.
	//
	// A component like this is why [CloudContext] exists. Nothing in a
	// configuration says which account it will be applied to, so an identity
	// that embeds the account is not computable from configuration alone,
	// and this package will not read it from anywhere: it is handed the
	// values or it says it does not have them. See [ResolveIn].
	Cloud CloudValue

	// IdentityAttr names the attribute of the provider's resource identity
	// schema that this component supplies, and is the inference the schemas
	// themselves do not carry: an identity schema says a route is identified
	// by a route_table_id, not that the route_table_id argument is where
	// that value is written.
	//
	// Three values, and the helpers below spell all three:
	//
	//   - [SameNameIdentity], what [attr] sets, is the ordinary case: the
	//     identity attribute is whichever argument supplied the value, under
	//     its own name. It is a rule rather than a name because the argument
	//     is chosen per instance - aws_route reads whichever of three
	//     destination arguments the route uses, and the provider's identity
	//     schema names all three.
	//   - An explicit attribute name, what [inAttr] sets, for the case where
	//     the two differ. Several components may name one attribute, and
	//     their rendered strings then concatenate in order to form it, which
	//     is how an SNS topic's single "arn" is built out of a literal
	//     prefix, the region, the account and the topic's name.
	//   - Empty: this component supplies no identity attribute at all. That
	//     is every separator between two identity attributes - exactly the
	//     character that stops mattering once a run imports by identity
	//     object - and it is what [idlessAttr] says about an argument whose
	//     value is part of the import string and part of no identity.
	//
	// An entry whose components do not supply every attribute the provider
	// requires for import cannot be imported by identity, and its import-ID
	// string is used instead. aws_route_table_association is the archetype:
	// the provider identifies an association by the rtbassoc- ID it assigns,
	// and the table builds the association's documented import string out of
	// a subnet and a route table. Both are right about different things, and
	// only one of them is an identity object.
	IdentityAttr string
}

// SameNameIdentity is [Component.IdentityAttr] for the ordinary case: the
// identity attribute is the argument that supplied the value, under its own
// name. It is not an attribute name and no schema has one - the character is
// not legal in an identifier - because it stands for a rule that is applied
// per instance, once the argument is chosen.
const SameNameIdentity = "*"

// CloudValue names one property of the cloud a run is against, for a
// [Component] whose value the configuration does not carry.
//
// The set is deliberately tiny and closed. It covers the two substitutions
// the AWS provider's account-embedded import identities need - an SQS queue
// URL (https://sqs.REGION.amazonaws.com/ACCOUNT/NAME) and an SNS topic ARN
// (arn:aws:sns:REGION:ACCOUNT:NAME) - and nothing else. A third kind is a
// decision to make deliberately, not a slot to fill: the partition ("aws",
// "aws-cn", "aws-us-gov") is written as a literal in the templates below
// because every one of those identities is partition-specific in more places
// than the partition segment, and a run against a non-commercial partition
// needs a review rather than a substitution.
type CloudValue string

const (
	// CloudNone is the zero value: this component reads no cloud property.
	CloudNone CloudValue = ""

	// CloudAccountID is the AWS account ID the run is against, twelve
	// digits, no hyphens.
	CloudAccountID CloudValue = "account-id"

	// CloudRegion is the region the run is against, e.g. "us-east-1".
	CloudRegion CloudValue = "region"
)

// describe renders a cloud value for an operator-facing sentence.
func (c CloudValue) describe() string {
	switch c {
	case CloudAccountID:
		return "AWS account ID"
	case CloudRegion:
		return "region"
	default:
		return string(c)
	}
}

// CloudContext is the set of cloud properties a caller can supply so that
// identities embedding them become computable. Both fields are optional; an
// empty one means "not known", never "empty string".
//
// This is the whole of the escape hatch, and its shape is what keeps this
// package cloud-free and process-free (see the package documentation).
// Nothing here is discovered: the values arrive from a caller that already
// has them, and a caller that has none passes the zero value and gets
// [ClassNeedsDiscovery] for the types that need them rather than an error.
//
// Where the values come from in this fork, when they come from anywhere:
// the region is the provider configuration's own, which
// [internal/live/discovery.Request.Region] already carries and which
// every signed request the list clients make must have. The account ID first
// becomes knowable one phase later still - the AWS provider puts an
// account_id attribute in the resource identity objects it attaches to list
// results, which [internal/live/discovery.TypeScan.AccountID] records
// as the scan runs. Both are therefore behind the provider, and identity
// resolution runs in front of it, which is why the pipeline as it stands
// passes the zero value and the two account-derived types below reach their
// live resources through their markers instead.
type CloudContext struct {
	// AccountID is the AWS account ID the run is against.
	AccountID string

	// Region is the region the run is against.
	Region string
}

// value returns the context's value for one cloud property, and whether the
// caller supplied it.
func (c CloudContext) value(which CloudValue) (string, bool) {
	var v string
	switch which {
	case CloudAccountID:
		v = c.AccountID
	case CloudRegion:
		v = c.Region
	default:
		return "", false
	}
	return v, v != ""
}

// attr reads the first of these arguments the configuration sets, and
// supplies the identity attribute of that same name. That is the whole of the
// inference for all but three of the entries below.
func attr(names ...string) Component {
	return Component{Attrs: names, IdentityAttr: SameNameIdentity}
}

// idlessAttr reads an argument that is part of the import-ID string and part
// of no identity attribute, which makes its type importable by string only.
// Used where the provider identifies a resource by something the
// configuration does not hold at all; the entry says so rather than mapping
// the argument onto an attribute that means something else.
func idlessAttr(names ...string) Component { return Component{Attrs: names} }

// sep is a fixed fragment of the import ID between two identity attributes.
// It supplies no identity attribute, which is the point: it is the character
// that stops existing once a run imports by identity object.
func sep(s string) Component { return Component{Literal: s} }

// cloud is a property of the cloud the run is against. See [CloudValue].
func cloud(v CloudValue) Component { return Component{Cloud: v} }

// inAttr says a component supplies one named identity attribute rather than
// the one its own name implies. Several components wrapped in the same name
// concatenate into it, in order.
func inAttr(identityAttr string, c Component) Component {
	c.IdentityAttr = identityAttr
	return c
}

func serverAssigned(typeName, reason, importSyntax string, identityAttrs ...string) TypeIdentity {
	return TypeIdentity{
		Type:           typeName,
		ServerAssigned: true,
		Reason:         reason,
		ImportSyntax:   importSyntax,
		IdentityAttrs:  identityAttrs,
	}
}

// DefaultTable is the v0 identity table: the AWS resource types the estate
// fixture (live/e2e/estate) uses, which are also the types the P1.1
// admission lint admits. A type absent from this table is outside
// the stateless subset and resolving it is an error.
//
// The import-ID grammars below are the AWS provider's documented ones (see
// each type's "Import" section in the provider docs); the identity
// attributes are the attributes the provider sets to that same value.
//
// What is left of this table, now that the schemas are plumbed
// (opentofu#2854: providers.ResourceIdentitySchema and the per-resource
// providers.Schema.IdentitySchema returned by GetProviderSchema):
//
// A type whose identity is one attribute named after the argument that
// supplies it no longer needs a row at all. [SynthesizeTypeIdentity] builds
// one from the schema whenever the caller had schemas to hand, so the rows
// below that say only "one argument, same name" are now a pre-provider
// fallback rather than the only way in - which is what the first paragraph of
// this comment used to say could not be done, because GetProviderSchema needs
// a plugin process and this package is deliberately process-free. It still
// is: the schemas arrive as a parameter, from a caller that has them.
//
// What survives is the inference no schema carries, and it is now written
// down attribute by attribute rather than as a string grammar. An identity
// schema says a route is identified by a route_table_id; that the
// route_table_id *argument* is where the value is written is this table's
// claim, and [Component.IdentityAttr] is where it is made. So is the
// composition of an SNS topic's single arn out of four fragments, and so is
// the refusal of aws_route_table_association, whose identity is a value no
// configuration holds.
//
// The separator characters below are on their way out. Every type whose
// components supply the whole of what the provider requires for import is
// asked for by identity object now, and for those the "_" and "/" and "," are
// dead weight kept for the operator-facing string and for the types with no
// identity schema at all. See [VerifyTable] and
// internal/live/projection/build.go.
var DefaultTable = buildTable(
	// ---- Server-assigned identities (admission path 2: markers) ----------

	serverAssigned("aws_vpc",
		"EC2 assigns the VPC ID (vpc-…) at create time; no configuration argument determines it.",
		"vpc-ID", "id"),
	serverAssigned("aws_subnet",
		"EC2 assigns the subnet ID (subnet-…) at create time; the CIDR and AZ in config do not name it.",
		"subnet-ID", "id"),
	serverAssigned("aws_security_group",
		"EC2 assigns the security group ID (sg-…) at create time; the group name in config is not its import identity.",
		"sg-ID", "id"),
	serverAssigned("aws_route_table",
		"EC2 assigns the route table ID (rtb-…) at create time.",
		"rtb-ID", "id"),
	serverAssigned("aws_internet_gateway",
		"EC2 assigns the internet gateway ID (igw-…) at create time.",
		"igw-ID", "id"),
	// First slice of the survey's marker cohort (#20). Neither type's
	// import ID can be built from configuration, which is the whole reason
	// they are here rather than in the client-named section: discovery
	// finds them by their tags and reads the identity off the list result.
	serverAssigned("aws_kms_key",
		"KMS assigns the key ID (a UUID) at create time; a key has no name argument, and its description names nothing the API accepts as an identity.",
		"KEYID", "id", "key_id"),
	serverAssigned("aws_route53_zone",
		"Route 53 assigns the hosted zone ID (Z…) at create time; the zone's domain name is not its import identity, and two zones may carry the same name.",
		// The provider's identity schema for this type names zone_id, not
		// id, so both are listed: the resource's own id attribute equals
		// the zone ID, and zone_id is what a list result carries.
		"ZONEID", "id", "zone_id"),
	serverAssigned("aws_eip",
		"EC2 assigns the allocation ID (eipalloc-…) at create time; count instances are fungible and no argument distinguishes them.",
		"eipalloc-ID", "id", "allocation_id"),
	// The ELBv2 chain (#20). All three are named in configuration and none
	// of those names is the identity: ELBv2 mints an ARN per object, the
	// provider's identity schema for each of them requires exactly that ARN,
	// and the resource's own id attribute is set to the same string. "arn"
	// comes first in each list because a list result carries the identity
	// object, which holds arn and not id.
	serverAssigned("aws_lb",
		"ELBv2 assigns the load balancer ARN at create time; the name argument is client-chosen but the API accepts only the ARN as an identity, and a deleted-and-recreated load balancer of the same name has a different one.",
		"LOADBALANCERARN", "arn", "id"),
	serverAssigned("aws_lb_target_group",
		"ELBv2 assigns the target group ARN at create time; as with the load balancer, the name argument names the group but does not identify it.",
		"TARGETGROUPARN", "arn", "id"),
	serverAssigned("aws_lb_listener",
		"ELBv2 assigns the listener ARN at create time; a listener has no name argument at all, only a port and a protocol, which do not identify it either.",
		"LISTENERARN", "arn", "id"),
	// Third slice of the survey's marker cohort (#20). The three EC2 types
	// follow the aws_vpc shape exactly: a server-minted ID and nothing in
	// configuration that names it. The security group rules are one
	// resource per rule — the resource type exists precisely so that each
	// rule has its own identity and its own tags, unlike the inline
	// ingress/egress blocks of aws_security_group. The ACM and Step
	// Functions pair identify by an ARN, like the ELBv2 chain, and "arn"
	// leads their lists for the same reason: a list result carries the
	// identity object, which holds arn and not id.
	serverAssigned("aws_vpc_security_group_ingress_rule",
		"EC2 assigns the security group rule ID (sgr-…) at create time; a rule's port range, protocol and CIDR describe it but do not identify it.",
		"sgr-ID", "id", "security_group_rule_id"),
	serverAssigned("aws_vpc_security_group_egress_rule",
		"EC2 assigns the security group rule ID (sgr-…) at create time; a rule's port range, protocol and CIDR describe it but do not identify it.",
		"sgr-ID", "id", "security_group_rule_id"),
	serverAssigned("aws_launch_template",
		"EC2 assigns the launch template ID (lt-…) at create time; the name argument is client-chosen but the provider's identity schema requires the ID.",
		"lt-ID", "id"),
	serverAssigned("aws_acm_certificate",
		"ACM assigns the certificate ARN at create time; the domain name is not an identity, and several certificates may cover the same domain.",
		"CERTIFICATEARN", "arn", "id"),
	serverAssigned("aws_sfn_state_machine",
		"Step Functions assigns the state machine ARN at create time; the name argument is client-chosen, but the provider's identity schema requires the ARN, which wraps it in an account and a region the configuration does not carry.",
		"STATEMACHINEARN", "arn", "id"),
	serverAssigned("aws_ebs_volume",
		"EC2 assigns the volume ID (vol-…) at create time; a volume's size and availability zone describe it but do not identify it.",
		"vol-ID", "id"),

	// ---- Client-named identities (admission path 1) ----------------------

	TypeIdentity{
		Type:          "aws_s3_bucket",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKETNAME",
		IdentityAttrs: []string{"id", "bucket"},
	},
	TypeIdentity{
		Type:          "aws_iam_role",
		Components:    []Component{attr("name")},
		ImportSyntax:  "ROLENAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "LOGGROUPNAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// The receipt demo (PE.3, live/RECEIPTS.md): a plain
		// client-named resource, admitted the same way a bucket or a role
		// is. Its name is the parameter path
		// (/tofu-receipts/<estate>/<effect>), already fixed in config.
		Type:          "aws_ssm_parameter",
		Components:    []Component{attr("name")},
		ImportSyntax:  "PARAMETERNAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// First slice of the survey's client-named cohort (#19). The
		// provider's documented import ID is the table name, and its id
		// attribute is set to that same name.
		Type:          "aws_dynamodb_table",
		Components:    []Component{attr("name")},
		ImportSyntax:  "TABLENAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// Same slice (#19). Imports by the cluster name, but the provider
		// sets id to the cluster ARN, not the name, so id must not be
		// handed out as an identity source; only name carries the import
		// ID. Same standard of care as aws_route's synthesized id.
		Type:          "aws_ecs_cluster",
		Components:    []Component{attr("name")},
		ImportSyntax:  "CLUSTERNAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// #19's second slice. A metric alarm imports by its alarm_name
		// argument, and its id attribute is set to that same name.
		Type:          "aws_cloudwatch_metric_alarm",
		Components:    []Component{attr("alarm_name")},
		ImportSyntax:  "ALARMNAME",
		IdentityAttrs: []string{"id", "alarm_name"},
	},
	TypeIdentity{
		// #19's second slice. A KMS alias imports by its name argument,
		// the full alias/... string, and its id attribute equals it. The
		// alias is the client-named handle on a key whose own identity is
		// marker-discovered; target_key_id plays no part in the alias's
		// identity.
		Type:          "aws_kms_alias",
		Components:    []Component{attr("name")},
		ImportSyntax:  "alias/ALIASNAME",
		IdentityAttrs: []string{"id", "name"},
	},

	// ---- Account-derived identities: client-named in configuration, but
	// ---- the provider's import identity embeds the account and the region
	// ---- (live/SURVEY.md flag F2) ---------------------------------
	//
	// This is the survey's client-named path failing the strict version of
	// it: the configuration holds the name and the provider wants an ARN
	// built around it. A [CloudContext] is what closes the gap, and without
	// one the instance resolves as ClassNeedsDiscovery and is found by its
	// tags like any marker type - which is what happens in this fork's
	// pipeline today, because identity resolution runs before any provider
	// does. See [CloudContext].
	//
	// The partition segment is a literal "aws" rather than a third cloud
	// value; see [CloudValue] for why.

	// aws_sqs_queue is the type this section was designed around. Its
	// identity is the same shape as the topic's below -
	// https://sqs.REGION.amazonaws.com/ACCOUNT/NAME - and the messaging
	// batch (below, in this table's own "Registry-ratified" second
	// section) now wires it with exactly that template. The fork still
	// cannot supply a CloudContext, so a context-less run reaches a queue
	// through its marker, and the marker path is where the emulator
	// breaks: floci reports a queue's URL as its own endpoint
	// (http://localhost:4566/ACCOUNT/NAME), and the AWS provider's
	// importer parses only the amazonaws.com form, so the import fails on
	// the very string the list call handed back. Real AWS returns the
	// canonical URL and has no such gap. See
	// live/e2e/estates/messaging/README.md and choudoufu#26.
	TypeIdentity{
		Type: "aws_sns_topic",
		// Every component feeds one identity attribute, the arn, which the
		// provider's identity schema is the only thing that requires. The
		// colons here are inside a value rather than between two of them,
		// so unlike every other separator in this table they survive
		// import-by-identity.
		Components: []Component{
			inAttr("arn", sep("arn:aws:sns:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:sns:REGION:ACCOUNT:TOPICNAME",
		IdentityAttrs: []string{"arn", "id"},
	},

	// ---- Composed identities: concrete or parent-derived depending on
	// ---- whether the parents they name are themselves concrete ----------

	TypeIdentity{
		// A bucket policy is a named singleton child: exactly one per
		// bucket, identified by the bucket's own name. Concrete whenever
		// the bucket is.
		Type:          "aws_s3_bucket_policy",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKETNAME",
		IdentityAttrs: []string{"id", "bucket"},
	},
	// The four S3 bucket children below (#19's second slice) are the same
	// named-singleton-child shape as the bucket policy: at most one per
	// bucket, imported by the bucket name, verified against the provider's
	// identity schemas (required import attribute: bucket; account_id and
	// region optional). The provider's documented import grammar also
	// accepts "BUCKETNAME,EXPECTED_BUCKET_OWNER", and each type's id
	// attribute carries that suffixed form when expected_bucket_owner is
	// set — a configuration the v0 subset's own fixture does not use.
	TypeIdentity{
		Type:          "aws_s3_bucket_versioning",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKETNAME",
		IdentityAttrs: []string{"id", "bucket"},
	},
	TypeIdentity{
		Type:          "aws_s3_bucket_public_access_block",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKETNAME",
		IdentityAttrs: []string{"id", "bucket"},
	},
	TypeIdentity{
		Type:          "aws_s3_bucket_server_side_encryption_configuration",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKETNAME",
		IdentityAttrs: []string{"id", "bucket"},
	},
	TypeIdentity{
		Type:          "aws_s3_bucket_lifecycle_configuration",
		Components:    []Component{attr("bucket")},
		ImportSyntax:  "BUCKETNAME",
		IdentityAttrs: []string{"id", "bucket"},
	},
	TypeIdentity{
		// Both halves are client-named in any realistic config, so this
		// resolves concrete even though it is structurally a composite.
		Type: "aws_iam_role_policy_attachment",
		Components: []Component{
			attr("role"),
			sep("/"),
			attr("policy_arn"),
		},
		ImportSyntax: "ROLENAME/POLICYARN",
		// The attachment's own id is provider-internal and is not the
		// import ID, so nothing may derive an identity from it.
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// #19's second slice. An inline role policy is the same
		// concrete-composite shape as the attachment above: both halves of
		// ROLENAME:POLICYNAME are client-chosen strings, so this resolves
		// concrete in any realistic config. Unlike the attachment, its id
		// attribute is exactly the import ID (role:name), so id may be
		// handed out as an identity source.
		Type: "aws_iam_role_policy",
		Components: []Component{
			attr("role"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "ROLENAME:POLICYNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// #19's second slice, via #20: the survey classes a record set as
		// client-named (name and type are), but the provider's import
		// grammar joins them to the parent zone's server-assigned Z-ID, so
		// this is wired as a composite through the aws_route53_zone marker
		// (flag F5 in live/SURVEY.md). The record's id equals the
		// import ID for plain records, but carries a _SETIDENTIFIER suffix
		// for weighted/latency sets, which these components deliberately do
		// not build — so nothing may derive an identity from id, the
		// aws_route standard of care. A record with set_identifier resolves
		// to an identity missing that suffix and fails visibly at import
		// rather than binding some other record.
		Type: "aws_route53_record",
		Components: []Component{
			attr("zone_id"),
			sep("_"),
			attr("name"),
			sep("_"),
			attr("type"),
		},
		ImportSyntax:  "ZONEID_NAME_TYPE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Parent-derived in practice: the route table ID is live.
		// Destination is whichever of the three destination arguments the
		// route uses.
		Type: "aws_route",
		Components: []Component{
			attr("route_table_id"),
			sep("_"),
			attr("destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id"),
		},
		ImportSyntax: "ROUTETABLEID_DESTINATION",
		// aws_route's id is a synthesized r-rtb-… value, not the import
		// ID; deriving another resource's identity from it would be wrong.
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Parent-derived: both halves are live IDs. An association may
		// attach a subnet or a gateway to the table, hence the preference
		// list.
		Type: "aws_route_table_association",
		// Neither half is an identity attribute: the provider identifies an
		// association by the rtbassoc- ID it assigns, and this entry builds
		// the documented import *string* instead. Saying so is what keeps
		// this type importing by that string while everything around it
		// moves to identity objects.
		Components: []Component{
			idlessAttr("subnet_id", "gateway_id"),
			sep("/"),
			idlessAttr("route_table_id"),
		},
		ImportSyntax: "SUBNETID/ROUTETABLEID",
		// The association's id is a server-assigned rtbassoc-… value,
		// which is deliberately *not* its import ID; listing it as an
		// identity attribute would hand out the wrong string.
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Parent-derived (#21): the target group's ARN is live, and the
		// target and port come from configuration. The provider's identity
		// schema requires target_group_arn and target_id and treats port as
		// optional context, but the import *string* is all three joined by
		// commas, and an attachment that sets no port cannot have one built
		// - which is what the "identity argument not set" error says, rather
		// than an import ID that silently addresses a different attachment.
		Type: "aws_lb_target_group_attachment",
		Components: []Component{
			attr("target_group_arn"),
			sep(","),
			attr("target_id"),
			sep(","),
			attr("port"),
		},
		ImportSyntax: "TARGETGROUPARN,TARGETID,PORT",
		// The attachment's id is the comma-joined composite the provider
		// synthesizes, not a value ELBv2 ever minted, and nothing refers to
		// an attachment anyway. Same standard of care as aws_route's
		// synthesized id: hand out nothing rather than something that
		// happens to look right.
		IdentityAttrs: nil,
	},

	// ---- Registry-ratified (#40, #44): first batch, Lambda -----------
	//
	// Every row below started as a tools/row-gen proposal built from
	// live/registry.json (the CloudFormation Registry's primaryIdentifier,
	// readOnlyProperties and createOnlyProperties for the type's mapped
	// CFN type) rather than from the provider's own identity schema. The
	// registry evidence is strong for whether an argument is
	// provider-assigned at all, and weak for whether the CFN type's own
	// notion of "identity" is the string this Terraform provider actually
	// imports by — CloudFormation and the provider model the same AWS API
	// independently, and the two "Reason" mismatches this batch found
	// (aws_lambda_alias, aws_lambda_layer_version_permission — recorded
	// below rather than in this table, precisely because they are not
	// here) were exactly that: the CFN type's own read-only field is not
	// what the provider's import syntax names. Every row that did land
	// here was cross-checked against the provider's documented import
	// section (or, where present, its own identity schema in
	// live/survey-full.json) rather than accepted on the registry's word
	// alone. Cohort estate: live/e2e/estates/lambda.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_lambda_alias: row-gen proposed server-assigned via the CFN
	//     registry's AliasArn (read-only in the registry). The provider
	//     disagrees: aws_lambda_alias has no identity schema in v6.58.0,
	//     and its documented import ID is "function_name/alias_name" — a
	//     composite of two arguments the configuration already sets, not
	//     an ARN the alias resource mints. AliasArn is CloudFormation's
	//     own read-only projection of those same two arguments, not a
	//     value this provider treats as an identity.
	//   - aws_lambda_layer_version_permission: row-gen proposed
	//     server-assigned via the registry's opaque "Id". The provider's
	//     documented import ID is "layer-arn,version-number" — again a
	//     composite the configuration already supplies (the layer's ARN
	//     and the version being granted permission on), not the
	//     registry's Id. Same failure shape as the alias above.
	//
	// Both would need a hand-chosen composite separator row-gen's own
	// rules already refuse to guess (issue #44); this batch chooses not to
	// hand-write one for either without a config-signal check first, so
	// both stay out rather than land half-verified.

	serverAssigned("aws_lambda_code_signing_config",
		"Lambda mints the code signing config's ARN at create time; the type has no name argument for a wrong guess to reach for.",
		"CODESIGNINGCONFIGARN", "arn", "id"),
	serverAssigned("aws_lambda_event_source_mapping",
		"Lambda mints the event source mapping's UUID at create time; the event_source_arn in configuration names what it reads from, not the mapping itself.",
		"UUID", "uuid", "id"),
	serverAssigned("aws_lambda_layer_version",
		"Lambda mints the layer version's ARN at create time, embedding a version number it assigns and increments itself; the layer_name argument names the family, not one immutable version of it.",
		"LAYERVERSIONARN", "arn", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[CapacityProviderName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed directly against the provider's own identity schema
		// (live/survey-full.json: required_for_import=[name]) and against
		// the documented import command, which sets id to the same name.
		Type:          "aws_lambda_capacity_provider",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[FunctionName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's own identity schema
		// (live/survey-full.json: required_for_import=[function_name]) and
		// against the documented legacy import command, which sets id to
		// the function name.
		Type:          "aws_lambda_function",
		Components:    []Component{attr("function_name")},
		ImportSyntax:  "FUNCTION_NAME",
		IdentityAttrs: []string{"id", "function_name"},
	},

	// ---- Registry-ratified (#40, #44): second batch, IAM and ECR (#26) --
	//
	// Same method as the first Lambda batch above: every row started as a
	// tools/row-gen proposal from live/registry.json, and every row that
	// landed here was independently cross-checked against the AWS
	// provider's documented import behaviour, not accepted on the
	// registry's classification alone. Cohort estate:
	// live/e2e/estates/iam-ecr. Issue #26 named two blocked-emulator types,
	// aws_ecr_repository and aws_iam_user, as unblocked by the pinned
	// floci image's IAM-tag and ECR fixes; both are ratified below.
	//
	// Rejected, and deliberately absent from this table — the same
	// registry-says-server-assigned-but-the-provider-disagrees shape the
	// Lambda batch's two rejections established, all three confirmed by
	// reading the provider's own Argument Reference, not just its Import
	// section:
	//
	//   - aws_iam_policy: row-gen proposed server-assigned via the
	//     registry's opaque "Id". The provider disagrees: its documented
	//     import ID is the policy's ARN, and the ARN embeds the `name` and
	//     `path` arguments the resource's own Argument Reference lists as
	//     configuration (name is optional — Terraform assigns a random one
	//     when omitted — but when set, it is what the ARN's final path
	//     segment literally is). "Id" is CloudFormation's own read-only
	//     projection of that same composite, not a value this provider
	//     mints independently. SURVEY.md already carries this type as
	//     client-named, account-derived (the same CloudContext mechanism
	//     aws_sns_topic uses); wiring it that way is follow-on work this
	//     batch does not attempt.
	//   - aws_iam_saml_provider: row-gen proposed server-assigned via the
	//     registry's "Arn" (read-only in the registry). The provider's
	//     documented import ID is that same ARN, but `name` is a *required*
	//     configuration argument with no generated fallback, and the ARN's
	//     final path segment is that name verbatim
	//     (arn:aws:iam::ACCOUNT:saml-provider/NAME). Same failure shape as
	//     aws_lambda_alias: a read-only CFN field that is really a
	//     composite of an argument already in configuration.
	//   - aws_iam_virtual_mfa_device: row-gen proposed server-assigned via
	//     the registry's "SerialNumber". The provider's own docs say the
	//     serial number *is* the ARN
	//     (arn:aws:iam::ACCOUNT:mfa/NAME), and NAME is the required
	//     `virtual_mfa_device_name` argument verbatim — the same composite
	//     shape as the SAML provider above. (The type also mints a secret,
	//     base_32_string_seed, that can never be read back after create —
	//     a second, independent reason it would need care beyond this
	//     batch's scope even had its identity checked out.)
	//   - aws_iam_access_key: row-gen proposed server-assigned via the
	//     registry's opaque "Id", and the classification itself is not in
	//     question — but this type is one of the three SURVEY.md's "rule
	//     excludes" permanently, not merely leaves unwired: an access key
	//     is a credential born server-side alongside a secret
	//     (SecretAccessKey) that can never be read again, forwarded to the
	//     lifecycle layer by the fork's own architecture rather than
	//     modeled as an ordinary resource. Admitting it here would reverse
	//     that standing decision, which is out of scope for a row-gen
	//     ratification batch.
	//
	// Deferred, identity confirmed correct, but not wired this batch:
	//
	//   - aws_iam_group: row-gen correctly proposed client-named via `name`
	//     (confirmed against the provider's documented import, which sets
	//     id to the group name verbatim). live/survey.json — the curated
	//     68 this survey measures — already carries this type, and its own
	//     signal says untaggable (IAM has no TagGroup API). Admitting it
	//     would move it into
	//     tools/survey-gen/limitations_test.go's TestLimitationsDocAgainstSurvey
	//     derived set (admitted ∩ curated-68 ∩ untaggable), which requires
	//     live/LIMITATIONS.md's "Untaggable types cannot be removed by the
	//     sweep" entry to name it — an edit to the curated-68 apparatus
	//     this batch's mandate leaves untouched (unlike
	//     aws_lambda_layer_version, which sidesteps the same doc by being
	//     outside the curated 68 entirely, aws_iam_group cannot dodge it
	//     that way). Left for a batch prepared to move that doc.
	//
	// Not this batch's to decide, same as aws_lambda_permission in the
	// first batch: aws_iam_group_policy, aws_iam_role_policy and
	// aws_iam_user_policy are all needs-hand-separator (composite
	// PolicyName+GroupName/RoleName/UserName primary identifiers with no
	// separator in any schema); aws_iam_role_policy is wired already, via
	// this table's own #19 slice above, not via row-gen. aws_iam_role
	// itself was row-gen's eighth IAM proposal and is skipped here for the
	// same reason: already wired via this table's own #19 slice, not via
	// the registry. The remaining row-gen output for both services
	// (aws_ecr_lifecycle_policy, aws_ecr_pull_through_cache_rule,
	// aws_ecr_pull_time_update_exclusion,
	// aws_ecr_repository_creation_template, aws_ecr_repository_policy,
	// aws_iam_role_policy_attachment, aws_iam_server_certificate) is
	// evidence-only per #44's own non-goals — no pastable row was ever
	// generated for any of them.

	serverAssigned("aws_ecr_registry_policy",
		"the registry policy is a singleton per AWS account: its identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument — the resource's only argument, policy, sets the document content, not an identifying name.",
		"REGISTRYID", "registry_id"),
	serverAssigned("aws_ecr_registry_scanning_configuration",
		"the scanning configuration is a singleton per AWS account: its identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument.",
		"REGISTRYID", "registry_id"),
	serverAssigned("aws_ecr_replication_configuration",
		"the replication configuration is a singleton per AWS account: its identity is the account's own ECR registry ID, which pre-exists the resource and is never supplied by a configuration argument.",
		"REGISTRYID", "registry_id"),
	serverAssigned("aws_iam_service_linked_role",
		"IAM computes the service-linked role's name from aws_service_name using its own internal per-service convention (for example elasticbeanstalk.amazonaws.com becomes AWSServiceRoleForElasticBeanstalk), not a string transform of any configured argument; the provider's own docs say the role name is not an argument you provide. The documented import ID is the role's ARN, not the bare RoleName the registry reports as primaryIdentifier.",
		"ARN", "arn", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[RepositoryName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed directly against the provider's own identity schema
		// (live/survey-full.json: required=[name]) and against the
		// documented import command, which sets id to the repository name
		// verbatim. Issue #26's first named type: floci's ecr:CreateRepository
		// no longer needs a Docker daemon, so the earlier blocked-emulator
		// note no longer holds.
		Type:          "aws_ecr_repository",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; id is the registry_id, not the name
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[InstanceProfileName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's own identity schema
		// (live/survey-full.json: required=[name]) and against the
		// documented import command, which sets id to the instance
		// profile's name verbatim.
		Type:          "aws_iam_instance_profile",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[UserName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed directly against the provider's own identity schema
		// (live/survey-full.json: required=[name]) and against the
		// documented import command, which sets id to the user name
		// verbatim. Issue #26's second named type: floci's iam:GetUser now
		// returns Tags, so the earlier blocked-emulator note no longer
		// holds.
		Type:          "aws_iam_user",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	// ---- Registry-ratified (#40, #44): third batch, messaging (SQS, SNS
	// ---- beyond the already-admitted aws_sns_topic, CloudWatch, and
	// ---- EventBridge/Events) -------------------------------------------
	//
	// Same pipeline as the Lambda batch above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour (its "Import" section,
	// fetched from the provider's own website/docs/r/ source at the pinned
	// v6.58.0 tag) rather than accepted on the registry's word alone. Most
	// of the Logs and Events resource family carries no cfn_type in
	// live/mapping.json at all — aws_cloudwatch_log_stream,
	// aws_cloudwatch_log_metric_filter, aws_cloudwatch_log_subscription_filter,
	// aws_cloudwatch_event_bus, aws_cloudwatch_event_target and the rest —
	// so row-gen proposes nothing for them; this batch's Logs and Events
	// slice is only the two types the registry does map,
	// aws_cloudwatch_log_group (already admitted, client-named cohort
	// above) and aws_cloudwatch_event_rule (proposed, rejected below).
	// Cohort estate: live/e2e/estates/messaging.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_cloudwatch_alarm_mute_rule: row-gen proposed server-assigned
	//     via the registry's Arn (read-only; Name is createOnlyProperties
	//     only). The provider disagrees: both its documented import
	//     command and its identity schema's sole required_for_import
	//     attribute are the rule's own name argument, already in
	//     configuration — a name-composed ID the registry's own read-only
	//     field does not name. Same failure shape as the Lambda batch's
	//     aws_lambda_alias.
	//   - aws_cloudwatch_event_rule: row-gen proposed server-assigned via
	//     the registry's Arn, AWS::Events::Rule's sole readOnlyProperty.
	//     The provider disagrees: the documented import ID is
	//     "event_bus_name/rule_name", a composite of two configured
	//     arguments (event_bus_name silently defaulting to the account's
	//     default bus when omitted from configuration) — live/SURVEY.md's
	//     own curated-68 row already named this exact grammar. Wiring it
	//     needs a component this table's vocabulary does not have yet (a
	//     literal fallback for an omitted argument, not just a separator),
	//     so it stays a "needs hand separator" case rather than a guess
	//     this batch writes blind, the same stance as the Lambda batch's
	//     two rejections.
	//
	// Deferred, for a reason that is not about identity at all:
	//
	//   - aws_sns_topic_subscription: row-gen proposed server-assigned via
	//     the registry's Arn, and the provider agrees — SNS mints the
	//     subscription's own ARN (the parent topic's ARN plus a UUID) only
	//     once the subscription confirms, and live/SURVEY.md's own
	//     curated-68 row already reaches the same "ready" verdict by hand.
	//     It is left out of this table anyway: the type carries no tags
	//     argument, it is one of live/survey.json's curated 68 rows, and
	//     admitting an untaggable curated-68 type obligates
	//     live/LIMITATIONS.md's "Untaggable types cannot be removed by the
	//     sweep" entry (tools/survey-gen/limitations_test.go derives that
	//     entry's roster mechanically from the survey intersected with the
	//     admission table). Editing live/LIMITATIONS.md or the curated-68
	//     apparatus is out of scope for this batch — see
	//     live/e2e/estates/messaging/README.md and issue #54. The Lambda
	//     batch's aws_lambda_layer_version did not hit this wall only
	//     because it sits outside the curated 68, where
	//     internal/live/stamp/stamp_test.go's untaggableOutsideCuratedSurvey
	//     list, not the doc, is where its untaggability is pinned.

	TypeIdentity{
		// registry.json: primaryIdentifier=[AlarmName], in
		// createOnlyProperties and not in readOnlyProperties — client-named,
		// and row-gen proposed it correctly the first time (no CFN/TF
		// mismatch here, unlike the two rejections above). Confirmed against
		// the provider's documented import command (terraform import
		// aws_cloudwatch_composite_alarm.example example-alarm) and its own
		// Attribute Reference, which states id is "equivalent to its
		// alarm_name".
		Type:          "aws_cloudwatch_composite_alarm",
		Components:    []Component{attr("alarm_name")},
		ImportSyntax:  "ALARM_NAME",
		IdentityAttrs: []string{"id", "alarm_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DashboardName], in
		// createOnlyProperties and not in readOnlyProperties — client-named,
		// proposed correctly. Confirmed against the provider's documented
		// import command (terraform import aws_cloudwatch_dashboard.example
		// example-dashboard). The provider's own docs export only
		// dashboard_arn, not id, so id is not claimed as an identity source
		// here — the same standard of care aws_route's synthesized id gets.
		Type:          "aws_cloudwatch_dashboard",
		Components:    []Component{attr("dashboard_name")},
		ImportSyntax:  "DASHBOARD_NAME",
		IdentityAttrs: []string{"dashboard_name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, proposed correctly.
		// Confirmed against the provider's documented import command
		// (terraform import aws_cloudwatch_metric_stream.example
		// example-stream). The provider's own docs export arn,
		// creation_date, last_update_date and state, not id, so id is not
		// claimed as an identity source here either.
		Type:          "aws_cloudwatch_metric_stream",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed aws_sns_topic_policy server-assigned via the
		// registry's opaque, undocumented "Id" (AWS::SNS::TopicPolicy's
		// primaryIdentifier) — rejected as a proposal, the same failure
		// shape as the two rejections above, but the correction needs no
		// separator guess: the provider's documented import command
		// (terraform import aws_sns_topic_policy.default
		// arn:aws:sns:us-west-2:123456789012:my-topic) is exactly the SNS
		// topic's own ARN, which this resource's sole reference argument,
		// "arn", already carries in any realistic config. The same
		// named-singleton-child shape as aws_s3_bucket_policy above, keyed
		// on the parent's "arn" argument rather than a bucket's "bucket".
		// Concrete whenever the topic is.
		Type:          "aws_sns_topic_policy",
		Components:    []Component{attr("arn")},
		ImportSyntax:  "TOPICARN",
		IdentityAttrs: []string{"arn"},
	},
	TypeIdentity{
		// Named-singleton child of the queue, the same shape as
		// aws_sns_topic_policy just above: row-gen proposed server-assigned
		// via an opaque, undocumented registry "Id"; the provider's real,
		// documented import command (terraform import
		// aws_sqs_queue_policy.test
		// https://queue.amazonaws.com/123456789012/myqueue) is the queue's
		// own url, this resource's sole reference argument alongside
		// policy. The provider's docs export no additional attributes for
		// this type at all ("This resource exports no additional
		// attributes"), so no alias beyond the argument itself is claimed
		// here — the same standard of care aws_route's synthesized id gets.
		Type:          "aws_sqs_queue_policy",
		Components:    []Component{attr("queue_url")},
		ImportSyntax:  "QUEUEURL",
		IdentityAttrs: []string{"queue_url"},
	},

	TypeIdentity{
		// row-gen proposed aws_sqs_queue server-assigned via its QueueUrl
		// and Arn (both registry readOnlyProperties) — right in spirit
		// (SQS mints the URL; FifoQueue and QueueName alone do not
		// reconstruct it) but the flat serverAssigned() template undersells
		// it: this is the same account-derived shape as aws_sns_topic
		// above, not an opaque ID. The provider's own identity schema
		// requires exactly one field, url, and its documented import
		// command (terraform import aws_sqs_queue.example
		// https://queue.amazonaws.com/80398EXAMPLE/MyQueue) confirms the
		// value is a URL built from the queue's name plus the account and
		// region of the cloud the run is against — the exact
		// https://sqs.REGION.amazonaws.com/ACCOUNT/NAME grammar this
		// table's own account-derived section comment named for this type
		// before this batch ratified it. What kept this type out before
		// was a floci gap (choudoufu#26), never the identity — see
		// live/e2e/estates/messaging/README.md.
		Type: "aws_sqs_queue",
		Components: []Component{
			inAttr("url", sep("https://sqs.")),
			inAttr("url", cloud(CloudRegion)),
			inAttr("url", sep(".amazonaws.com/")),
			inAttr("url", cloud(CloudAccountID)),
			inAttr("url", sep("/")),
			inAttr("url", attr("name")),
		},
		ImportSyntax:  "https://sqs.REGION.amazonaws.com/ACCOUNT/NAME",
		IdentityAttrs: []string{"url", "id"},
	},

	// ---- Registry-ratified (#40, #44): fourth batch, API Gateway v1 and v2
	// ---- (issue #65) -----------------------------------------------------
	//
	// Same pipeline as the earlier three batches: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour. Two extensions beyond
	// what row-gen itself proposes were needed for this service, because
	// row-gen's own rule refuses any primaryIdentifier with more than one
	// part ("needs hand separator", issue #44 non-goal 3):
	//
	//   - live/import-grammar.json (tools/importdocs-gen, scraped from the
	//     pinned v6.58.0 provider docs) supplies the documented separator and
	//     the argument names the provider's own Import section names, for
	//     every composite ratified below.
	//   - Knowing the separator is not enough by itself: several of API
	//     Gateway's composite import IDs name a segment that is the child
	//     resource's own server-minted id (an AuthorizerId, a DeploymentId, a
	//     DocumentationPartId, a RequestValidatorId, the ResourceId the
	//     provider's own identity schema calls "id") rather than a
	//     configuration argument. Nothing in configuration can supply that
	//     segment, so a Components row for it would be a guess dressed up as
	//     a separator, which is exactly what issue #44 non-goal 3 forbids.
	//     Each such type was checked against the provider's Argument
	//     Reference — and, where available, live/survey-full.json's identity
	//     schema or the resource's own source for where SetId points — before
	//     landing either in the rejections below or, where every segment
	//     really is a configuration argument, in the ratified rows.
	//
	// 25 ApiGateway and 13 ApiGatewayV2 types were in row-gen's scope; 16 and
	// 5 respectively ratify here, the rest rejected (a composite needs a
	// server-minted segment) or deferred (a method/response property-child
	// per live/mapping.json's fold — see below). Cohort estate:
	// live/e2e/estates/apigateway; see that cohort's README for the full
	// floci verification this comment's floci notes summarize.
	//
	// Rejected, and deliberately absent from this table — every one because
	// the documented composite import ID names a segment that is the child's
	// own server-minted id, confirmed against the provider's Argument
	// Reference (v6.58.0) rather than against row-gen's registry evidence
	// alone:
	//
	//   - aws_api_gateway_authorizer: REST-API-ID/AUTHORIZER-ID. The
	//     resource's only arguments are name and rest_api_id; AuthorizerId is
	//     minted by the provider and appears nowhere in configuration.
	//   - aws_api_gateway_deployment: REST-API-ID/DEPLOYMENT-ID. The
	//     resource's only required argument is rest_api_id; DeploymentId is
	//     minted at create time.
	//   - aws_api_gateway_documentation_part: REST-API-ID/DOC-PART-ID. The
	//     resource's arguments are location, properties and rest_api_id;
	//     DocumentationPartId is minted at create time.
	//   - aws_api_gateway_request_validator: REST-API-ID/REQUEST-VALIDATOR-ID.
	//     The resource's arguments are name and rest_api_id; the validator's
	//     id is a value the provider assigns independently of name.
	//   - aws_api_gateway_resource: REST-API-ID/RESOURCE-ID.
	//     live/survey-full.json's identity schema names the requirement
	//     directly: required_for_import=[id, rest_api_id] — the resource's
	//     own id, not an argument.
	//   - aws_apigatewayv2_api_mapping: API-MAPPING-ID/DOMAIN-NAME. The
	//     resource's arguments are api_id, domain_name, stage and the
	//     optional api_mapping_key; ApiMappingId is minted at create time and
	//     is not api_mapping_key's value.
	//   - aws_apigatewayv2_authorizer: API-ID/AUTHORIZER-ID. The resource's
	//     arguments are api_id, authorizer_type and name; AuthorizerId is
	//     minted at create time.
	//   - aws_apigatewayv2_deployment: API-ID/DEPLOYMENT-ID. The resource's
	//     only required argument is api_id; DeploymentId is minted at create
	//     time.
	//   - aws_apigatewayv2_integration: API-ID/INTEGRATION-ID. The resource's
	//     required arguments are api_id and integration_type; IntegrationId
	//     is minted at create time.
	//   - aws_apigatewayv2_integration_response: API-ID/INTEGRATION-ID/
	//     INTEGRATION-RESPONSE-ID. Confirmed against the provider's own
	//     source (internal/service/apigatewayv2/integration_response.go):
	//     resourceIntegrationResponseCreate calls
	//     d.SetId(aws.ToString(output.IntegrationResponseId)), a value the
	//     API mints independently of the integration_response_key argument.
	//   - aws_apigatewayv2_model: API-ID/MODEL-ID. Confirmed against the
	//     provider's source (internal/service/apigatewayv2/model.go):
	//     resourceModelCreate calls d.SetId(aws.ToString(output.ModelId)), a
	//     value the API mints independently of the name argument.
	//   - aws_apigatewayv2_route: API-ID/ROUTE-ID. live/survey-full.json's
	//     identity schema names the requirement directly:
	//     required_for_import=[api_id, id] — the route's own id, not an
	//     argument route_key resolves to.
	//   - aws_apigatewayv2_route_response: API-ID/ROUTE-ID/
	//     ROUTE-RESPONSE-ID. Confirmed against the provider's source
	//     (internal/service/apigatewayv2/route_response.go):
	//     resourceRouteResponseCreate calls
	//     d.SetId(aws.ToString(output.RouteResponseId)), a value the API
	//     mints independently of the route_response_key argument.
	//
	// Deferred as method/response property-children, per live/mapping.json's
	// fold (row-gen's own output marks each
	// "(property-child of AWS::ApiGateway::Method)" or "...Stage", "no
	// pastable row" — no independent cfn_type of its own), not for any
	// identity weakness: each one's identity is in fact fully composable from
	// real configuration arguments alone. aws_api_gateway_method,
	// _integration, _integration_response and _method_response all require
	// exactly rest_api_id, resource_id and http_method (the latter two also
	// status_code), confirmed against live/survey-full.json's identity
	// schema — none of the four is the type's own server-minted id, unlike
	// the rejections above. aws_api_gateway_method_settings's identity
	// (rest_api_id, stage_name, method_path) is confirmed the same way
	// against live/import-grammar.json's scraped Import section instead,
	// that type predating the provider's identity-schema mechanism.
	// aws_api_gateway_method itself is not a fold (it is its own CFN
	// resource, AWS::ApiGateway::Method, merely with a composite
	// primaryIdentifier) and ratifies below; its three literal
	// property-children do not, because admitting a property-child needs a
	// parent-derived admission mechanism this table does not have yet — the
	// same gap aws_prometheus_alert_manager_definition and its APS siblings
	// are waiting on upstream. A future batch that builds that mechanism can
	// pick these three straight up; the identity work is already done here:
	//
	//   - aws_api_gateway_integration, aws_api_gateway_integration_response,
	//     aws_api_gateway_method_response (fold into AWS::ApiGateway::Method)
	//   - aws_api_gateway_method_settings (fold into AWS::ApiGateway::Stage)

	serverAssigned("aws_api_gateway_account",
		"the account settings resource is a singleton per AWS account: its identity is the caller's own AWS account ID, which pre-exists the resource and is never supplied by a configuration argument — the resource's only argument, cloudwatch_role_arn, does not identify it. Confirmed against the provider's own source (internal/service/apigateway/account.go): the Create method sets the id to r.Meta().AccountID(ctx), not to anything the configuration names.",
		"ACCOUNT_ID", "id"),
	serverAssigned("aws_api_gateway_api_key",
		"API Gateway mints the key's own id at create time; name is client-chosen but is not unique and is not what the provider imports by. Ratified despite a floci gap: reading an existing key back crashes the provider (a nil-pointer panic in resourceAPIKeyRead) rather than erroring gracefully — see live/e2e/estates/apigateway/README.md.",
		"APIKEYID", "id"),
	serverAssigned("aws_api_gateway_client_certificate",
		"API Gateway mints the client certificate's id at create time; every argument (description, region, tags) is optional and none of them identifies it. floci returns 406 for GenerateClientCertificate — not implemented, not evidence against the identity.",
		"CLIENTCERTIFICATEID", "id"),
	serverAssigned("aws_api_gateway_domain_name_access_association",
		"API Gateway mints the association's own ARN at create time; confirmed against the provider's own Identity Schema (required: arn) — the row-gen proposal here already matched the provider's documented behaviour with no correction needed.",
		"ARN", "arn", "id"),
	serverAssigned("aws_api_gateway_rest_api",
		"API Gateway mints the REST API's id at create time; name is client-chosen but is not unique and is not what the provider imports by. Re-verified against the pinned floci image (issue #65): the old blocked-emulator note undersold it — CreateRestApi and GetRestApi both work, and a terraform import against a floci-created REST API round-trips cleanly (confirmed by hand), but the provider's post-create availability waiter still spins forever because floci's DescribeRestApi never reports the AVAILABLE status the waiter polls for. That is a create-path gap, not a read/import gap, and this table only needs the latter — see live/e2e/estates/apigateway/README.md.",
		"RESTAPIID", "id"),
	serverAssigned("aws_api_gateway_usage_plan",
		"API Gateway mints the usage plan's id at create time; name is client-chosen but is not what the provider imports by. floci's own GetUsagePlan is broken (routes to a stray S3 NoSuchBucket error instead of the plan), so a terraform-managed usage plan cannot be read back against this emulator at all — a floci gap, not evidence against the identity; see live/e2e/estates/apigateway/README.md.",
		"ID", "id"),
	serverAssigned("aws_api_gateway_vpc_link",
		"API Gateway mints the VPC link's id at create time; name is client-chosen but is not what the provider imports by, the same shape as aws_lb above. floci returns 406 for CreateVpcLink — not implemented, not evidence against the identity.",
		"VPCLINKID", "id"),
	serverAssigned("aws_apigatewayv2_api",
		"API Gateway mints the v2 API's id at create time; confirmed against the provider's own Identity Schema (required: id) — the row-gen proposal here already matched the provider's documented behaviour. Confirmed against floci by hand: create, get and a terraform import all round-trip cleanly.",
		"APIID", "id"),
	serverAssigned("aws_apigatewayv2_routing_rule",
		"API Gateway mints the routing rule's own ARN at create time; the provider's docs list routing_rule_arn only in the Attribute Reference, not the Argument Reference, confirming it is computed rather than configurable. Untested against floci: creating one needs a working aws_apigatewayv2_domain_name first, and floci's CreateDomainName itself misroutes (see that type's note below) — the identity is sound regardless, being a property of the provider, not the emulator.",
		"ROUTINGRULEARN", "arn", "id"),
	serverAssigned("aws_apigatewayv2_vpc_link",
		"API Gateway mints the v2 VPC link's id at create time; name is client-chosen but is not what the provider imports by. Confirmed working against floci by hand (CreateVpcLink succeeds, unlike its v1 counterpart above).",
		"VPCLINKID", "id"),

	TypeIdentity{
		// row-gen marked this evidence-only because the registry's own
		// primaryIdentifier=[DomainName] argument name was GUESSED (not
		// backed by a provider identity schema or the carve seed) — not
		// because the identity itself is in doubt. Confirmed directly
		// against the provider's Argument Reference: domain_name is the
		// resource's sole required argument, and the documented import
		// command (terraform import aws_api_gateway_domain_name.example
		// dev.example.com) uses that same value verbatim. floci misroutes
		// CreateDomainName (a stray S3-shaped 400), so this type is untested
		// end-to-end against the pinned image; see
		// live/e2e/estates/apigateway/README.md.
		Type:          "aws_api_gateway_domain_name",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{"domain_name"},
	},
	TypeIdentity{
		// Same correction as aws_api_gateway_domain_name just above, and the
		// same floci gap (CreateDomainName misroutes) for the same reason —
		// domain_name_configuration's own certificate_arn dependency is not
		// what blocks it here, the emulator's routing is.
		Type:          "aws_apigatewayv2_domain_name",
		Components:    []Component{attr("domain_name")},
		ImportSyntax:  "DOMAIN_NAME",
		IdentityAttrs: []string{"domain_name"},
	},
	TypeIdentity{
		// A named-singleton-child of the REST API, the same shape as
		// aws_s3_bucket_policy and aws_sns_topic_policy above: at most one
		// per REST API, imported by the API's own id, which this resource's
		// sole reference argument, rest_api_id, already carries. row-gen
		// marked this "(property-child of AWS::ApiGateway::RestApi)
		// [evidence-only]" the same as the four deferred property-children
		// above, but unlike those this one needs no separator decision at
		// all — a single component, not a composite — so it ratifies here on
		// the same standard the earlier batches' singleton-child policies
		// used. Confirmed against the provider's Argument Reference (policy,
		// rest_api_id) and the documented import command (terraform import
		// aws_api_gateway_rest_api_policy.example 12345abcde).
		Type:          "aws_api_gateway_rest_api_policy",
		Components:    []Component{attr("rest_api_id")},
		ImportSyntax:  "REST_API_ID",
		IdentityAttrs: []string{"id", "rest_api_id"},
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: rest_api_id
		// and name are both required arguments, and the documented import
		// command (terraform import aws_api_gateway_model.example
		// 12345abcde/example) joins them REST-API-ID/NAME. Confirmed against
		// floci by hand: create, get and a terraform import all round-trip
		// cleanly with zero plan diff.
		Type: "aws_api_gateway_model",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("name"),
		},
		ImportSyntax:  "REST-API-ID/NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: rest_api_id
		// and stage_name are both required arguments (deployment_id is a
		// third, unrelated to identity), and the documented import command
		// (terraform import aws_api_gateway_stage.example 12345abcde/example)
		// joins them REST-API-ID/STAGE-NAME. Confirmed against floci by
		// hand: create, get and a terraform import all round-trip cleanly
		// with zero plan diff — note the stage's own id attribute is an
		// unrelated internal "ags-..." value, which is why it is not listed
		// as an identity source here, the same standard of care aws_route's
		// synthesized id gets.
		Type: "aws_api_gateway_stage",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("stage_name"),
		},
		ImportSyntax:  "REST-API-ID/STAGE-NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: rest_api_id
		// and version are both required arguments, and the documented import
		// command (terraform import aws_api_gateway_documentation_version.example
		// 5i4e1ko720/example-version) joins them REST-API-ID/VERSION. floci
		// returns 406 for CreateDocumentationVersion — not implemented, so
		// this type is untested end-to-end against the pinned image; see
		// live/e2e/estates/apigateway/README.md.
		Type: "aws_api_gateway_documentation_version",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("version"),
		},
		ImportSyntax:  "REST-API-ID/VERSION",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen marked this evidence-only because the registry's own
		// primaryIdentifier=[Id] is opaque and read-only — the same
		// registry-says-server-assigned-but-the-provider-disagrees shape as
		// aws_sns_topic_policy above. The provider's real, documented import
		// command (terraform import aws_api_gateway_gateway_response.example
		// 12345abcde/UNAUTHORIZED) is REST-API-ID/RESPONSE-TYPE, both of
		// which are the resource's own required arguments (rest_api_id,
		// response_type). floci's PutGatewayResponse misroutes (a stray
		// S3-shaped 404), so this type is untested end-to-end against the
		// pinned image; see live/e2e/estates/apigateway/README.md.
		Type: "aws_api_gateway_gateway_response",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("response_type"),
		},
		ImportSyntax:  "REST-API-ID/RESPONSE-TYPE",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: domain_name
		// and the optional base_path are both configuration arguments (base_path
		// defaults to "" for the root path), and the documented import
		// examples (terraform import aws_api_gateway_base_path_mapping.example
		// example.com/ and .../example.com/base-path) join them
		// DOMAIN-NAME/BASE-PATH — note rest_api_id, though required to
		// create the mapping, is not part of the identity at all. This type
		// sits behind the same floci domain_name gap as
		// aws_api_gateway_domain_name above, so it is untested end-to-end
		// against the pinned image.
		Type: "aws_api_gateway_base_path_mapping",
		Components: []Component{
			attr("domain_name"),
			sep("/"),
			attr("base_path"),
		},
		ImportSyntax:  "DOMAIN-NAME/BASE-PATH",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: usage_plan_id
		// and key_id are both required arguments, and confirmed against the
		// provider's own source (internal/service/apigateway/usage_plan_key.go):
		// the import function splits the documented
		// USAGE-PLAN-ID/USAGE-PLAN-KEY-ID string and calls
		// d.Set(names.AttrKeyID, usagePlanKeyId) — the second segment is
		// literally the key_id argument's value, not a separate id the
		// resource mints. Confirmed against floci by hand: create, get and a
		// terraform import all round-trip cleanly with zero plan diff, even
		// though the parent aws_api_gateway_usage_plan cannot itself be read
		// back through this same emulator (see that type's note above) —
		// GetUsagePlanKey and GetUsagePlan are independent floci code paths.
		Type: "aws_api_gateway_usage_plan_key",
		Components: []Component{
			attr("usage_plan_id"),
			sep("/"),
			attr("key_id"),
		},
		ImportSyntax:  "USAGE-PLAN-ID/USAGE-PLAN-KEY-ID",
		IdentityAttrs: []string{"id", "key_id"},
	},
	TypeIdentity{
		// The one composite in this batch whose primaryIdentifier really is
		// three configuration arguments end to end, confirmed directly
		// against live/survey-full.json's identity schema
		// (required_for_import=[http_method, resource_id, rest_api_id] —
		// none of the three is the method's own id, because a method has no
		// id the provider mints; it is identified entirely by the three
		// arguments that address it). The documented import command
		// (terraform import aws_api_gateway_method.example
		// 12345abcde/67890fghij/GET) joins them
		// REST-API-ID/RESOURCE-ID/HTTP-METHOD. Confirmed working against
		// floci by hand (PutMethod via the raw API), though not
		// import-tested end to end because it needs the same rest_api chain
		// as aws_api_gateway_resource, which this table rejects above.
		Type: "aws_api_gateway_method",
		Components: []Component{
			attr("rest_api_id"),
			sep("/"),
			attr("resource_id"),
			sep("/"),
			attr("http_method"),
		},
		ImportSyntax:  "REST-API-ID/RESOURCE-ID/HTTP-METHOD",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Confirmed against the provider's Argument Reference: api_id and
		// name (the v2 stage's client-chosen name — this type's argument is
		// literally called "name", not "stage_name" as in the v1 type above)
		// are both required arguments, and the documented import command
		// (terraform import aws_apigatewayv2_stage.example
		// aabbccddee/example-stage) joins them API-ID/STAGE-NAME. Confirmed
		// against floci by hand: creating a v2 stage through terraform
		// succeeds cleanly (unlike the v1 rest_api chain above, v2's own API
		// and stage create paths have no waiter gap).
		Type: "aws_apigatewayv2_stage",
		Components: []Component{
			attr("api_id"),
			sep("/"),
			attr("name"),
		},
		ImportSyntax:  "API-ID/STAGE-NAME",
		IdentityAttrs: nil,
	},
)

func buildTable(entries ...TypeIdentity) map[string]TypeIdentity {
	m := make(map[string]TypeIdentity, len(entries))
	for _, e := range entries {
		m[e.Type] = e
	}
	return m
}

// LookupType returns the identity knowledge for a resource type, and
// whether the type is in the table at all.
func LookupType(typeName string) (TypeIdentity, bool) {
	e, ok := DefaultTable[typeName]
	return e, ok
}

// identityAttrFor is the identity attribute one component supplies for one
// instance, given which of its alternative arguments the configuration
// actually set. The empty string means it supplies none.
func (c Component) identityAttrFor(argName string) string {
	if c.IdentityAttr != SameNameIdentity {
		return c.IdentityAttr
	}
	// The rule only means anything for a component that read an argument;
	// a literal or a cloud value has no name of its own to take.
	return argName
}

// SuppliesIdentityAttr reports whether some component of this entry can
// supply the named identity attribute, taking the same-name rule over each
// component's whole alternative list.
//
// It is the type-level question, so it is answered over every alternative:
// aws_route can supply destination_cidr_block or destination_ipv6_cidr_block
// or destination_prefix_list_id, and which one a given route supplies is a
// per-instance answer [Resolve] makes.
func (t TypeIdentity) SuppliesIdentityAttr(name string) bool {
	for _, c := range t.Components {
		if c.IdentityAttr == "" {
			continue
		}
		if c.IdentityAttr != SameNameIdentity {
			if c.IdentityAttr == name {
				return true
			}
			continue
		}
		for _, a := range c.Attrs {
			if a == name {
				return true
			}
		}
	}
	return false
}

// ComposesIdentity reports whether this entry can build a whole identity
// object: every attribute the provider requires for import is supplied by
// some component.
//
// This is the bar for importing by identity rather than by string. It is
// deliberately about the *required* attributes only - the optional ones are
// context the provider fills in itself (account_id, region) or alternatives
// it needs no particular one of - and it is deliberately checked against a
// real identity schema rather than asserted here, because the whole point of
// the exercise is that the provider is the authority on what identifies its
// own resources.
func (t TypeIdentity) ComposesIdentity(required []string) bool {
	if t.ServerAssigned || len(required) == 0 {
		return false
	}
	for _, name := range required {
		if !t.SuppliesIdentityAttr(name) {
			return false
		}
	}
	return true
}

// AdmittedTypes lists every resource type the v0 table covers, sorted.
func AdmittedTypes() []string {
	out := make([]string, 0, len(DefaultTable))
	for t := range DefaultTable {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (t TypeIdentity) hasIdentityAttr(name string) bool {
	for _, a := range t.IdentityAttrs {
		if a == name {
			return true
		}
	}
	return false
}
