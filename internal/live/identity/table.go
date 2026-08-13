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

	// RecordBacked is true when this type's identity is not observed from
	// the cloud at all: the type is one of GitHub issue #73's
	// RECORD_ADMITTED logical types (internal/live/lint), whose whole
	// existence is a persisted micro-state record. Instances of such a
	// type would always classify [ClassRecordBacked], whatever their
	// arguments say, the same way ServerAssigned instances always classify
	// ClassNeedsDiscovery above.
	//
	// Staged and currently inert: no entry in [DefaultTable] sets this
	// field yet, because a RECORD_ADMITTED type is refused by lint before
	// it ever reaches this table, and [SynthesizeTypeIdentity] never
	// produces it either. It exists so the projection work #73 stages next
	// - hydrating a record-backed instance without a cloud read - is an
	// additive change to this struct's callers rather than a breaking one.
	RecordBacked bool

	// Components build the import identity by concatenation, in order.
	// Required unless ServerAssigned or RecordBacked.
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
	TypeIdentity{
		// aws_iam_group: this batch's own deferral note above named it
		// correctly the first time — client-named via name, no identity
		// schema in v6.58.0, the documented import command (terraform
		// import aws_iam_group.developers developers) sets id to the group
		// name verbatim — and deferred it anyway because admitting an
		// untaggable curated-68 type obligated live/LIMITATIONS.md's
		// "Untaggable types" entry, which this batch's mandate left
		// untouched. #54 regeneralized that entry's derivation past the
		// curated 68 (tools/survey-gen/untaggable_render.go), so the ECS/EKS
		// batch (#65) ratifies the deferral here rather than opening a
		// second cohort for one already-settled type.
		Type:          "aws_iam_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "GROUP_NAME",
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

	// ---- Registry-ratified (#40, #44): fourth batch, EC2 core (instances,
	// ---- EBS, ENI; issue #65) -------------------------------------------
	//
	// Same pipeline as the three batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour at the pinned v6.58.0
	// tag (its "Import" section and, where the provider has one, its own
	// identity schema) rather than accepted on the registry's word alone.
	// Scope is "instances and their periphery" — the slice issue #65 itself
	// names "EC2 core (instances, EBS, ENI)" — not the full 114-type EC2
	// registry service tools/row-gen enumerates; the VPC/Transit
	// Gateway/VPN/Client VPN/IPAM/Verified Access/route-server/NAT-gateway
	// families that make up the rest of that count are a future batch's
	// scope, not this one's. Cohort estate: live/e2e/estates/ec2-core.
	//
	// aws_instance is this batch's headline type: the repo's long-standing
	// canonical unadmitted example. live/e2e/limits/unadmitted-type and
	// live/LIMITATIONS.md's matching entry swap to aws_nat_gateway — a real,
	// non-logical, server-assigned EC2 type still in live/SURVEY.md's
	// curated 68, deliberately left out of this batch's own scope below and
	// out of every batch issue #65 names next, so it stays a stable example
	// rather than one this same wave of ratification would immediately have
	// to re-swap. See that fixture's own comment for the rest of the
	// account.
	//
	// Rejected, and deliberately absent from this table: none. Every
	// pastable proposal row-gen made in this batch's instances/EBS/ENI
	// slice checked out against the provider's real import behaviour — a
	// first for a registry-ratified batch, and worth naming precisely
	// because the other three batches all found at least one CFN-says-one-
	// thing-provider-says-another mismatch.
	//
	// Out of scope for this batch, not rejected on the evidence:
	//
	//   - aws_ec2_instance_connect_endpoint: a real, server-assigned,
	//     cleanly-proposed type (row-gen: primary identifier Id, read-only
	//     and not create-only), but it is SSH/RDP connectivity
	//     infrastructure for reaching an instance, not part of the
	//     instance's own identity, EBS, or ENI periphery this batch's
	//     mandate covers. Left for a networking-focused batch.
	//   - aws_nat_gateway_eip_association and
	//     aws_network_interface_sg_attachment: both evidence-only,
	//     property-children row-gen folds onto a parent (AWS::EC2::NatGateway
	//     and AWS::EC2::NetworkInterface respectively) with no pastable row
	//     of their own (issue #44's own non-goals). The second parent,
	//     aws_network_interface, is ratified below; the first,
	//     aws_nat_gateway, is not admitted at all yet (SURVEY.md's
	//     blocked-emulator: floci loses subnet_id on read) and is out of
	//     this batch's instances/EBS/ENI scope regardless.
	//
	// aws_placement_group is the one correction this batch makes to
	// row-gen's own classification, not to the provider's identity:
	// row-gen filed it evidence-only because registry.json's primary
	// identifier (GroupName) does not string-match the provider's own
	// argument name (name) closely enough for row-gen's classifier to
	// paste a row (issue #44's own non-goal — no fuzzy matching between a
	// CFN field name and a provider argument name). The provider's real,
	// documented import command settles it independently of the registry:
	// client-named via `name`, the same shape as aws_key_pair alongside it
	// below.

	serverAssigned("aws_instance",
		"EC2 mints the instance ID (i-…) at create time; ami, instance_type, subnet_id and the rest of the launch configuration describe what to launch, not what comes back. Confirmed against the provider's own identity schema (v6.58.0: required id) and its documented import command.",
		"INSTANCEID", "id"),
	serverAssigned("aws_ec2_fleet",
		"EC2 mints the fleet's own identifier (fleet-…) at create time; the type's launch_template_config and target_capacity_specification blocks describe what to launch, not the fleet's own identity.",
		"FLEETID", "id"),
	serverAssigned("aws_ec2_capacity_reservation",
		"EC2 mints the reservation's ID (cr-…) at create time; instance_type, instance_platform and availability_zone in configuration describe what capacity to reserve, not the reservation's own identity.",
		"ID", "id"),
	serverAssigned("aws_ec2_host",
		"EC2 mints the dedicated host's ID (h-…) at create time; availability_zone and instance_type/instance_family in configuration describe what the host supports, not the host's own identity.",
		"HOSTID", "id"),
	serverAssigned("aws_network_interface",
		"EC2 mints the ENI's ID (eni-…) at create time; subnet_id in configuration names where the interface lives, not the interface itself. Confirmed against the provider's own identity schema (v6.58.0: required id).",
		"ID", "id"),
	serverAssigned("aws_network_interface_attachment",
		"EC2 mints the attachment's own ID (eni-attach-…) at create time, distinct from the instance_id and network_interface_id arguments that name the two ends of the attachment; the provider's own docs export no id attribute for this type at all, only attachment_id, which is also the documented import ID.",
		"ATTACHMENTID", "attachment_id"),
	serverAssigned("aws_network_interface_permission",
		"EC2 mints the permission's own ID at create time, distinct from the network_interface_id, aws_account_id and permission arguments that describe what is granted; the provider's own docs export no id attribute for this type, only network_interface_permission_id, which is also the documented import ID.",
		"NETWORKINTERFACEPERMISSIONID", "network_interface_permission_id"),
	serverAssigned("aws_eip_association",
		"EC2 mints the association's own ID (eipassoc-…) at create time, distinct from the allocation_id, instance_id and network_interface_id arguments that name what is being associated.",
		"ID", "id"),
	serverAssigned("aws_spot_fleet_request",
		"EC2 mints the spot fleet request's ID (sfr-…) at create time; the type's launch_specification/launch_template_config and target_capacity arguments describe what to launch, not the request's own identity.",
		"ID", "id"),

	TypeIdentity{
		// registry.json: primary_identifier=["KeyName"], in
		// create_only_properties and not in read_only_properties —
		// client-named. Confirmed directly against the provider's own
		// documented import command (terraform import aws_key_pair.deployer
		// deployer-key) and its Attribute Reference, which states id "The
		// key pair name."
		Type:          "aws_key_pair",
		Components:    []Component{attr("key_name")},
		ImportSyntax:  "KEY_NAME",
		IdentityAttrs: []string{"id", "key_name"},
	},
	TypeIdentity{
		// row-gen classified this evidence-only (see the batch comment
		// above for why); the provider's real, documented import command
		// settles it anyway: "terraform import aws_placement_group.prod_pg
		// production-placement-group" imports by the group's own `name`
		// argument, and the Attribute Reference confirms id "The name of
		// the placement group." — the same client-named shape as
		// aws_key_pair just above.
		Type:          "aws_placement_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	// ---- Registry-ratified (#40, #44): fourth batch, DynamoDB periphery
	// ---- and ElastiCache (issue #65) ------------------------------------
	//
	// Same pipeline as the three batches above: every row started as a
	// tools/row-gen proposal or evidence-only finding from live/registry.json
	// and live/mapping.json, cross-checked against the AWS provider's
	// documented import behaviour (its "Import" section, fetched from the
	// provider's own website/docs/r/ source at the pinned v6.58.0 tag) and,
	// for two corrections below, against live/import-grammar.json's
	// scraped import grammar directly. Cohort estate:
	// live/e2e/estates/dynamodb-elasticache.
	//
	// DynamoDB's row-gen section (6 types) is almost entirely the
	// already-admitted aws_dynamodb_table: the other five are either
	// evidence-only or property-children folded onto AWS::DynamoDB::Table
	// with no CFN type of their own, so row-gen's primaryIdentifier
	// analysis never runs on four of them at all. There is no separate
	// DynamoDB "backup" resource type in the provider to propose or
	// reject — point-in-time recovery is an argument on aws_dynamodb_table
	// itself, not a standalone managed resource, so there was nothing here
	// for a backup row to be.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_elasticache_global_replication_group: row-gen proposed
	//     server-assigned via the registry's GlobalReplicationGroupId, and
	//     unlike the two corrections below, the provider agrees with the
	//     shape, not just the argument. Its own Argument Reference has no
	//     global_replication_group_id argument at all — the two Required
	//     arguments are global_replication_group_id_suffix and
	//     primary_replication_group_id — and its Attribute Reference
	//     exports global_replication_group_id as a separate, computed
	//     field: AWS prepends its own region-derived code to the
	//     configured suffix (the documented import example,
	//     okuqm-global-replication-group-1, is not a string any
	//     configuration sets). live/survey-full.json's own automated pass
	//     reaches "moves to Ops" for the same reason (untaggable, no
	//     native list resource, no identity schema in v6.58.0) — and
	//     unlike aws_ecr_registry_policy and its two account-singleton
	//     siblings in the IAM/ECR batch above, this type is not a
	//     one-per-account singleton either: many global replication
	//     groups can exist per account, so there is no deterministic
	//     fallback identity to read without a list. No admission path
	//     recovers it.
	//
	// Deferred as composite import IDs this batch does not hand-write, the
	// same restraint the Lambda and messaging batches above already
	// state (both times over a row-gen-proposed server-assigned guess;
	// these four are over row-gen's evidence-only and needs-hand-separator
	// output instead, now checkable because live/import-grammar.json's
	// scrape gives every one of them a confirmed separator character and
	// argument order — issue #65 notes this is why needs-hand-separator is
	// "now largely resolvable" going forward). Confirmed, not merely
	// guessed, and left out anyway: adding new composite-separator rows to
	// a registry-ratified section is a bigger methodological step than
	// this batch takes, so a future batch can lift these four rows
	// directly rather than re-deriving them:
	//
	//   - aws_dynamodb_global_secondary_index: table_name and index_name,
	//     both Required arguments and both named in the provider's own
	//     identity schema (required_for_import=[index_name, table_name]),
	//     joined by a comma (terraform import
	//     aws_dynamodb_global_secondary_index.example
	//     'example-table,example-index'). Parent-derived off the
	//     already-admitted aws_dynamodb_table.
	//   - aws_dynamodb_kinesis_streaming_destination: table_name and
	//     stream_arn, both Required arguments, joined by a comma
	//     (terraform import
	//     aws_dynamodb_kinesis_streaming_destination.example
	//     example,arn:aws:kinesis:us-east-1:111122223333:exampleStreamName).
	//     Docs-tier evidence only — v6.58.0 ships no identity schema for
	//     this type — but the Argument Reference and the import command
	//     agree on both required arguments and the separator.
	//   - aws_elasticache_user_group_association: user_group_id and
	//     user_id, both Required arguments, joined by a comma (terraform
	//     import aws_elasticache_user_group_association.example
	//     userGoupId1,userId). Parent-derived off
	//     aws_elasticache_user_group and aws_elasticache_user, both
	//     ratified below.
	//   - aws_dynamodb_contributor_insights: table_name (Required) and
	//     index_name (Optional), joined into
	//     name:TABLE_NAME/index:INDEX_NAME plus the account number
	//     (terraform import aws_dynamodb_contributor_insights.test
	//     name:ExampleTableName/index:ExampleIndexName/123456789012). Left
	//     out for a second reason beyond the separator: index_name is
	//     optional, and expressing "this literal segment only when an
	//     argument is set, omitted otherwise" is a component this table's
	//     vocabulary does not have — the same gap that kept the messaging
	//     batch's aws_cloudwatch_event_rule out for its optional
	//     event_bus_name.

	TypeIdentity{
		// row-gen classified this client-named (registry primaryIdentifier
		// TableName, in createOnlyProperties and not readOnlyProperties)
		// but could not paste a row: the argument name was only GUESSED
		// (snake_cased from "TableName") because v6.58.0 ships no identity
		// schema for this type and live/import-grammar.json's own scrape
		// shows no parsed argument either — its Import section text says
		// only "using the global table name" with no import-block
		// argument list to lift a name from. Confirmed directly against
		// the provider's own Argument Reference (fetched from the pinned
		// v6.58.0 docs source): this AWS DynamoDB Global Table (V1,
		// deprecated in the provider's own docs in favor of
		// aws_dynamodb_table's replica block, but still real and
		// importable in v6.58.0) declares exactly one required argument,
		// name, and its documented import command sets id to that same
		// name verbatim (terraform import aws_dynamodb_global_table.MyTable
		// MyTable). No tags argument exists in the Argument Reference at
		// all — survey-full.json's own taggable:false signal agrees — so
		// this type is untaggable, the same "moves to Ops" default the
		// automated classifier falls back to whenever it has neither an
		// identity schema nor a tags argument to reason from
		// (aws_ecs_cluster's docs-tier exception is the same shape, minus
		// the tags argument that tips it to marker instead). Client-named
		// admission needs neither: the name is already in configuration,
		// and no list or tag step is any part of path 3.
		Type:          "aws_dynamodb_global_table",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// Never a row-gen proposal at all: live/mapping.json folds this
		// type onto AWS::DynamoDB::Table (via==fold) rather than mapping
		// it to its own CFN type, so row-gen's primaryIdentifier analysis
		// never runs on it and it prints only as an evidence-only
		// property-child. Verified independently instead, against the
		// provider's real identity schema (live/survey-full.json:
		// required_for_import=[resource_arn], no optional-for-import
		// attribute beyond the account/region context pair) and its
		// documented import command (terraform import
		// aws_dynamodb_resource_policy.example
		// arn:aws:dynamodb:us-east-1:1234567890:table/my-table) — a single
		// argument, resource_arn, the parent table's own ARN. The same
		// named-singleton-child shape as aws_sns_topic_policy and
		// aws_sqs_queue_policy from the messaging batch above, keyed on
		// the parent's "arn"-shaped argument rather than a bucket's
		// "bucket". live/survey-full.json's own automated pass reaches
		// the same verdict (path: parent-derived, admission: schema)
		// independently of this batch's hand check.
		Type:          "aws_dynamodb_resource_policy",
		Components:    []Component{attr("resource_arn")},
		ImportSyntax:  "RESOURCE_ARN",
		IdentityAttrs: []string{"resource_arn"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[ClusterName], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; the argument came from
		// live/import-grammar.json rather than the provider's identity
		// schema (v6.58.0 ships none for this type). Confirmed against
		// the provider's own Argument Reference (cluster_id, Required)
		// and its documented import command (terraform import
		// aws_elasticache_cluster.my_cluster my_cluster).
		Type:          "aws_elasticache_cluster",
		Components:    []Component{attr("cluster_id")},
		ImportSyntax:  "CLUSTER_ID",
		IdentityAttrs: []string{"cluster_id"},
	},
	TypeIdentity{
		// row-gen's registry rule read CacheParameterGroupName as
		// primaryIdentifier ⊆ readOnlyProperties and would have proposed
		// server-assigned, but issue #55's import-grammar demotion caught
		// it first: live/import-grammar.json shows the documented import
		// ID is argument-composed ("redis-params"), so row-gen printed
		// evidence-only rather than a wrong pastable row — the same
		// automated catch that already protected the Lambda batch's
		// aws_lambda_alias-shaped mistakes from ever being proposed
		// clean. Confirmed against the provider's real Argument
		// Reference: name and family are both Required, id equals name
		// verbatim per the Attribute Reference, and tags is a real
		// optional argument (survey-full.json's own taggable:true signal
		// agrees, classing this "marker" on raw signals alone — the
		// identity is nonetheless the name argument, confirmed by the
		// documented import command: terraform import
		// aws_elasticache_parameter_group.default redis-params).
		Type:          "aws_elasticache_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	// ---- Registry-ratified (#40, #44): fourth batch, data plane (Kinesis,
	// ---- KinesisFirehose, Glue, Athena; issue #65's recipe) ---------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour (its "Import" section
	// at the pinned v6.58.0 tag). Several rows also needed the pinned
	// provider's own wire schema, read directly with `terraform providers
	// schema -json` against the real hashicorp/aws 6.58.0 binary, to settle
	// whether a value row-gen or the website docs named is actually a
	// settable configuration argument — the same category of check that
	// caught the earlier batches' aws_sqs_queue and aws_sns_topic_policy
	// corrections, applied here to more rows than any prior batch needed.
	// Cohort estate: live/e2e/estates/data.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_athena_named_query: row-gen proposed server-assigned via the
	//     registry's opaque "NamedQueryId", and the provider agrees — the
	//     documented import command uses the query ID, which no argument
	//     reconstructs. Unlike this table's singleton-per-account
	//     serverAssigned rows (aws_ecr_registry_policy and this batch's own
	//     aws_glue_data_catalog_encryption_settings below), a named query is
	//     not a singleton: many exist per workgroup, distinguished only by
	//     that opaque ID. The pinned provider's wire schema confirms the
	//     type carries no tags argument and the provider ships no native
	//     list resource for it either, so none of the four admission paths
	//     recovers a pre-existing instance — live/survey-full.json's own
	//     classifier reaches the same "moves to Ops" verdict for exactly
	//     this reason.
	//   - aws_glue_schema: row-gen proposed server-assigned via the
	//     registry's "Arn", and the provider's documented import command
	//     confirms an ARN
	//     (arn:aws:glue:REGION:ACCOUNT:schema/REGISTRY_NAME/SCHEMA_NAME) —
	//     but unlike aws_glue_registry below, REGISTRY_NAME is not
	//     reconstructable: the resource's only registry reference argument
	//     is registry_arn (the parent's full ARN string), and the pinned
	//     provider's wire schema shows registry_name is computed-only,
	//     never settable. Building this identity would mean parsing a bare
	//     name out of a parent ARN string, a component this table's
	//     vocabulary does not have (sep, attr and cloud concatenate; none
	//     of them extract a substring of another component). Left out
	//     rather than guessed.
	//   - aws_glue_partition: row-gen classed this evidence-only (the
	//     registry's own primaryIdentifier rule does not fire on it at
	//     all). The provider's documented import command
	//     (CATALOG_ID:DATABASE_NAME:TABLE_NAME:PARTITION_VALUE1#PARTITION_VALUE2#...)
	//     is otherwise fully reconstructable — the same account-derived
	//     catalog_id as aws_glue_catalog_table above, plus its required
	//     database_name and table_name — except for partition_values
	//     itself, a required list(string) argument the pinned provider's
	//     wire schema confirms is joined into the import string with "#".
	//     This table's Components vocabulary has no list-join primitive
	//     (every component reads one scalar argument), so this one is left
	//     out rather than guessed, the same standard of care as the schema
	//     rejection above.
	//
	// Out of this batch's named scope, not rejected on the merits:
	//
	//   - aws_kinesis_resource_policy: row-gen proposed this cleanly
	//     (client-named via the provider's own identity schema,
	//     required_for_import=[resource_arn]) but issue #65's recipe scopes
	//     this batch's Kinesis slice to streams and consumers.
	//   - aws_kinesis_analytics_application, aws_kinesisanalyticsv2_application,
	//     aws_kinesis_video_stream: different CFN services (KinesisAnalytics,
	//     KinesisAnalyticsV2, KinesisVideo), not Kinesis or KinesisFirehose.
	//   - aws_glue_catalog: a distinct, newer top-level "Catalog" resource
	//     (federated catalog registration) easily confused with
	//     aws_glue_catalog_database/aws_glue_catalog_table by name alone;
	//     issue #65's recipe names only "catalog database/table". Its own
	//     identity schema (required=[name]) would make it a clean, direct
	//     client-named row for a future batch.
	//   - aws_glue_catalog_table_optimizer, aws_glue_dev_endpoint,
	//     aws_glue_security_configuration, aws_glue_user_defined_function,
	//     aws_glue_workflow, aws_glue_data_quality_ruleset: all evidence-only
	//     per row-gen (no pastable row), outside this batch's named scope.
	//     aws_glue_catalog_table_optimizer in particular looks fully
	//     reconstructable on inspection (catalog_id,database_name,table_name,type,
	//     comma-separated, no list-join needed) — a plausible pickup for a
	//     future batch.
	//   - aws_athena_capacity_reservation, aws_athena_prepared_statement:
	//     needs-hand-separator and evidence-only respectively per row-gen;
	//     issue #65's recipe names only workgroups, data catalogs and named
	//     queries.

	TypeIdentity{
		// registry.json classed this evidence-only (argument "name" GUESSED
		// from the CFN property name, not backed by an identity schema or
		// the carve seed — v6.58.0 ships no identity schema for this type).
		// Independently confirmed against the provider's documented import
		// command (terraform import aws_kinesis_stream.example
		// example-stream) and Attribute Reference ("arn - ... (same as
		// id)"): the pinned provider's wire schema confirms name is the
		// sole required argument and id/arn are optional+computed.
		Type:          "aws_kinesis_stream",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen classified this needs-hand-separator: registry.json's
		// primary identifier is the pair [VolumeId, InstanceId], a
		// composite with no separator any schema names (issue #44's own
		// non-goal). The separator is not a guess here: live/import-
		// grammar.json's scrape of the provider's own Import section names
		// it directly — DEVICE_NAME:VOLUME_ID:INSTANCE_ID — and the
		// provider's own identity schema (v6.58.0) requires exactly those
		// three arguments, all Required in the Argument Reference too, so
		// any realistic configuration already has them. Parent-derived over
		// aws_ebs_volume (already admitted) and aws_instance (ratified
		// above in this same batch): resolving this type needs both to
		// resolve first. The provider's docs export no additional
		// id-shaped attribute for this type at all — only the three
		// arguments read back — so no alias beyond them is claimed here,
		// the same standard of care aws_route's synthesized id gets.
		Type: "aws_volume_attachment",
		Components: []Component{
			attr("device_name"),
			sep(":"),
			attr("volume_id"),
			sep(":"),
			attr("instance_id"),
		},
		ImportSyntax:  "DEVICE_NAME:VOLUME_ID:INSTANCE_ID",
		IdentityAttrs: []string{"device_name", "instance_id", "volume_id"},
	},
	TypeIdentity{
		// row-gen proposed this server-assigned via registry.json's
		// AccountId (AWS::EC2::SnapshotBlockPublicAccess's primary
		// identifier) — the same singleton-per-account shape as the
		// IAM/ECR batch's three ECR registry-level types. The provider
		// disagrees about the shape, not the singleton-ness: its own
		// identity schema requires nothing at all for import (account_id
		// and region are both Optional), and its documented import command
		// is always the fixed literal string "default" ("terraform import
		// aws_ebs_snapshot_block_public_access.example default"), not an
		// account ID the account happens to have. This is a per-region
		// settings object AWS gives every region exactly one of, not a
		// value AWS mints per resource, so it needs no discovery at all:
		// Components below is a pure literal, computable from configuration
		// with nothing to look up — ServerAssigned is deliberately false,
		// unlike every other row in this batch. The provider's own docs say
		// this resource "exports no additional attributes", so no
		// IdentityAttrs are claimed either, the same standard of care
		// aws_route's synthesized id gets.
		Type:          "aws_ebs_snapshot_block_public_access",
		Components:    []Component{sep("default")},
		ImportSyntax:  "default",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ReplicationGroupId], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (replication_group_id, Required) and its
		// documented import command (terraform import
		// aws_elasticache_replication_group.my_replication_group
		// replication-group-1).
		Type:          "aws_elasticache_replication_group",
		Components:    []Component{attr("replication_group_id")},
		ImportSyntax:  "REPLICATION_GROUP_ID",
		IdentityAttrs: []string{"replication_group_id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[ServerlessCacheName], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (name, Required) and its documented import
		// command (terraform import
		// aws_elasticache_serverless_cache.my_cluster my_cluster).
		Type:          "aws_elasticache_serverless_cache",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[CacheSubnetGroupName], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (name, Required) and its documented import
		// command (terraform import aws_elasticache_subnet_group.bar
		// tf-test-cache-subnet).
		Type:          "aws_elasticache_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	// ---- Registry-ratified (#40, #44): fourth batch, storage (EFS, FSx,
	// ---- Backup, issue #65) ----------------------------------------------
	//
	// Same tools/row-gen pipeline as the three batches above: every row
	// started as a proposal from live/registry.json, and every row that
	// landed here was independently cross-checked, not accepted on the
	// registry's classification alone. The cross-check for this batch
	// mostly used live/import-grammar.json — tools/importdocs-gen's parse
	// of the AWS provider's own website/docs/r/ Import sections at the
	// pinned v6.58.0 tag — rather than the provider's identity schema,
	// because live/survey-full.json records that none of EFS, FSx or
	// Backup ships an identity schema in this provider release at all
	// ("identity_schema": false for every type in scope); three rows below
	// also needed the provider's rendered Attribute Reference fetched
	// directly, because the grammar excerpt alone did not say what the
	// import-grammar's own "id" resolved to. Cohort estate:
	// live/e2e/estates/storage.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_backup_selection: row-gen proposed server-assigned via the
	//     registry's opaque, undocumented "Id". The provider's real,
	//     documented import command (terraform import
	//     aws_backup_selection.example plan-id|selection-id) is a two-part
	//     composite, and unlike aws_iam_role_policy's ROLENAME:POLICYNAME
	//     or aws_route53_record's zone/name/type, the second half — the
	//     selection's own Backup Selection identifier — is exactly as
	//     server-assigned as the registry said, with no argument in
	//     configuration that reconstructs it (plan_id is a live reference,
	//     but the selection id is minted by AWS Backup and appears nowhere
	//     until after create). The type also carries no tags argument, so
	//     the marker path cannot substitute either. This is not a
	//     CFN-says-server-assigned-provider-disagrees case like the three
	//     corrections below; here the registry and the provider agree, and
	//     the answer is still "no admission path recovers it" — exactly
	//     what live/survey-full.json's own mechanical classifier says for
	//     this type ("moves to Ops").
	//   - aws_backup_restore_testing_selection: row-gen marks this
	//     needs-hand-separator (a composite RestoreTestingPlanName +
	//     RestoreTestingSelectionName primary identifier, per its own
	//     non-goals). Unusually for that class, live/import-grammar.json
	//     does name the separator this time — "name:restore_testing_plan_name"
	//     — and the provider's schema confirms both halves are required,
	//     client-supplied arguments (name, restore_testing_plan_name), not
	//     a server-assigned value like the selection above. That makes it
	//     a strong candidate for a future batch, but this batch keeps the
	//     standing discipline the Lambda, IAM/ECR and messaging batches
	//     already established: never hand-write a composite row-gen itself
	//     declined to propose. Left for a batch prepared to extend the
	//     table's own component vocabulary test coverage around it.
	//   - aws_efs_mount_target: row-gen proposed server-assigned via the
	//     registry's "Id", and the identity classification is not in
	//     question — the provider's documented import command (terraform
	//     import aws_efs_mount_target.alpha fsmt-52a643fb) confirms a
	//     server-assigned MountTargetId with nothing in configuration
	//     (ip_address, subnet_id, file_system_id) that reconstructs it.
	//     What sinks it is that no admission path recovers that id from a
	//     stateless run at all: the type carries no tags argument (the
	//     provider's docs list no tags block, confirmed against
	//     live/registry.json's AWS::EFS::MountTarget tagging.taggable:
	//     false), so the marker path has nothing to search on, and
	//     live/registry.json's handlers.list_required_input for the same
	//     CFN type is ["FileSystemId"] rather than empty, so
	//     internal/live/registry.Roster.Listable reports false and the
	//     Cloud Control fallback (#47, the mechanism aws_efs_file_system
	//     below relies on) does not apply either — scanTypeCloudControl
	//     only ever calls ListResources with no input. Same verdict as
	//     aws_backup_selection above, and the same one
	//     live/survey-full.json's classifier reaches independently
	//     ("moves to Ops").
	//
	// Corrected, not merely accepted — three rows where row-gen's registry
	// heuristic (primaryIdentifier ⊆ readOnlyProperties ⇒ server-assigned)
	// pointed at a CloudFormation-only field the provider does not use for
	// import, the same failure shape as the Lambda batch's aws_lambda_alias
	// and the IAM batch's aws_iam_policy:
	//
	//   - aws_backup_framework: row-gen proposed server-assigned via the
	//     registry's FrameworkArn. The provider's documented Import section
	//     says otherwise, in so many words: "import Backup Framework using
	//     the `id` which corresponds to the name of the Backup Framework."
	//     `name` is a required argument already in configuration
	//     (live/survey-full.json's schema pull confirms it; the registry's
	//     own create_only_properties row even names FrameworkName). Wired
	//     below as client-named via name, not server-assigned via ARN.
	//   - aws_backup_report_plan: the same shape and the same sentence,
	//     verbatim, in the provider's docs ("...using the `id` which
	//     corresponds to the name of the Backup Report Plan."), against
	//     row-gen's registry-driven ReportPlanArn proposal. Wired
	//     client-named via name.
	//   - aws_backup_logically_air_gapped_vault: row-gen would not propose
	//     this one at all — its own argument-name guess (backup_vault_name)
	//     was unbacked by any schema, so it surfaced evidence-only, no
	//     pastable row, per #44's non-goals. The provider's rendered docs
	//     (website/docs/r/backup_logically_air_gapped_vault.html.markdown)
	//     resolve it cleanly: "The `id` ... The name of the Logically Air
	//     Gapped Backup Vault," and `name` is a required argument. Wired
	//     client-named via name, the same shape as aws_backup_vault.
	//
	// aws_efs_file_system is issue #47's own worked example — see
	// internal/live/lint/admission.go's comment above this batch's
	// admittedTypesV0 rows for the Cloud Control enumeration-source note,
	// which belongs to the discovery package rather than to this table.

	serverAssigned("aws_efs_file_system",
		"EFS assigns the file system ID (fs-…) at create time; AvailabilityZoneName, Encrypted, KmsKeyId and PerformanceMode describe it but do not name it.",
		"FILESYSTEMID", "id"),
	serverAssigned("aws_efs_access_point",
		"EFS assigns the access point ID (fsap-…) at create time; FileSystemId names the parent, not the access point itself.",
		"ACCESSPOINTID", "id"),

	// The FSx family: four TF resource types folded onto CloudFormation's
	// one generic AWS::FSx::FileSystem (live/mapping.json's "alias" rows),
	// none with a name argument at all — confirmed against the provider's
	// own schema pull (no `name` attribute on any of the four) as well as
	// against live/import-grammar.json, which documents all four importing
	// "using the `id`" with no argument composition.
	serverAssigned("aws_fsx_lustre_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_ontap_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_windows_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_openzfs_file_system",
		"FSx assigns the file system ID (fs-…) at create time; the type has no name argument, and KmsKeyId, SecurityGroupIds, SubnetIds and BackupId describe it but do not name it.",
		"ID", "id"),
	serverAssigned("aws_fsx_ontap_storage_virtual_machine",
		"FSx assigns the storage virtual machine ID (svm-…) at create time; the name argument names the SVM but the provider's documented import identity is the ID, not the name.",
		"ID", "id"),
	serverAssigned("aws_fsx_ontap_volume",
		"FSx assigns the volume ID (fsvol-…) at create time; the name argument names the volume but the provider's documented import identity is the ID, not the name.",
		"VOLUMEID", "id"),
	serverAssigned("aws_fsx_openzfs_volume",
		"FSx assigns the volume ID (fsvol-…) at create time; the name argument names the volume but the provider's documented import identity is the ID, not the name.",
		"VOLUMEID", "id"),
	serverAssigned("aws_fsx_openzfs_snapshot",
		"FSx assigns the snapshot ID (fsvolsnap-…) at create time, distinct from the name argument; the provider's own Attribute Reference documents id and name as two separate values.",
		"ID", "id"),
	serverAssigned("aws_fsx_data_repository_association",
		"FSx assigns the data repository association ID (dra-…) at create time; FileSystemId, FileSystemPath and DataRepositoryPath describe it but do not name it.",
		"ASSOCIATIONID", "id"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, proposed correctly.
		// Confirmed against the provider's documented import command
		// (terraform import aws_fsx_s3_access_point_attachment.example
		// example-attachment) and against the provider's own schema pull,
		// which has no id attribute for this type at all — the same
		// standard of care aws_route's synthesized id gets.
		Type:          "aws_fsx_s3_access_point_attachment",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	// ---- Registry-ratified (#40, #44, #65): fourth batch, Route53
	// ---- remainder and CloudFront ----------------------------------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour (its Argument
	// Reference, Attribute Reference and Import section, fetched from the
	// provider's own website/docs/r/ source at the pinned v6.58.0 tag)
	// rather than accepted on the registry's word alone. This batch adds a
	// second kind of check row-gen itself cannot make: whether a
	// server-assigned type is actually *recoverable* at all. row-gen's
	// registry evidence only says an identity is server-minted, not whether
	// this fork's tag-filtered marker discovery can ever find the specific
	// live instance a resource block names — that needs
	// live/tag-verbs.json (which AWS API operation, if any, tags a given
	// service's resources) and live/survey-full.json's per-type taggable
	// signal (the AWS provider's own schema, which does not expose a tags
	// argument the underlying API does not support). aws_route53_zone's
	// marker row and aws_route53_record's zone-keyed composite (both above,
	// pre-registry) are this table's oldest precedent for both shapes this
	// batch leans on: a taggable server-assigned type needs nothing but its
	// own marker row, and a type whose import identity is literally built
	// from an already-admitted parent's live value plus configuration
	// arguments is parent-derived, not server-assigned, however row-gen
	// itself classified it. Cohort estate:
	// live/e2e/estates/route53-cloudfront.
	//
	// Rejected, and deliberately absent from this table — every one
	// confirmed server-assigned on the provider's own docs (row-gen was
	// right about that much), but genuinely unrecoverable: untaggable (no
	// live/tag-verbs.json service-wide TagResource/ChangeTagsForResource
	// covers the type, and the provider's own schema carries no tags
	// argument), not an account- or parent-wide singleton the way
	// aws_ecr_registry_policy's account-scoped ECR registry ID is (the
	// second and third IAM/ECR batch's precedent for admitting an untaggable
	// server-assigned type anyway), and not composable from an admitted
	// parent's value the way the parent-derived rows below are — an account
	// may hold arbitrarily many of them, and nothing in configuration or in
	// an admitted parent's identity picks out one:
	//
	//   - aws_route53_cidr_collection: row-gen proposed server-assigned via
	//     the registry's opaque "Id", confirmed against the provider's
	//     Import section (id alone, unrelated to the required "name"
	//     argument). Route53's own tagging operation, ChangeTagsForResource
	//     (live/tag-verbs.json), takes a ResourceType parameter that AWS
	//     documents as accepting only "healthcheck" and "hostedzone" — a
	//     CIDR collection is neither, and the provider's schema for this
	//     type carries no tags argument at all. An account may hold
	//     arbitrarily many collections with no marker to tell them apart.
	//   - aws_route53_cidr_location: row-gen folded this into
	//     AWS::Route53::CidrCollection's own registry entry and proposed
	//     parent-derived admission "once [the collection] is ratified". On
	//     independent verification its import grammar does turn out to be
	//     exactly that composite — the collection's own ID plus the
	//     location's "name" argument, comma-separated
	//     (COLLECTIONID,LOCATIONNAME, confirmed against the provider's
	//     Import section and Argument Reference) — the same shape as the
	//     parent-derived rows below. It stays out anyway, and only for the
	//     reason just above: its parent, aws_route53_cidr_collection, is
	//     rejected, so nothing admits the live collection ID a
	//     cidr_collection_id argument would need to resolve to in a
	//     stateless run.
	//   - aws_route53_query_log: row-gen folded this into
	//     AWS::Route53::HostedZone's own registry entry and proposed
	//     parent-derived admission through aws_route53_zone, the same
	//     speculative note as the cidr_location fold above. Verification
	//     does not bear it out here: the provider's documented import ID
	//     (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx) is a bare, opaque UUID the
	//     query-logging subscription's own create call mints — not the
	//     hosted_zone_id argument, and not any concatenation of it with
	//     another configured value. Query logging is not its own
	//     CloudFormation-registered resource type at all (it is a nested
	//     property of AWS::Route53::HostedZone), so there is no Cloud
	//     Control list handler to enumerate it by either, and the type
	//     carries no tags argument. row-gen's own "no pastable row" verdict
	//     was the honest one; the fold's speculative note does not survive
	//     independent verification.
	//   - aws_route53recoverycontrolconfig_routing_control: row-gen
	//     proposed server-assigned via the registry's opaque
	//     "RoutingControlArn", confirmed against the provider's own Import
	//     section and Attribute Reference. Unlike its three
	//     Route53RecoveryControl siblings ratified below, this type's
	//     schema carries no tags argument (confirmed against
	//     live/survey-full.json and against live/registry.json, whose
	//     createOnlyProperties for this type omits "Tags" where the
	//     cluster's and control panel's do not), and the ARN's own
	//     routing-control segment (d5d90e587870494b in the documented
	//     example) is a second, independent server-minted token — not the
	//     required "name" argument the provider docs also list, so it is
	//     not literally composable from configuration plus an admitted
	//     parent's ARN either.
	//   - aws_route53_resolver_dnssec_config: row-gen proposed
	//     server-assigned via the registry's opaque "Id", confirmed against
	//     the provider's Import section (rdsc-… id, not the type's sole
	//     "resource_id" argument). resource_id names a VPC — aws_vpc is
	//     already admitted above — but the DNSSEC config's own import id is
	//     a second, unrelated server-minted token, so unlike
	//     aws_route53_hosted_zone_dnssec below (whose import id *is*
	//     literally its parent's own id) this one cannot be built as a
	//     parent-derived composite. The type is untaggable and
	//     Route53Resolver's own generic TagResource operation
	//     (live/tag-verbs.json) does not change that: the provider's schema
	//     for this specific type carries no tags argument to attach through
	//     it.
	//   - aws_route53_resolver_query_log_config_association: row-gen
	//     proposed server-assigned via the registry's opaque "Id",
	//     confirmed against the provider's Import section (rqlca-… id,
	//     unrelated to the "resolver_query_log_config_id"/"resource_id"
	//     arguments it composes from at create time). Untaggable, same
	//     shape as the DNSSEC config just above.
	//   - aws_route53profiles_resource_association: row-gen proposed
	//     server-assigned via the registry's opaque "Id", confirmed against
	//     the provider's Import section (rpa-id-… id, unrelated to its
	//     "profile_id"/"resource_arn"/"name" arguments). Unlike its two
	//     Route53Profiles siblings ratified below, live/registry.json shows
	//     no "Tags" in this type's createOnlyProperties, and
	//     live/survey-full.json confirms the provider's schema carries none
	//     either — Route53Profiles' own generic TagResource operation
	//     (live/tag-verbs.json) has nothing to attach to for this type.
	//   - aws_cloudfront_cache_policy, aws_cloudfront_continuous_deployment_policy,
	//     aws_cloudfront_key_group, aws_cloudfront_origin_access_identity,
	//     aws_cloudfront_origin_request_policy, aws_cloudfront_public_key,
	//     aws_cloudfront_response_headers_policy: row-gen proposed all seven
	//     server-assigned via each type's registry-opaque "Id", confirmed
	//     against the provider's Import sections (each imports by a bare,
	//     server-minted id unrelated to the type's own required arguments).
	//     None carries a tags argument (CloudFront's own generic
	//     TagResource operation in live/tag-verbs.json exists but is not
	//     composable by tools/tagverbs-gen's own accounting, and none of
	//     these seven types' provider schemas expose a tags argument to
	//     drive it through regardless), an account may hold arbitrarily many
	//     of each, and none is a parent-derived composite the way
	//     aws_cloudfront_monitoring_subscription below is. This is not
	//     aws_cloudfront_origin_access_control's own shape, despite looking
	//     identical on the surface — see its entry below for the mechanism
	//     that tells the two apart.
	//
	// Out of scope, never proposed by row-gen at all: aws_route53_resolver_config
	// (evidence-only — its "resource_id" argument name is guessed, not
	// backed by a provider identity schema or the carve seed) and the whole
	// Route53RecoveryReadiness service (all four types evidence-only, same
	// guessed-argument reason) — issue #65's recipe names "Route53Profiles/
	// RecoveryControl if proposed" and is silent on RecoveryReadiness for
	// exactly that reason.

	serverAssigned("aws_route53_health_check",
		"Route 53 assigns the health check's own id (a UUID) at create time; nothing in configuration reconstructs it.",
		"ID", "id"),

	serverAssigned("aws_route53profiles_association",
		"Route 53 Profiles assigns the association's own id (rpa-id-…) at create time; the name, resource_id and profile_id arguments describe what is being associated but do not identify the association itself.",
		"ID", "id"),
	serverAssigned("aws_route53profiles_profile",
		"Route 53 Profiles assigns the profile's own id (rp-…) at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),

	serverAssigned("aws_route53recoverycontrolconfig_cluster",
		"Route53 Recovery Control mints the cluster's own ARN at create time; the name argument is client-chosen but the provider's documented import id is the ARN.",
		"ARN", "arn", "id"),
	serverAssigned("aws_route53recoverycontrolconfig_control_panel",
		"Route53 Recovery Control mints the control panel's own ARN at create time, embedding the cluster_arn argument's cluster but not reducible to it; the provider's documented import id is the panel's own ARN.",
		"ARN", "arn", "id"),
	serverAssigned("aws_route53recoverycontrolconfig_safety_rule",
		"Route53 Recovery Control mints the safety rule's own ARN at create time; the rule's several *_rule_config blocks describe its condition but do not identify it.",
		"ARN", "arn", "id"),

	serverAssigned("aws_route53_resolver_endpoint",
		"Route53Resolver assigns the endpoint's own id (rslvr-in-…/rslvr-out-…) at create time; direction and the security_group_ids/ip_address arguments configure it but do not identify it.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_firewall_domain_list",
		"Route53Resolver assigns the domain list's own id (rslvr-fdl-…) at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_firewall_rule_group",
		"Route53Resolver assigns the rule group's own id (rslvr-frg-…) at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_firewall_rule_group_association",
		"Route53Resolver assigns the association's own id (rslvr-frgassoc-…) at create time; the firewall_rule_group_id and vpc_id arguments name what is being associated but do not identify the association itself.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_query_log_config",
		"Route53Resolver assigns the query log config's own id (rqlc-…) at create time; the name and destination_arn arguments configure it but do not identify it.",
		"ID", "id"),
	serverAssigned("aws_route53_resolver_rule",
		"Route53Resolver assigns the rule's own id (rslvr-rr-…) at create time; the provider's identity schema requires exactly that id, and none of domain_name, rule_type or target_ip reconstructs it.",
		"ID", "id"),
	// aws_route53_resolver_rule_association is the one row-gen proposal in
	// this family whose own registry evidence and live/survey-full.json's
	// mechanical classifier both land on "list + content match" rather than
	// a flat marker: it is untaggable (no tags argument in the provider's
	// schema), but the provider ships a native list resource for it (rare
	// among the types in this batch) whose results carry resolver_rule_id
	// and vpc_id — the same two required arguments the association's create
	// call takes — so listing and matching by that content, the same
	// mechanism aws_eip above uses with its tofu-slot tag standing in for
	// content instead, recovers a specific instance without needing a tag
	// at all. Confirmed against the provider's own Identity Schema (single
	// required attribute: id, unrelated to resolver_rule_id/vpc_id
	// verbatim, ruling out a parent-derived composite the way
	// aws_route53_zone_association below is one).
	serverAssigned("aws_route53_resolver_rule_association",
		"Route53Resolver assigns the association's own id (rslvr-rrassoc-…) at create time; untaggable, but the provider's native list resource returns resolver_rule_id and vpc_id per association, the same two required arguments the association's create call takes, so list plus content match (as aws_eip above uses tags for) recovers a specific instance.",
		"ID", "id"),

	serverAssigned("aws_cloudfront_anycast_ip_list",
		"CloudFront assigns the anycast IP list's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_connection_function",
		"CloudFront assigns the connection function's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_connection_group",
		"CloudFront assigns the connection group's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	// aws_cloudfront_origin_access_control: row-gen proposed server-assigned
	// via the registry's opaque "Id" — right about the identity (confirmed
	// against the provider's own Import section: the bare OAC id, unrelated
	// to the required "name" argument), but the flat serverAssigned()
	// template undersells it exactly the way it undersold aws_sqs_queue in
	// the messaging batch. This type is untaggable (no tags argument in the
	// provider's schema; CloudFront has no per-OAC tagging API at all), so a
	// tag-filtered marker list cannot find one. live/SURVEY.md already
	// worked this out by hand, before row-gen existed: it carries this exact
	// type as "list + content match", the same category
	// aws_route53_resolver_rule_association above earns from its own native
	// list resource — list every OAC in the account and match by content,
	// here the required "name" argument, which AWS enforces unique per
	// account, standing in for the tag a tag-filtered marker would otherwise
	// use. That prior classification is honored here, not second-guessed.
	// It is untaggable and one of live/survey.json's curated 68 rows, which
	// is exactly the shape the messaging batch's aws_sns_topic_subscription
	// hit and deferred rather than ratified — but issue #54 has since landed
	// (tools/survey-gen/untaggable_render.go): live/LIMITATIONS.md's
	// "Untaggable types cannot be removed by the sweep" entry now derives
	// its roster from live/survey-full.json's taggability signal across the
	// *entire* registry-backed roster, not from the curated 68 intersected
	// with the admission table the messaging batch's deferral was written
	// against, so admitting an untaggable curated-68 type is exactly the
	// same doc-mechanical fixup ratifying any other untaggable type already
	// needs — not the out-of-scope hand-edit it used to obligate. See
	// live/e2e/estates/route53-cloudfront/README.md for the full writeup.
	serverAssigned("aws_cloudfront_origin_access_control",
		"CloudFront assigns the origin access control's own id at create time; the name argument is client-chosen (and AWS-enforced unique per account, which is what the list-plus-content match reads) but is not the import identity.",
		"ID", "id"),
	// aws_cloudfront_distribution: precedented already in live/SURVEY.md
	// (path "marker", status "blocked-emulator") before this batch or
	// row-gen existed — row-gen itself declines to propose a pastable row
	// here (evidence-only: live/import-grammar.json's own heuristic
	// mis-read the Identity Schema's required "id" attribute as an
	// "argument-composed" id, which it is not — id is computed, not
	// settable). Confirmed directly against the provider's Identity Schema
	// (required: id) and Import section: the distribution's own id
	// (E74FTE3EXAMPLE-shaped), unrelated to any configuration argument.
	// Taggable and listable (live/survey-full.json), so the marker path
	// this row-gen-shaped entry follows the same shape as
	// aws_route53_zone's own pre-registry row above. Still blocked-emulator
	// per SURVEY.md (floci serves no usable CloudFront distribution
	// lifecycle, choudoufu#26) — not evidence against the identity, the
	// same standing the messaging batch's CloudWatch rows and this batch's
	// own aws_sqs_queue-style entries already established.
	serverAssigned("aws_cloudfront_distribution",
		"CloudFront assigns the distribution's own id at create time; the provider's identity schema requires exactly that id, and no configuration argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_distribution_tenant",
		"CloudFront assigns the distribution tenant's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_multitenant_distribution",
		"CloudFront assigns the multi-tenant distribution's own id at create time, the same shape as aws_cloudfront_distribution above; no configuration argument reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_trust_store",
		"CloudFront assigns the trust store's own id at create time; the name argument is client-chosen but is not the import identity.",
		"ID", "id"),
	serverAssigned("aws_cloudfront_vpc_origin",
		"CloudFront assigns the VPC origin's own id at create time; the vpc_origin_endpoint_config block configures it but does not identify it.",
		"ID", "id"),

	// aws_cloudfront_function: row-gen itself declines to propose a
	// pastable row (evidence-only — the provider has not adopted the newer
	// Identity Schema plugin feature for this type in v6.58.0, so
	// live/survey-full.json shows no identity schema at all), but
	// live/import-grammar.json's docs-derived evidence and
	// live/registry.json's own AWS::CloudFront::Function row (primaryIdentifier
	// ["Name"], in createOnlyProperties and not in readOnlyProperties) agree
	// with each other and with the provider's documented import command
	// (terraform import aws_cloudfront_function.test my_test_function):
	// client-named, the "name" argument verbatim. The provider's own
	// Attribute Reference does not state that id equals name, so id is not
	// claimed as an identity source here — the same standard of care
	// aws_route's synthesized id gets.
	TypeIdentity{
		Type:          "aws_cloudfront_function",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[UserId], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (user_id, Required, alongside access_string,
		// engine and user_name) and its documented import command
		// (terraform import aws_elasticache_user.my_user userId1).
		Type:          "aws_elasticache_user",
		Components:    []Component{attr("user_id")},
		ImportSyntax:  "USER_ID",
		IdentityAttrs: []string{"user_id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[UserGroupId], in
		// createOnlyProperties and not in readOnlyProperties —
		// client-named, proposed correctly; argument from
		// live/import-grammar.json. Confirmed against the provider's own
		// Argument Reference (user_group_id, Required, alongside engine)
		// and its documented import command (terraform import
		// aws_elasticache_user_group.my_user_group userGoupId1).
		Type:          "aws_elasticache_user_group",
		Components:    []Component{attr("user_group_id")},
		ImportSyntax:  "USER_GROUP_ID",
		IdentityAttrs: []string{"user_group_id"},
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

	// ---- Registry-ratified (#40, #44): fourth batch, RDS (issue #65's
	// ---- ratification campaign) -----------------------------------------
	//
	// Same pipeline as the earlier batches: every row started as a
	// tools/row-gen proposal from live/registry.json's RDS section (18
	// proposals), cross-checked against the AWS provider's documented
	// import behaviour at the pinned v6.58.0 tag
	// (raw.githubusercontent.com/hashicorp/terraform-provider-aws/v6.58.0/website/docs/r/*.html.markdown)
	// rather than accepted on the registry's classification alone. Cohort
	// estate: live/e2e/estates/rds.
	//
	// Five of the seventeen ratified rows are corrections, the same shape
	// as the messaging batch's aws_sns_topic_policy: row-gen filed them
	// "evidence-only" or "needs hand separator" because the registry's own
	// primaryIdentifier/readOnlyProperties evidence did not resolve them,
	// but the provider's own Import section names a concrete, documented
	// grammar built entirely from arguments already in configuration —
	// aws_db_proxy_default_target_group, aws_db_proxy_endpoint,
	// aws_db_instance_role_association, aws_rds_cluster_role_association and
	// aws_rds_global_cluster below. One proposal is rejected outright
	// (aws_db_proxy_target): its documented import string embeds a literal
	// segment ("RDS_INSTANCE" vs. "TRACKED_CLUSTER") chosen by *which* of two
	// optional arguments a config sets, a conditional-literal component this
	// table's vocabulary does not have, the same "needs a component this
	// table's vocabulary does not have yet" shape as the messaging batch's
	// aws_cloudwatch_event_rule rejection.
	//
	// aws_db_instance keeps live/SURVEY.md's own recorded wrinkle (the
	// "third wrinkle" in that file's "Classification wrinkles" section): the
	// original survey filed it under marker on taggability alone, because
	// v6.58.0 ships it no identity schema and no list resource, but its
	// documented import ID is the client-chosen "identifier" argument, so it
	// wires client-named here, exactly as that file predicted a batch that
	// reached RDS would do. Its own "id" attribute is the RDS DBI resource
	// ID, a distinct provider-minted value the provider's own Attribute
	// Reference lists separately from "identifier" — unlike
	// aws_rds_cluster_instance below, whose "id" and "identifier" attributes
	// are documented as the same string — so "id" is deliberately not
	// claimed as an identity source here, the same standard of care as
	// aws_ecs_cluster's synthesized id.
	//
	// aws_db_instance is also this batch's emulator caveat, the same
	// deliberate stance as the messaging batch's aws_sqs_queue: floci needs
	// the Docker socket mounted into its container to serve RDS at all
	// (lex00/floci#28), and neither the gated Go test harness
	// (internal/live/flocitest.flocitest.go), the shell e2e harness
	// (live/e2e/run.sh) nor any cohort README's "Verifying by hand" `docker
	// run` command mounts it as of this batch — confirmed by inspection, not
	// merely carried over from live/SURVEY.md's note. The type ratifies on
	// paper: its identity is sound and independently verified against the
	// provider's docs regardless of what any one emulator can run. See
	// live/e2e/estates/rds/README.md's "Verifying by hand" section for the
	// caveat recorded the way aws_sqs_queue's is.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_db_proxy_target: row-gen filed this evidence-only (a fold
	//     child of aws_db_proxy_default_target_group with no registry
	//     primaryIdentifier of its own). The provider's documented import ID
	//     is "db_proxy_name/target_group_name/type/resource_identifier",
	//     where db_proxy_name and target_group_name are both configured
	//     arguments and resource_identifier is whichever of
	//     db_instance_identifier or db_cluster_identifier a config sets
	//     (idlessAttr's alternation-list shape handles that part fine) — but
	//     "type" is the literal string "RDS_INSTANCE" or "TRACKED_CLUSTER"
	//     chosen by *which* of those two optional arguments is set, not a
	//     value any argument carries and not a fixed separator either. No
	//     [Component] in this table's vocabulary expresses "a literal
	//     conditioned on which alternative matched", so this stays a
	//     needs-hand-separator case rather than a guess this batch writes
	//     blind, the same stance as the messaging batch's two rejections.
	//
	// Not this batch's to decide: aws_db_proxy_target's own true fold
	// children (aws_db_snapshot, aws_db_cluster_snapshot,
	// aws_rds_cluster_endpoint, and the rest of the RDS resource family
	// row-gen classifies "marker" by taggability alone rather than proposing
	// a pastable row) carry no registry evidence at all and are simply
	// outside this batch's scope, the same as the messaging batch's Logs and
	// Events family.

	TypeIdentity{
		// registry.json: primaryIdentifier=[SubscriptionName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_event_subscription.default
		// rds-event-sub) and its Attribute Reference, which states id is
		// "The name of the RDS event notification subscription" — the same
		// name argument verbatim.
		Type:          "aws_db_event_subscription",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	serverAssigned("aws_kinesis_stream_consumer",
		"Kinesis mints the stream consumer's ARN at create time, embedding a creation timestamp it assigns itself; the name argument names the consumer but not one registration of it against a stream.",
		"STREAMARN/consumer/CONSUMERNAME:TIMESTAMP", "arn", "id"),

	TypeIdentity{
		// row-gen proposed aws_kinesis_firehose_delivery_stream client-named
		// via "arn" (registry primaryIdentifier=[DeliveryStreamName], but
		// the argument line row-gen actually emitted came from the
		// provider's own identity schema, live/survey-full.json:
		// required_for_import=[arn]) — right about which value the provider
		// needs, wrong about it being a plain configuration argument: the
		// pinned provider's wire schema shows arn is computed-only, never
		// settable, so attr("arn") would find nothing in a real resource
		// block. The provider's documented import command (terraform import
		// aws_kinesis_firehose_delivery_stream.example
		// arn:aws:firehose:us-east-1:123456789012:deliverystream/example-delivery-stream)
		// confirms the ARN is this predictable account-derived shape, the
		// same correction the messaging batch made to aws_sqs_queue above:
		// reconstructed from the required "name" argument plus the run's
		// own region and account, not read as a literal "arn" argument.
		Type: "aws_kinesis_firehose_delivery_stream",
		Components: []Component{
			inAttr("arn", sep("arn:aws:firehose:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":deliverystream/")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:firehose:REGION:ACCOUNT:deliverystream/NAME",
		IdentityAttrs: []string{"arn", "id"},
	},

	TypeIdentity{
		// row-gen classed this evidence-only (argument "database_name"
		// GUESSED from the CFN property name — v6.58.0 ships no identity
		// schema for this type). Independently confirmed against the
		// provider's documented import command (terraform import
		// aws_glue_catalog_database.database 123456789012:my_database) and
		// its Argument/Attribute Reference: the real argument is "name",
		// not "database_name", and "catalog_id" is optional, defaulting to
		// the run's own AWS account ID when omitted — an account-derived
		// shape, not a plain client-named row. The pinned provider's wire
		// schema confirms catalog_id is optional+computed and id is
		// "Catalog ID and name of the database".
		Type: "aws_glue_catalog_database",
		Components: []Component{
			inAttr("id", cloud(CloudAccountID)),
			inAttr("id", sep(":")),
			inAttr("id", attr("name")),
		},
		ImportSyntax:  "CATALOG_ID:NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque,
		// undocumented "Id" (AWS::Glue::Table's primaryIdentifier) —
		// rejected as a proposal, the same registry/provider mismatch shape
		// the earlier batches' rejections found. The provider's documented
		// import command (terraform import aws_glue_catalog_table.MyTable
		// 123456789012:MyDatabase:MyTable) and its Attribute Reference
		// ("id - Catalog ID, database name, and table name, separated by
		// colons") show the identity is fully reconstructed from
		// configuration: the same account-derived catalog_id as
		// aws_glue_catalog_database just above, plus the required, already
		// admitted parent's database_name and this resource's own required
		// name. Untaggable — the pinned provider's wire schema carries no
		// tags argument for this type at all.
		Type: "aws_glue_catalog_table",
		Components: []Component{
			inAttr("id", cloud(CloudAccountID)),
			inAttr("id", sep(":")),
			inAttr("id", attr("database_name")),
			inAttr("id", sep(":")),
			inAttr("id", attr("name")),
		},
		ImportSyntax:  "CATALOG_ID:DATABASE_NAME:TABLE_NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed aws_glue_registry server-assigned via the
		// registry's opaque "Arn" — right that the provider's documented
		// import command (terraform import aws_glue_registry.example
		// arn:aws:glue:us-west-2:123456789012:registry/example) uses the
		// ARN, but the flat serverAssigned() template undersells it, the
		// same correction as aws_kinesis_firehose_delivery_stream above:
		// the ARN is the predictable
		// arn:aws:glue:REGION:ACCOUNT:registry/NAME shape, built from the
		// required "registry_name" argument plus the run's region and
		// account, not an opaque server mint.
		Type: "aws_glue_registry",
		Components: []Component{
			inAttr("arn", sep("arn:aws:glue:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":registry/")),
			inAttr("arn", attr("registry_name")),
		},
		ImportSyntax:  "arn:aws:glue:REGION:ACCOUNT:registry/REGISTRY_NAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named. Confirmed against
		// the provider's own identity schema (live/survey-full.json:
		// required_for_import=[name]) and against the documented import
		// command (terraform import aws_glue_job.example example), which
		// sets id to the job name.
		Type:          "aws_glue_job",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	serverAssigned("aws_backup_plan",
		"Backup mints the plan's own id at create time, a UUID distinct from the client-chosen name; the provider's Attribute Reference documents id as \"the id of the backup plan,\" separate from name and from version.",
		"BACKUPPLANID", "id"),

	TypeIdentity{
		// Confirmed against the provider's documented import command
		// (terraform import aws_backup_vault.test-vault TestVault) and its
		// Attribute Reference, which states id is "the name of the vault."
		Type:          "aws_backup_vault",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, argument sourced
		// from live/import-grammar.json. Confirmed against the provider's
		// documented import command (terraform import
		// aws_glue_crawler.MyJob MyJob) and Attribute Reference ("id -
		// Crawler name").
		Type:          "aws_glue_crawler",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBInstanceIdentifier], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// The provider ships no identity schema for this type in v6.58.0 (see
		// live/SURVEY.md's "third wrinkle"), but its documented import
		// command (terraform import aws_db_instance.default
		// mydb-rds-instance) and Argument Reference ("identifier - (Optional)
		// The name of the RDS instance, if omitted, Terraform will assign a
		// random, unique identifier") confirm the client-named shape
		// row-gen proposed. Its own "id" attribute is "RDS DBI resource ID"
		// per the Attribute Reference — a distinct provider-minted value,
		// not the identifier — so "id" is deliberately not claimed as an
		// identity source here. See this section's banner comment above for
		// the emulator caveat (lex00/floci#28) this type ratifies despite.
		Type:          "aws_db_instance",
		Components:    []Component{attr("identifier")},
		ImportSyntax:  "IDENTIFIER",
		IdentityAttrs: []string{"identifier"}, // "id" intentionally omitted: id is the DBI resource ID, not the identifier
	},
	TypeIdentity{
		// row-gen filed this evidence-only: registry.json's primaryIdentifier
		// (TargetGroupArn) is entirely a readOnlyProperties field, so its own
		// classify rule refuses a pastable row, noting only "import docs show
		// argument-composed ID". Reading that import section directly: the
		// documented command (terraform import
		// aws_db_proxy_default_target_group.example example) imports "using
		// the db_proxy_name" — a named-singleton child of aws_db_proxy, the
		// same shape as aws_sns_topic_policy in the messaging batch, keyed on
		// the parent's own name argument rather than an opaque ARN the
		// registry's primaryIdentifier names. The provider's own Attribute
		// Reference confirms it: "id - Name of the RDS DB Proxy" — the
		// exported "name" attribute is the target group's own fixed name
		// ("default"), a different thing, and is not claimed as an identity
		// source here.
		Type:          "aws_db_proxy_default_target_group",
		Components:    []Component{attr("db_proxy_name")},
		ImportSyntax:  "DB_PROXY_NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen filed this evidence-only: registry.json's guessed argument
		// name (db_proxy_endpoint_name) is "not backed by a provider identity
		// schema or the carve seed", so its own rules refuse a pastable row.
		// Reading the import section directly resolves it cleanly: the
		// documented import ID is "DB-PROXY-NAME/DB-PROXY-ENDPOINT-NAME", a
		// concrete composite of two arguments the Argument Reference marks
		// Required (db_proxy_name, db_proxy_endpoint_name) — the same
		// concrete-composite shape as aws_iam_role_policy_attachment. The
		// Attribute Reference confirms "id" is exactly that composite ("The
		// name of the proxy and proxy endpoint separated by /"), so id is
		// claimed as an identity source, the same standard of care as
		// aws_iam_role_policy's colon-joined id.
		Type: "aws_db_proxy_endpoint",
		Components: []Component{
			attr("db_proxy_name"),
			sep("/"),
			attr("db_proxy_endpoint_name"),
		},
		ImportSyntax:  "DB-PROXY-NAME/DB-PROXY-ENDPOINT-NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[OptionGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_option_group.example
		// mysql-option-group) and its Attribute Reference ("id - DB option
		// group name").
		Type:          "aws_db_option_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBParameterGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_parameter_group.rds_pg rds-pg) and its
		// Attribute Reference ("id - The db parameter group name").
		Type:          "aws_db_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// Corrected from row-gen's server-assigned FrameworkArn proposal —
		// see the rejected/corrected note above. Confirmed against the
		// provider's documented import command (terraform import
		// aws_backup_framework.test <id>, where the doc states id
		// "corresponds to the name of the Backup Framework") and its
		// Argument Reference, which requires name.
		Type:          "aws_backup_framework",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen classed this evidence-only (no identity schema in
		// v6.58.0). Independently confirmed against the provider's
		// documented import command (terraform import
		// aws_glue_connection.MyConnection 123456789012:MyConnection) and
		// Attribute Reference ("id - Catalog ID and name of the
		// connection"): the same account-derived catalog_id:name shape as
		// aws_glue_catalog_database above, not a plain client-named row.
		Type: "aws_glue_connection",
		Components: []Component{
			inAttr("id", cloud(CloudAccountID)),
			inAttr("id", sep(":")),
			inAttr("id", attr("name")),
		},
		ImportSyntax:  "CATALOG_ID:NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed aws_glue_classifier server-assigned via the
		// registry's opaque "Id" (AWS::Glue::Classifier's primaryIdentifier,
		// with an empty createOnlyProperties list — the polymorphic
		// Grok/XML/JSON/CSV classifier shapes defeat the registry's own
		// top-level modeling) — rejected as a proposal, the same
		// registry/provider mismatch shape as aws_lambda_alias and
		// aws_cloudwatch_alarm_mute_rule. The provider's documented import
		// command (terraform import aws_glue_classifier.MyClassifier
		// MyClassifier) and Attribute Reference ("id - Name of the
		// classifier") show the identity is the required "name" argument,
		// already in configuration. Untaggable — the pinned provider's wire
		// schema carries no tags argument for this type at all.
		Type:          "aws_glue_classifier",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBProxyName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_db_proxy.example example) and its Argument
		// Reference ("name" is Required, not merely settable). Unlike the
		// types above, its own "id" attribute is documented as "The Amazon
		// Resource Name (ARN) for the proxy" — a different value from name —
		// so "id" is deliberately not claimed as an identity source here,
		// the same standard of care as aws_ecs_cluster's synthesized id.
		Type:          "aws_db_proxy",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted: id is the proxy's ARN, not name
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBSubnetGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// The provider ships an identity schema for this type
		// (required_for_import: name), which live/survey-full.json's own
		// mechanical pass reads as "needs-config-signal" because name is
		// settable but not a schema-Required argument (Optional, Terraform
		// assigns a random name when omitted) — the same shape
		// aws_s3_bucket's own "bucket" argument already has among the types
		// this table admits unconditionally. Confirmed against the
		// provider's documented import command (terraform import
		// aws_db_subnet_group.default production-subnet-group) and its
		// Attribute Reference ("id - The db subnet group name").
		Type:          "aws_db_subnet_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// Corrected from row-gen's server-assigned ReportPlanArn proposal —
		// the same shape and the same corroborating sentence in the
		// provider's docs as aws_backup_framework above.
		Type:          "aws_backup_report_plan",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBClusterIdentifier], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Same "needs-config-signal" mechanical classification as
		// aws_db_subnet_group above, for the same reason
		// (cluster_identifier is Optional; Terraform assigns a random one
		// when omitted), overridden here the same way. Confirmed against the
		// provider's documented import command (terraform import
		// aws_rds_cluster.aurora_cluster aurora-prod-cluster) and its
		// Attribute Reference ("id - RDS Cluster Identifier").
		Type:          "aws_rds_cluster",
		Components:    []Component{attr("cluster_identifier")},
		ImportSyntax:  "CLUSTER_IDENTIFIER",
		IdentityAttrs: []string{"id", "cluster_identifier"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBInstanceIdentifier] (this type
		// maps to the same AWS::RDS::DBInstance CFN type as aws_db_instance
		// above), in createOnlyProperties and not in readOnlyProperties —
		// client-named. Confirmed against the provider's documented import
		// command (terraform import
		// aws_rds_cluster_instance.prod_instance_1
		// aurora-cluster-instance-1) and its Attribute Reference, which lists
		// both "identifier" and "id" as "Instance identifier" — the same
		// string — unlike aws_db_instance above, where id is a distinct
		// DBI resource ID. "id" is claimed as an identity source here for
		// exactly that reason.
		Type:          "aws_rds_cluster_instance",
		Components:    []Component{attr("identifier")},
		ImportSyntax:  "IDENTIFIER",
		IdentityAttrs: []string{"id", "identifier"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBClusterParameterGroupName], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_rds_cluster_parameter_group.cluster_pg
		// production-pg-1) and its Attribute Reference ("id - The db cluster
		// parameter group name").
		Type:          "aws_rds_cluster_parameter_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	// ---- Registry-ratified (#40, #44): fourth batch, ECS and EKS
	// ---- (issue #65) ------------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own
	// website/docs/r/ source at the pinned v6.58.0 tag), not accepted on the
	// registry's classification alone. Six of row-gen's nine EKS proposals
	// were "needs hand separator" (a composite primaryIdentifier with no
	// separator in any schema, issue #44's own non-goal); this batch resolved
	// five of those six by hand from the provider's own documented import
	// grammar rather than the registry's (live/import-grammar.json, issue
	// #65's own note that this artifact "largely resolves" the needs-hand-
	// separator backlog). Cohort estate: live/e2e/estates/ecs-eks.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_ecs_capacity_provider: row-gen proposed client-named via the
	//     registry's createOnlyProperties "Name" — the same
	//     registry-says-client-named-but-the-provider-disagrees shape the
	//     earlier batches' rejections established. The provider's own
	//     identity schema requires the server-assigned arn
	//     (arn:aws:ecs:REGION:ACCOUNT:capacity-provider/NAME), not name, and
	//     its documented import command confirms it. Even granting
	//     server-assigned status, v6.58.0 ships this type with no native
	//     list resource (live/survey-full.json:
	//     aws_ecs_capacity_provider.signals.list_resource is false), the
	//     same gap that keeps aws_efs_file_system out of the marker cohort
	//     above: a tag-filtered list needs something to list.
	//   - aws_ecs_daemon_task_definition: the same family+server-assigned-
	//     revision shape as aws_ecs_task_definition below, one section
	//     later in the provider's own docs (ECS's new daemon-scheduling
	//     sibling of the ordinary task definition). Rejected for the same
	//     reason: see aws_ecs_task_definition's own entry.
	//   - aws_ecs_express_gateway_service: v6.58.0 ships this type with no
	//     identity schema at all (no "Identity Schema" heading in its own
	//     doc, unlike every other type in this section), its service_name
	//     argument is Optional and Terraform-generated when omitted, and
	//     row-gen's own enumeration story calls it flatly "not listable" —
	//     three independent reasons, any one of which alone would keep a
	//     type out of the four admission paths, and here all three hold at
	//     once.
	//   - aws_ecs_service: live/SURVEY.md's curated-68 row calls this type
	//     client-named ("cluster + name, the cluster itself client-named"),
	//     and its provider identity schema does require exactly those two
	//     names. But the resource's own Argument Reference documents
	//     `cluster` as "(Optional) ARN of an ECS cluster" — accepting an
	//     ARN — while the identity schema's `cluster` field is documented
	//     as "The name of the cluster": the same argument name, two
	//     different shapes. The type's own Example Usage sets
	//     `cluster = aws_ecs_cluster.foo.id`, and this table's own
	//     aws_ecs_cluster entry below records that id is the cluster's ARN,
	//     not its name — the idiomatic form of this exact argument would
	//     silently build a wrong identity (an ARN where the import grammar
	//     wants a bare name) rather than fail visibly. A hand composite that
	//     cannot tell which shape a given configuration used is a guess,
	//     not evidence; this needs a config-signal check (an argument that
	//     names the cluster by aws_ecs_cluster.foo.name specifically) this
	//     batch does not attempt, the same non-goal boundary the messaging
	//     batch's aws_cloudwatch_event_rule rejection drew.
	//   - aws_ecs_task_definition: SURVEY.md's own curated-68 row records
	//     this type's shape as "family + revision, the revision assigned
	//     server-side per registration" and groups it among the five rows
	//     its wrinkles section admits neither derivation nor a marker
	//     recovers. The ARN embeds family:revision, and revision is not a
	//     configuration argument at all — the Attribute Reference exports it
	//     read-only, incrementing by one on every new registration of the
	//     same family. That rules out client-naming (revision is never in
	//     configuration) and, less obviously, rules out the marker path
	//     too: every revision of one family is a distinct live object, but
	//     ECS does not vary a task definition's tags by revision, so a
	//     tag-filtered list would return every revision under one identical
	//     tag set with nothing left to tell them apart — the marker path's
	//     one-live-object-per-tag-set assumption, sound for every admitted
	//     marker type above, breaks here. A shape outside the four admission
	//     paths, honestly; rejected rather than forced into either one.
	//   - aws_ecs_task_set: row-gen's own needs-hand-separator note points at
	//     a three-part primaryIdentifier (Cluster, Service, Id), and the
	//     provider's own Import section confirms the shape:
	//     ecs-svc/DEPLOYMENTID,SERVICEARN,CLUSTERARN. The comma separator is
	//     no longer the obstacle live/import-grammar.json resolves for the
	//     five EKS composites below — the DEPLOYMENTID segment is, since it
	//     is server-assigned with no configuration argument or previously
	//     admitted parent's identity attribute that supplies it (unlike
	//     aws_route53_record's zone_id, which comes from an already-resolved
	//     parent). Compounding it, both `cluster` and `service` are
	//     documented as "Short name or ARN" — the same argument-accepts-
	//     either-shape ambiguity that rejected aws_ecs_service above, twice
	//     over in one type.
	//   - aws_eks_identity_provider_config: row-gen's needs-hand-separator
	//     note and live/import-grammar.json's own separator (":") both
	//     resolve cleanly — cluster_name and identity_provider_config_name,
	//     colon-joined, the same shape as aws_eks_addon below. But
	//     identity_provider_config_name is not a top-level argument of this
	//     resource: the provider's Argument Reference nests it inside the
	//     required `oidc` block (oidc.identity_provider_config_name), and
	//     every Component this table has ever built - every attr() call in
	//     this file - reads a top-level resource argument
	//     ([resolver.identityArgs] builds its schema from top-level
	//     hcl.AttributeSchema entries only). This table's vocabulary cannot
	//     honestly express an identity sourced from inside a nested block
	//     without inventing that capability, which is a mechanism change,
	//     not a ratification; rejected rather than forced.
	//   - aws_eks_pod_identity_association: the identity requires
	//     cluster_name (a required argument) plus association_id, which is
	//     not a configuration argument at all - the provider mints it and
	//     the Attribute Reference documents it as "The ID of the
	//     association", read-only. Server-assigned, so this needs the
	//     marker path; the type is taggable, but v6.58.0 ships it with no
	//     native list resource (live/survey-full.json:
	//     aws_eks_pod_identity_association.signals.list_resource is false),
	//     the same aws_efs_file_system gap that keeps
	//     aws_ecs_capacity_provider out above: nothing enumerates it.
	//
	// A note on floci, not on any of the rejections above: EKS cluster
	// creation is unsupported by the pinned floci image (lex00/floci#27,
	// still open), so nothing in this cohort that names a cluster_name
	// argument could be apply-verified against the emulator this batch ran
	// - see live/e2e/estates/ecs-eks/README.md, "Verifying by hand". Per
	// issue #65's own recipe ("apply against the pinned floci image where it
	// serves the types, gaps documented in the cohort README, not
	// blocking"), that gap is documented rather than treated as a reason to
	// leave aws_eks_cluster and its five EKS dependents unratified - the
	// same standard the messaging batch applied to aws_sqs_queue's own open
	// floci gap.

	serverAssigned("aws_ecs_daemon",
		"ECS mints the daemon's ARN at create time; the name argument is client-chosen but the documented import identity is the ARN (arn:aws:ecs:REGION:ACCOUNT:daemon/CLUSTER/NAME), which also embeds the cluster and region rather than reducing to a bare name.",
		"arn:aws:ecs:REGION:ACCOUNT:daemon/CLUSTER/NAME", "arn"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[Cluster], in
		// createOnlyProperties and not in readOnlyProperties — client-named,
		// and row-gen proposed it correctly. Confirmed against the
		// provider's documented import command (terraform import
		// aws_ecs_cluster_capacity_providers.example my-cluster) and its own
		// Attribute Reference ("id - Same as cluster_name"). No "Identity
		// Schema" heading in the provider's own doc at all — v6.58.0 ships
		// this type with no identity schema, the same docs-tier evidence
		// aws_ecs_cluster's own entry above rests on — and no tags argument
		// either: a named singleton child of the cluster, the same shape as
		// aws_s3_bucket_policy, concrete whenever the cluster is.
		Type:          "aws_ecs_cluster_capacity_providers",
		Components:    []Component{attr("cluster_name")},
		ImportSyntax:  "CLUSTER_NAME",
		IdentityAttrs: []string{"id", "cluster_name"},
	},

	TypeIdentity{
		// live/import-grammar.json: separator ":", arguments
		// [cluster_name, principal_arn], both required. Confirmed against
		// the provider's own Identity Schema (required: cluster_name,
		// principal_arn) and its documented import command
		// (example-cluster:arn:aws:iam::123456789012:role/example). Neither
		// argument is optional, so this is concrete in any realistic
		// config, the same iam_role_policy-style composite as
		// aws_iam_role_policy_attachment. The Attribute Reference exports
		// access_entry_arn, created_at and modified_at — no "id" at all —
		// so no attribute is claimed as an identity source.
		Type: "aws_eks_access_entry",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("principal_arn"),
		},
		ImportSyntax:  "CLUSTERNAME:PRINCIPALARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// live/import-grammar.json flags this row's separator "unsure", but
		// the provider's own doc names it plainly: cluster_name,
		// principal_arn and policy_arn, octothorp-joined
		// (example-cluster#arn:...#arn:...), all three required arguments
		// per the Identity Schema. Untaggable — the Argument Reference
		// carries no tags block at all — so this joins
		// untaggableAdmittedTypes in internal/live/stamp alongside
		// aws_lb_target_group_attachment, the same shape: a composite of
		// three client-supplied values with no marker to fall back to.
		Type: "aws_eks_access_policy_association",
		Components: []Component{
			attr("cluster_name"),
			sep("#"),
			attr("principal_arn"),
			sep("#"),
			attr("policy_arn"),
		},
		ImportSyntax:  "CLUSTERNAME#PRINCIPALARN#POLICYARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// live/import-grammar.json: separator ":", arguments
		// [addon_name, cluster_name], both required per the Identity
		// Schema. The Attribute Reference documents id explicitly: "EKS
		// Cluster name and EKS Addon name separated by a colon (:)" —
		// cluster_name first, matching the documented import command
		// (example-cluster:example-addon) and this entry's own component
		// order.
		Type: "aws_eks_addon",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("addon_name"),
		},
		ImportSyntax:  "CLUSTERNAME:ADDONNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// live/import-grammar.json: separator ",", arguments
		// [capability_name, cluster_name], both required per the Identity
		// Schema (cluster_name, capability_name) and the documented import
		// command (example-cluster,example-capability). A newer EKS
		// resource (GitOps capabilities: ArgoCD, ACK, KRO) outside
		// live/SURVEY.md's curated 68, the same standing as
		// aws_lambda_layer_version and aws_cloudwatch_dashboard before it.
		// The Attribute Reference exports arn, configuration.*, tags_all
		// and version — no "id" — so no attribute is claimed as an
		// identity source.
		Type: "aws_eks_capability",
		Components: []Component{
			attr("cluster_name"),
			sep(","),
			attr("capability_name"),
		},
		ImportSyntax:  "CLUSTERNAME,CAPABILITYNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, proposed correctly.
		// Confirmed against the provider's own Identity Schema (required:
		// name) and its Attribute Reference ("id - Name of the cluster").
		// live/SURVEY.md's curated-68 row already reaches "client-named";
		// its "blocked-emulator" status is a floci gap (EKS cluster
		// creation, lex00/floci#27, still open), not an identity gap — see
		// the floci note above this section.
		Type:          "aws_eks_cluster",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen's argument line named the literal doc token "CATALOG-ID",
		// not a real configuration argument — evidence-only, never a
		// pastable row. The provider's real argument is "catalog_id",
		// optional, defaulting to the run's own AWS account ID when
		// omitted (the same default aws_glue_catalog_database and
		// aws_glue_connection above share), and its Attribute Reference
		// sets id to that same catalog ID — a singleton-per-account shape
		// like the IAM/ECR batch's aws_ecr_registry_policy, not a
		// discovered one. Untaggable — the pinned provider's wire schema
		// carries no tags argument for this type at all.
		Type:          "aws_glue_data_catalog_encryption_settings",
		Components:    []Component{inAttr("id", cloud(CloudAccountID))},
		ImportSyntax:  "CATALOG_ID",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], in createOnlyProperties
		// and not in readOnlyProperties — client-named, argument sourced
		// from live/import-grammar.json. Confirmed against the provider's
		// documented import command (terraform import
		// aws_glue_trigger.MyTrigger MyTrigger) and Attribute Reference
		// ("id - Trigger name").
		Type:          "aws_glue_trigger",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json proposed this one correctly (client-named via
		// name). Confirmed against the provider's documented import
		// command (terraform import aws_backup_restore_testing_plan.example
		// my_testing_plan); the provider's own schema pull has no id
		// attribute for this type at all, so none is claimed here.
		Type:          "aws_backup_restore_testing_plan",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed nothing pastable here at all — its own
		// argument-name guess (backup_vault_name) was unbacked by any
		// schema, so this surfaced evidence-only per #44's non-goals. The
		// provider's rendered docs resolve it: "the `id` ... The name of
		// the Logically Air Gapped Backup Vault," with name required. See
		// the corrected note above.
		Type:          "aws_backup_logically_air_gapped_vault",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},

	serverAssigned("aws_glue_ml_transform",
		"Glue assigns the ML transform's ID (tfm-…) at create time; the name argument names the transform but the API accepts only the ID as an identity.",
		"tfm-ID", "id"),

	TypeIdentity{
		// row-gen classed this evidence-only (argument "name" GUESSED from
		// the CFN property name — v6.58.0 ships no identity schema for this
		// type). Independently confirmed against the provider's documented
		// import command (terraform import aws_athena_workgroup.example
		// example) and Attribute Reference ("id - Workgroup name").
		Type:          "aws_athena_workgroup",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen classed this evidence-only (argument "name" GUESSED from
		// the CFN property name — v6.58.0 ships no identity schema for this
		// type). Independently confirmed against the provider's documented
		// import command (terraform import aws_athena_data_catalog.example
		// example-data-catalog) and Attribute Reference ("id - Name of the
		// data catalog").
		Type:          "aws_athena_data_catalog",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen filed this evidence-only (a fold child of aws_rds_cluster
		// with no registry primaryIdentifier of its own). Reading the
		// provider's Import section directly: the documented import ID is
		// "DB Cluster Identifier and IAM Role ARN separated by a comma", a
		// concrete composite of two arguments the Argument Reference marks
		// Required (db_cluster_identifier, role_arn) — the same
		// concrete-composite shape as aws_iam_role_policy. The Attribute
		// Reference confirms "id" is exactly that composite, so id is
		// claimed as an identity source.
		Type: "aws_rds_cluster_role_association",
		Components: []Component{
			attr("db_cluster_identifier"),
			sep(","),
			attr("role_arn"),
		},
		ImportSyntax:  "DBCLUSTERIDENTIFIER,ROLEARN",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen refused a pastable row outright ("the composite separator
		// is not registry evidence; a human chooses it") because
		// primaryIdentifier=[Engine, EngineVersion] is a composite with no
		// separator in any schema. Reading the provider's Import section
		// resolves it: the documented import ID is "engine and engine_version
		// separated by a colon", and both halves are Required arguments
		// already in configuration — the same concrete-composite shape as
		// aws_iam_role_policy's ROLENAME:POLICYNAME. The provider's own
		// Attribute Reference exports no "id" at all for this type, so this
		// imports by string only, like aws_route_table_association; nothing
		// is claimed as an identity source.
		Type: "aws_rds_custom_db_engine_version",
		Components: []Component{
			attr("engine"),
			sep(":"),
			attr("engine_version"),
		},
		ImportSyntax:  "ENGINE:ENGINE_VERSION",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this evidence-only: the registry's own primaryIdentifier
		// (GlobalClusterIdentifier) is in createOnlyProperties, which would
		// ordinarily propose client-named, but row-gen's own rule flags the
		// argument name as "GUESSED: snake_cased CFN property name, not
		// backed by a provider identity schema or the carve seed" and
		// refuses a pastable row on that basis alone. The provider's own
		// Argument Reference resolves the guess directly: "global_cluster_identifier
		// - (Required, Forces new resources)" is exactly that argument, no
		// snake-casing inference needed, and its Attribute Reference confirms
		// "id - RDS Global Cluster identifier" is the same string. Confirmed
		// against the documented import command (terraform import
		// aws_rds_global_cluster.example example).
		Type:          "aws_rds_global_cluster",
		Components:    []Component{attr("global_cluster_identifier")},
		ImportSyntax:  "GLOBAL_CLUSTER_IDENTIFIER",
		IdentityAttrs: []string{"id", "global_cluster_identifier"},
	},
	serverAssigned("aws_rds_integration",
		"the RDS service assigns the integration's own ARN at create time (Amazon Resource Name (ARN) of the Integration); integration_name, source_arn and target_arn together name what it connects, not the integration resource itself.",
		"ARN", "arn", "id"),
	// aws_rds_integration: registry.json's primaryIdentifier (IntegrationArn)
	// is entirely a readOnlyProperties field, matching row-gen's
	// server-assigned proposal. Confirmed against the provider's documented
	// import command (terraform import aws_rds_integration.example
	// arn:aws:rds:us-west-2:123456789012:integration:abcdefgh-...) and its
	// Attribute Reference, which lists both "arn" and a deprecated "id"
	// alias of the same ARN.

	TypeIdentity{
		// row-gen filed this evidence-only (a fold child of aws_db_instance
		// with no registry primaryIdentifier of its own). Reading the
		// provider's Import section directly: the documented import ID is
		// "DB Instance Identifier and IAM Role ARN separated by a comma", a
		// concrete composite of two arguments the Argument Reference marks
		// Required (db_instance_identifier, role_arn) — the same shape as
		// aws_rds_cluster_role_association above. The Attribute Reference
		// confirms "id" is exactly that composite, so id is claimed as an
		// identity source.
		Type: "aws_db_instance_role_association",
		Components: []Component{
			attr("db_instance_identifier"),
			sep(","),
			attr("role_arn"),
		},
		ImportSyntax:  "DBINSTANCEIDENTIFIER,ROLEARN",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[DBShardGroupIdentifier], in
		// createOnlyProperties and not in readOnlyProperties — client-named.
		// Confirmed against the provider's documented import command
		// (terraform import aws_rds_shard_group.example
		// example-shard-group) and its Argument Reference
		// ("db_shard_group_identifier" is Required, with no
		// Terraform-assigned fallback, unlike every *_group name above). Its
		// Attribute Reference exports no "id" attribute at all (only arn,
		// db_shard_group_resource_id, endpoint), so nothing is claimed as an
		// identity source beyond the argument itself.
		Type:          "aws_rds_shard_group",
		Components:    []Component{attr("db_shard_group_identifier")},
		ImportSyntax:  "DB_SHARD_GROUP_IDENTIFIER",
		IdentityAttrs: []string{"db_shard_group_identifier"},
	},
	TypeIdentity{
		// live/import-grammar.json: separator ":", arguments
		// [cluster_name, fargate_profile_name], both required per the
		// Identity Schema. The Attribute Reference documents id explicitly:
		// "EKS Cluster name and EKS Fargate Profile name separated by a
		// colon (:)", matching the documented import command
		// (example-cluster:example-profile).
		Type: "aws_eks_fargate_profile",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("fargate_profile_name"),
		},
		ImportSyntax:  "CLUSTERNAME:FARGATEPROFILENAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// live/survey-full.json classes this needs-config-signal, not
		// schema-provable: node_group_name is Optional in the resource
		// ("If omitted, Terraform will assign a random, unique name.
		// Conflicts with node_group_name_prefix.") the same
		// Optional+Computed name-generation idiom
		// admissionEvidenceExceptions already carries for aws_s3_bucket,
		// aws_iam_role and aws_iam_instance_profile, extended here to a
		// composite's second half rather than a lone argument, the same way
		// aws_iam_role_policy's own exception already does. Its
		// live/SURVEY.md curated-68 row classes it client-named by hand on
		// that same judgment ("cluster_name + node_group_name"), which this
		// entry follows; a config that sets only node_group_name_prefix (or
		// neither) resolves to ClassNeedsDiscovery honestly at the
		// per-instance level rather than failing this table's own
		// admission. live/import-grammar.json: separator ":", confirmed
		// against the provider's Attribute Reference ("id - EKS Cluster
		// name and EKS Node Group name separated by a colon (:)") and its
		// documented import command (example-cluster:example-group). Status
		// "blocked-emulator" in SURVEY.md is the same open EKS-cluster-
		// creation floci gap as aws_eks_cluster's own entry, not an
		// identity gap.
		Type: "aws_eks_node_group",
		Components: []Component{
			attr("cluster_name"),
			sep(":"),
			attr("node_group_name"),
		},
		ImportSyntax:  "CLUSTERNAME:NODEGROUPNAME",
		IdentityAttrs: []string{"id"},
	},
	// aws_cloudfront_key_value_store: row-gen proposed this correctly the
	// first time (client-named via the provider's own identity schema,
	// required: name), confirmed against the provider's documented import
	// command (terraform import aws_cloudfront_key_value_store.example
	// example_store).
	TypeIdentity{
		Type:          "aws_cloudfront_key_value_store",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},

	// aws_cloudfront_realtime_log_config: row-gen proposed server-assigned
	// via the registry's "Arn" (this type's own primaryIdentifier) — right
	// that the ARN is the identity, but the flat serverAssigned() template
	// undersells it the same way it undersold aws_sns_topic and
	// aws_sqs_queue above: this is the account-derived shape, not an opaque
	// ID. The provider's own Identity Schema requires exactly one field,
	// arn, and its documented import command (terraform import
	// aws_cloudfront_realtime_log_config.example
	// arn:aws:cloudfront::111122223333:realtime-log-config/ExampleNameForRealtimeLogConfig)
	// confirms the value is built from the required "name" argument plus
	// the account of the cloud the run is against — no region segment,
	// because CloudFront is a global service, unlike aws_sns_topic's and
	// aws_sqs_queue's regional ARNs/URLs above.
	TypeIdentity{
		Type: "aws_cloudfront_realtime_log_config",
		Components: []Component{
			inAttr("arn", sep("arn:aws:cloudfront::")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":realtime-log-config/")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:cloudfront::ACCOUNT:realtime-log-config/NAME",
		IdentityAttrs: []string{"arn", "id"},
	},

	// aws_route53_hosted_zone_dnssec: row-gen classified this evidence-only
	// (its "hosted_zone_id" argument name was guessed, not backed by a
	// provider identity schema). Independent verification against the
	// provider's Argument Reference (required: hosted_zone_id, the type's
	// only argument beside the optional signing_status) and Import section
	// confirms it is a named-singleton-child of the zone, the same shape as
	// aws_s3_bucket_policy: at most one per hosted zone, and the import id
	// is not a second, separately-minted token the way
	// aws_route53_resolver_dnssec_config's is (rejected above) — it is
	// literally the parent zone's own id, verbatim (terraform import
	// aws_route53_hosted_zone_dnssec.example Z1D633PJN98FT9). Concrete
	// whenever the zone is, through the aws_route53_zone marker.
	TypeIdentity{
		Type:          "aws_route53_hosted_zone_dnssec",
		Components:    []Component{attr("hosted_zone_id")},
		ImportSyntax:  "HOSTEDZONEID",
		IdentityAttrs: nil,
	},
	// aws_route53_key_signing_key: row-gen classified this needs-hand-separator
	// (registry primaryIdentifier ["HostedZoneId", "Name"], composite, no
	// separator in any schema). Independent verification against the
	// provider's Argument Reference (required: hosted_zone_id,
	// key_management_service_arn, name) and Import section supplies the
	// separator row-gen would not guess: a comma, between the parent zone's
	// own id and the key-signing key's own "name" argument (terraform
	// import aws_route53_key_signing_key.example Z1D633PJN98FT9,example) —
	// the Import section's own prose describes the second half as a "KMS
	// Key identifier", but the worked example and the registry's
	// primaryIdentifier both agree it is the "name" argument, not the
	// separate key_management_service_arn argument, so the prose is read as
	// imprecise rather than followed literally. The same
	// hand-separator-from-docs move live/e2e/estates/route53-cloudfront's
	// README makes explicit for aws_route53_zone_association and
	// aws_route53_resolver_firewall_rule below, all three precedented by
	// aws_route53_record's own ZONEID_NAME_TYPE composite above.
	TypeIdentity{
		Type: "aws_route53_key_signing_key",
		Components: []Component{
			attr("hosted_zone_id"),
			sep(","),
			attr("name"),
		},
		ImportSyntax:  "HOSTEDZONEID,NAME",
		IdentityAttrs: nil,
	},
	// aws_route53_zone_association: row-gen classified this evidence-only
	// (folded as a property-child of AWS::Route53::HostedZone). The
	// provider's own Identity Schema settles it directly: required
	// zone_id and vpc_id, both already-admitted-or-configured (zone_id
	// through the aws_route53_zone marker, vpc_id through the
	// already-admitted aws_vpc marker), joined by a colon in the documented
	// import command (terraform import aws_route53_zone_association.example
	// Z123456ABCDEFG:vpc-12345678). A composite of two required identity
	// attributes is exactly what [SynthesizeTypeIdentity] refuses to
	// self-admit (composites need a separator no schema carries), which is
	// why this needs a hand row despite having a full Identity Schema —
	// the same reason aws_route53_record above does.
	TypeIdentity{
		Type: "aws_route53_zone_association",
		Components: []Component{
			attr("zone_id"),
			sep(":"),
			attr("vpc_id"),
		},
		ImportSyntax:  "ZONEID:VPCID",
		IdentityAttrs: nil,
	},
	// aws_route53_resolver_firewall_rule: row-gen folded this into
	// AWS::Route53Resolver::FirewallRuleGroup's own registry entry and
	// proposed parent-derived admission "once [the rule group] is ratified"
	// — ratified above, in this same batch. Independent verification
	// against the provider's Argument Reference and Import section
	// confirms the composite: required firewall_rule_group_id, joined by a
	// colon to firewall_domain_list_id for a standard rule (terraform
	// import aws_route53_resolver_firewall_rule.example
	// rslvr-frg-0123456789abcdef:rslvr-fdl-0123456789abcdef) — both already
	// admitted above, in this batch, as their own marker rows. The
	// provider's docs also describe a second, "DNS Firewall Advanced" rule
	// shape whose second half is a firewall_threat_protection_id the
	// provider itself computes from a dns_threat_protection argument rather
	// than a value configuration supplies directly; this entry does not
	// attempt that shape. It does not need to: attr's own contract (read
	// the first of these arguments the configuration sets; fail naming all
	// of them if none is) means an advanced rule, which sets
	// dns_threat_protection instead of firewall_domain_list_id, simply
	// fails to resolve rather than resolving to a wrong or guessed string —
	// the same honest half-coverage aws_route53_record's own set_identifier
	// caveat above accepts rather than guessing around.
	TypeIdentity{
		Type: "aws_route53_resolver_firewall_rule",
		Components: []Component{
			attr("firewall_rule_group_id"),
			sep(":"),
			attr("firewall_domain_list_id"),
		},
		ImportSyntax:  "FIREWALLRULEGROUPID:FIREWALLDOMAINLISTID",
		IdentityAttrs: nil,
	},
	// aws_cloudfront_monitoring_subscription: row-gen classified this
	// evidence-only (its "distribution_id" argument name was guessed, not
	// backed by a provider identity schema). Independent verification
	// against the provider's Argument Reference (required: distribution_id,
	// monitoring_subscription) and Attribute Reference — "id: the ID …,
	// which corresponds to the distribution_id" — confirms the same
	// named-singleton-child shape as aws_route53_hosted_zone_dnssec above:
	// at most one per distribution, and the import id is literally the
	// parent's own id, verbatim (terraform import
	// aws_cloudfront_monitoring_subscription.example E3QYSUHO4VYRGB).
	// Concrete whenever aws_cloudfront_distribution above is, through its
	// own marker.
	TypeIdentity{
		Type:          "aws_cloudfront_monitoring_subscription",
		Components:    []Component{attr("distribution_id")},
		ImportSyntax:  "DISTRIBUTIONID",
		IdentityAttrs: []string{"id"},
	},

	// ---- Registry-ratified (#40, #44, #65): fifth batch, compute
	// ---- platforms -------------------------------------------------------
	//
	// Same tools/row-gen pipeline as the earlier batches: every row started
	// as a proposal from live/registry.json, cross-checked against the AWS
	// provider's documented import behaviour (its Argument Reference,
	// Attribute Reference and Import section, fetched from the provider's
	// own website/docs/r/ source at the pinned v6.58.0 tag) rather than
	// accepted on the registry's word alone, plus live/tag-verbs.json and
	// live/survey-full.json's per-type taggable/listable signals for
	// recoverability the same way the Route53/CloudFront batch above used
	// them. Six services: Batch, EMR (cluster/security-configuration/
	// studio plus the EMRContainers and EMRServerless siblings), App
	// Runner, Elastic Beanstalk, Amplify, Lightsail. Cohort estate:
	// live/e2e/estates/compute-platforms.
	//
	// Three rows needed reclassification beyond what row-gen itself
	// proposed, all caught by reading the provider docs rather than
	// trusting the registry or row-gen's own bucket:
	//
	//   - aws_batch_job_definition: row-gen proposed client-named on an
	//     "arn" argument (its own resolveArgName pulled "arn" out of
	//     live/survey-full.json's identity-schema signal, which names the
	//     provider's *identity* attribute, not a settable argument — the
	//     applyImportGrammarDemotions check that catches this shape for
	//     server-assigned rows never runs on a client-named one, so
	//     row-gen's own proposal is a bug here, not a source to paste
	//     as-is). The provider's Argument Reference confirms "arn" is
	//     Computed only; the Attribute Reference confirms the ARN embeds a
	//     revision number Batch mints on every new revision
	//     ("arn - ARN of the job definition, includes revision (:#)"),
	//     which no argument reconstructs. Ratified below as server-assigned
	//     instead.
	//   - aws_amplify_app: row-gen proposed server-assigned via the
	//     registry's "Arn" primaryIdentifier — right about server-assigned,
	//     wrong about which token: the provider's Import section documents
	//     import by the App ID alone (e.g. "d2ypk4k47z8u6"), and the
	//     Attribute Reference lists "id" ("Unique ID of the Amplify app")
	//     as a separate exported attribute from "arn", confirming they are
	//     not the same string. Ratified below with IdentityAttrs "id" only.
	//   - aws_elastic_beanstalk_environment: row-gen classified this
	//     evidence-only (its "environment_name" argument was GUESSED, not
	//     backed by an identity schema or the carve seed). The provider's
	//     own Import section shows otherwise: "import Elastic Beanstalk
	//     Environments using the id" (example: e-rpqsewtp2j), an opaque
	//     token Elastic Beanstalk mints at create time that no argument in
	//     the registry's create-only set (CNAMEPrefix, EnvironmentName,
	//     ApplicationName, SolutionStackName, Tier/Name, Tier/Type)
	//     reconstructs — the CFN registry entry does not even carry an
	//     EnvironmentId in its own read-only properties, a registry gap
	//     the provider's docs fill in directly. Ratified below as
	//     server-assigned, the same "docs over registry" move the earlier
	//     Route53/CloudFront batch made repeatedly.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_emr_instance_fleet, aws_emr_instance_group: row-gen proposed
	//     both server-assigned via the registry's opaque "Id" — the
	//     mapping only reaches them at all because live/mapping.json
	//     aliases them onto AWS::EMR::InstanceFleetConfig and
	//     AWS::EMR::InstanceGroupConfig (their TF and CFN names do not
	//     match directly). live/survey-full.json's mechanical signal
	//     rejects both anyway: untaggable, no native list resource, "no
	//     admission path recovers it" — they are child objects of a
	//     cluster with no individual tagging or listing surface of their
	//     own, the same shape live/tag-verbs.json's EMR AddTags entry
	//     shows (ClusterId plus ResourceId, not a single scalar this
	//     fork's tag-filtered marker discovery can key on). Confirmed
	//     against the provider's own Import section too: both import by
	//     CLUSTERID/FLEETID or CLUSTERID/GROUPID, a composite whose first
	//     half is recoverable (the cluster's marker) but whose second half
	//     names a specific live instance discovery has no way to find
	//     without the tag or list surface neither type has.
	//   - aws_emr_studio_session_mapping: row-gen classified this
	//     needs-hand-separator (registry primaryIdentifier ["StudioId",
	//     "IdentityType", "IdentityName"], composite, no separator in any
	//     schema). The provider's Import section does supply one
	//     (STUDIOID:IDENTITYTYPE:IDENTITYID, colon-separated) and all three
	//     are plain configuration arguments, the same shape the
	//     Route53/CloudFront batch hand-verified for several composites —
	//     but this batch's own scope for the EMR remainder is "only what
	//     row-gen proposes" (unlike Lightsail below, which was scoped to
	//     expand on row-gen's guesses), so it is left out here rather than
	//     hand-added, a deliberate scope boundary rather than a recoverability
	//     failure.
	//   - aws_emr_managed_scaling_policy: row-gen folded this as a
	//     property-child of AWS::EMR::Cluster (evidence-only, no pastable
	//     row); same "only what row-gen proposes" scope boundary as the
	//     session mapping above, even though its own Import section
	//     ("using the EMR Cluster identifier") would make it a clean
	//     named-singleton-child of aws_emr_cluster once in scope.
	//   - aws_elastic_beanstalk_application_version,
	//     aws_elastic_beanstalk_configuration_template: both
	//     needs-hand-separator per row-gen, and this batch's own scope for
	//     Elastic Beanstalk names only "applications, environments" —
	//     both are out of scope on that alone, and independent
	//     verification does not make either an easy hand-add anyway: the
	//     provider ships no Import section at all for
	//     aws_elastic_beanstalk_application_version (confirmed absent from
	//     live/import-grammar.json and from the provider's own docs page),
	//     and aws_elastic_beanstalk_configuration_template's
	//     live/survey-full.json signal is untaggable with no native list
	//     resource.
	//   - aws_amplify_domain_association: evidence-only per row-gen
	//     (import docs show the same composite shape as aws_amplify_branch
	//     below, APPID/DOMAINNAME), independently verifiable the same way
	//     — but this batch's own scope for Amplify names only
	//     "apps/branches", so it is left out rather than hand-added.
	//   - aws_amplify_webhook: never reaches row-gen at all —
	//     live/mapping.json records it "cfn-unmodeled" ("searched
	//     AWS::Amplify: only App, Branch and Domain exist; no Webhook
	//     type"), and notes by name that it is not
	//     AWS::CodePipeline::Webhook, a different service's unrelated
	//     same-named concept — the false-positive risk this batch's own
	//     recipe flagged in advance. Nothing to ratify or reject: it is
	//     outside the registry-backed pipeline entirely.
	//   - aws_lightsail_domain: row-gen classified this evidence-only (its
	//     "domain_name" argument guess is in fact correct — confirmed
	//     against the provider's Argument Reference, domain_name is the
	//     sole required argument), but the provider ships no Import
	//     section for this resource at all (confirmed absent from both
	//     live/import-grammar.json and the provider's own docs page), and
	//     live/survey-full.json's signal agrees separately: untaggable,
	//     no native list resource, "no admission path recovers it". Two
	//     independent rejections, and also simply not one of the
	//     categories ("instances, databases, buckets, LBs + certificates")
	//     this batch's own Lightsail scope named.
	//   - aws_lightsail_bucket_resource_access,
	//     aws_lightsail_container_service_deployment_version,
	//     aws_lightsail_disk_attachment, aws_lightsail_domain_entry,
	//     aws_lightsail_lb_attachment, aws_lightsail_lb_certificate_attachment,
	//     aws_lightsail_lb_https_redirection_policy,
	//     aws_lightsail_lb_stickiness_policy,
	//     aws_lightsail_bucket_access_key: property-child folds of the
	//     Lightsail types ratified below. Several now have every parent
	//     they need (aws_lightsail_lb_attachment's LBNAME,INSTANCENAME
	//     composite, for one), but adding parent-derived rows for a whole
	//     second tier of child types is a bigger step than this batch's
	//     named Lightsail scope ("instances, databases, buckets, LBs +
	//     certificates") asked for. Left for a future batch, the same
	//     deliberate boundary as the EMR and Elastic Beanstalk exclusions
	//     above.
	//
	// See internal/live/lint/admission.go's matching banner for the same
	// summary, and live/e2e/estates/compute-platforms/README.md for the
	// full per-type coverage table.

	serverAssigned("aws_batch_compute_environment",
		"AWS Batch mints the compute environment's own ARN at create time; the compute_environment_name argument is client-chosen but is not the import identity — the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	// aws_batch_job_definition: reclassified from row-gen's client-named
	// "arn" proposal to server-assigned; see the batch banner above.
	serverAssigned("aws_batch_job_definition",
		"AWS Batch mints a new ARN, embedding a revision number, on every job definition revision; the job_definition_name argument only a fresh revision's config sets does not reconstruct it. The provider's Identity Schema requires arn.",
		"ARN", "arn"),
	serverAssigned("aws_batch_job_queue",
		"AWS Batch mints the job queue's own ARN at create time; the job_queue_name argument is client-chosen but is not the import identity — the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_batch_scheduling_policy",
		"AWS Batch mints the scheduling policy's own ARN at create time; the name argument is client-chosen but is not the import identity.",
		"ARN", "arn", "id"),

	// aws_emr_cluster: the registry's own AWS::EMR::Cluster entry carries
	// handlers.create/read/update/delete/list all false (this fork's CFN
	// Cloud Control registry copy is a stub for this type), but this
	// fork's marker discovery does not depend on Cloud Control's own list
	// handler — live/survey-full.json's signal (taggable, "recoverable by
	// tag-filtered list") and the provider's own Import section
	// ("terraform import aws_emr_cluster.cluster j-123456ABCDEF") both
	// confirm the identity independently of the registry's handler gap.
	serverAssigned("aws_emr_cluster",
		"EMR mints the cluster's own id (j-…) at create time; none of the create-only arguments (Name, ReleaseLabel, ServiceRole, ...) reconstructs it.",
		"ID", "id"),
	// aws_emr_security_configuration: row-gen's own proposal, confirmed
	// against the provider's Import section (name argument, verbatim).
	TypeIdentity{
		Type:          "aws_emr_security_configuration",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	serverAssigned("aws_emr_studio",
		"EMR mints the studio's own id (es-…) at create time; none of the create-only arguments (AuthMode, ServiceRole, VpcId, ...) reconstructs it.",
		"ID", "id"),
	serverAssigned("aws_emrcontainers_virtual_cluster",
		"EMRContainers mints the virtual cluster's own id at create time; the name and container_provider arguments configure it but do not identify it.",
		"ID", "id"),
	serverAssigned("aws_emrserverless_application",
		"EMRServerless mints the application's own id at create time; the name and type arguments configure it but do not identify it.",
		"ID", "id"),

	serverAssigned("aws_apprunner_auto_scaling_configuration_version",
		"App Runner mints the auto scaling configuration version's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_observability_configuration",
		"App Runner mints the observability configuration's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_service",
		"App Runner mints the service's own ARN at create time; the service_name argument is client-chosen but is not the import identity — the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_vpc_connector",
		"App Runner mints the VPC connector's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),
	serverAssigned("aws_apprunner_vpc_ingress_connection",
		"App Runner mints the VPC ingress connection's own ARN at create time; the provider's Identity Schema requires arn.",
		"ARN", "arn", "id"),

	// aws_elastic_beanstalk_application: row-gen's own proposal, confirmed
	// against the provider's Import section (name argument, verbatim).
	TypeIdentity{
		Type:          "aws_elastic_beanstalk_application",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_elastic_beanstalk_environment: reclassified from row-gen's
	// evidence-only guess ("environment_name", unconfident); see the
	// batch banner above.
	serverAssigned("aws_elastic_beanstalk_environment",
		"Elastic Beanstalk mints the environment's own id (e-…) at create time; the environment_name argument is client-chosen but is not the import identity — the registry's own schema does not even carry an EnvironmentId, but the provider's Import section documents import by the environment's own opaque id.",
		"ID", "id"),

	// aws_amplify_app: reclassified from row-gen's server-assigned "Arn"
	// proposal to the App ID the provider actually documents importing
	// by; see the batch banner above. IdentityAttrs names only "id", not
	// "arn" — the provider's own Attribute Reference lists them as two
	// distinct exported attributes.
	serverAssigned("aws_amplify_app",
		"Amplify mints the app's own id (App ID, e.g. d2ypk4k47z8u6) at create time; the name argument is client-chosen but is not the import identity — the provider's Import section documents import by the App ID, embedded as the trailing path segment of the app's ARN but a distinct exported attribute ('id') from 'arn' itself.",
		"ID", "id"),
	// aws_amplify_branch: row-gen classified this evidence-only (import
	// docs show an argument-composed id, which applyImportGrammarDemotions
	// catches for a server-assigned proposal). Independent verification
	// against the provider's Import section confirms the composite
	// directly: required app_id (the just-ratified aws_amplify_app's own
	// id) and branch_name, slash-separated (terraform import
	// aws_amplify_branch.master d2ypk4k47z8u6/master) — the same
	// hand-separator-from-docs move the Route53/CloudFront batch made
	// repeatedly.
	TypeIdentity{
		Type: "aws_amplify_branch",
		Components: []Component{
			attr("app_id"),
			sep("/"),
			attr("branch_name"),
		},
		ImportSyntax:  "APPID/BRANCHNAME",
		IdentityAttrs: nil,
	},

	// Lightsail: issue #65's recipe names this service as one "the sweep
	// mapped many" for — row-gen's own registry-derived argument guesses
	// (snake-cased CFN property names) are wrong for several of these
	// types, so every one below was independently checked against the
	// provider's own Argument Reference and Import section at v6.58.0
	// rather than pasted from row-gen's evidence-only guess.
	//
	// aws_lightsail_bucket, aws_lightsail_container_service,
	// aws_lightsail_distribution: row-gen's own proposals (import grammar
	// sourced "name" argument), confirmed against the provider's Import
	// sections verbatim.
	TypeIdentity{
		Type:          "aws_lightsail_bucket",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_lightsail_container_service",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_lightsail_distribution",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_instance: row-gen guessed "instance_name" (evidence-
	// only, unconfident). The provider's Argument Reference names the
	// required argument "name" ("Instance name, must be unique per
	// region"), matching the Import section's own "using their name".
	TypeIdentity{
		Type:          "aws_lightsail_instance",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_database: row-gen guessed "relational_database_name"
	// (evidence-only, unconfident) — the guess happens to be right this
	// time: the provider's Argument Reference confirms
	// relational_database_name as the required, ForceNew argument the
	// Import section's "using their name" refers to.
	TypeIdentity{
		Type:          "aws_lightsail_database",
		Components:    []Component{attr("relational_database_name")},
		ImportSyntax:  "RELATIONALDATABASENAME",
		IdentityAttrs: []string{"relational_database_name"},
	},
	// aws_lightsail_lb: row-gen guessed "load_balancer_name" (evidence-
	// only, unconfident). The provider's Argument Reference names the
	// required argument plain "name".
	TypeIdentity{
		Type:          "aws_lightsail_lb",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_certificate: row-gen guessed "certificate_name"
	// (evidence-only, unconfident). The provider's Argument Reference
	// names the required argument plain "name" ("Name of the
	// certificate"), matching the Import section's "using the certificate
	// name".
	TypeIdentity{
		Type:          "aws_lightsail_certificate",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_lb_certificate: row-gen classified this
	// needs-hand-separator (registry primaryIdentifier ["CertificateName",
	// "LoadBalancerName"], composite, no separator in any schema).
	// Independent verification against the provider's Argument Reference
	// (required: domain_name, lb_name, name — the certificate's own name
	// argument is plain "name", not "certificate_name") and Import section
	// supplies the separator: a comma, lb_name first (terraform import
	// aws_lightsail_lb_certificate.example
	// example-load-balancer,example-load-balancer-certificate) — both
	// components plain configuration arguments, no marker or tag
	// dependency either half.
	TypeIdentity{
		Type: "aws_lightsail_lb_certificate",
		Components: []Component{
			attr("lb_name"),
			sep(","),
			attr("name"),
		},
		ImportSyntax:  "LBNAME,CERTIFICATENAME",
		IdentityAttrs: nil,
	},
	// aws_lightsail_disk: row-gen guessed "disk_name" (evidence-only,
	// unconfident). The provider's Argument Reference names the required
	// argument plain "name", matching the Import section's "using the
	// name attribute".
	TypeIdentity{
		Type:          "aws_lightsail_disk",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_lightsail_static_ip: row-gen guessed "static_ip_name" (evidence-
	// only, unconfident). The provider's Argument Reference names the
	// required argument plain "name", matching the Import section's
	// "using the name attribute".
	TypeIdentity{
		Type:          "aws_lightsail_static_ip",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// ---- Registry-ratified (#40, #44, #65): fifth batch, developer tools
	// ---- (issue #65) ------------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source at the pinned v6.58.0 tag), not accepted on the registry's
	// classification alone. Four of these types — CodeBuild::Project,
	// ::ReportGroup and ::SourceCredential, and CodeCommit::Repository —
	// ship with every CFN Registry handler false (create, read, update,
	// delete, list): row-gen still emits a proposal for each, built from
	// primaryIdentifier/readOnlyProperties/createOnlyProperties evidence
	// that exists in the registry schema regardless of the handlers, but
	// that evidence carries no corroborating registry capability, which is
	// exactly what row-gen's own "not listable" annotation on each is
	// flagging. The provider's documented Import section is the stronger,
	// provider-native signal for all four, and it disagrees with row-gen's
	// server-assigned proposal for three of them. Cohort estate:
	// live/e2e/estates/devtools.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_codebuild_source_credential: row-gen proposed server-assigned
	//     via the registry's opaque "Id", and the provider's own documented
	//     import command confirms an ARN
	//     (arn:aws:codebuild:REGION:ACCOUNT:token/SERVERTYPE) — but unlike
	//     aws_codebuild_report_group below, no argument reconstructs it:
	//     server_type and auth_type name the kind of credential, not an
	//     identifying name, and token holds the secret value itself, never
	//     echoed back. The provider's own Argument Reference carries no
	//     tags block for this type at all (untaggable), and
	//     live/survey-full.json's own signal agrees there is no native list
	//     resource either — no admission path recovers a pre-existing
	//     instance.
	//
	// Out of this batch's named scope: issue #65's own recipe names
	// CodeArtifact, CodeBuild, CodeCommit, CodeConnections (with its
	// CodeStarConnections predecessor), CodeStarNotifications, CodeDeploy,
	// CodePipeline and the ECR-public leftover — not CodeGuru. row-gen's
	// CodeGuruProfiler and CodeGuruReviewer sections (2 types total) are
	// left for a future batch. The earlier IAM/ECR batch's own deferred ECR
	// rows (aws_ecr_lifecycle_policy, aws_ecr_pull_through_cache_rule,
	// aws_ecr_pull_time_update_exclusion,
	// aws_ecr_repository_creation_template, aws_ecr_repository_policy) are
	// unchanged by this batch; only the ECR-public pair below is this
	// batch's to decide.

	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Arn"
		// — right that the ARN is the identity, but the flat
		// serverAssigned() template undersells it the same way it
		// undersold aws_sns_topic, aws_sqs_queue and
		// aws_cloudfront_realtime_log_config above: this is the
		// account-derived shape, not an opaque ID. The provider's own
		// Identity Schema requires exactly one field, arn, and its
		// documented import command (terraform import
		// aws_codeartifact_domain.example
		// arn:aws:codeartifact:us-west-2:012345678912:domain/tf-acc-test)
		// confirms the value is built from the required "domain" argument
		// plus the region and account of the cloud the run is against.
		// CFN Registry AWS::CodeArtifact::Domain reports handlers.list =
		// true with no required list input — the same Cloud Control
		// fallback shape as aws_efs_file_system in the storage batch
		// above, since the pinned provider itself has no native list
		// resource for this type (live/survey-full.json:
		// aws_codeartifact_domain.signals.list_resource is false).
		Type: "aws_codeartifact_domain",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codeartifact:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":domain/")),
			inAttr("arn", attr("domain")),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:domain/DOMAIN",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// A named-singleton-child of the domain, the same shape as
		// aws_api_gateway_rest_api_policy and aws_s3_bucket_policy above:
		// row-gen marked this "(property-child of AWS::CodeArtifact::Domain)
		// [evidence-only]", but the identity needs no new mechanism — its
		// own required "domain" argument reconstructs the same
		// account-derived ARN as aws_codeartifact_domain just above, and
		// the provider's documented import command uses exactly that ARN
		// (terraform import aws_codeartifact_domain_permissions_policy.example
		// arn:aws:codeartifact:us-west-2:012345678912:domain/tf-acc-test).
		// This type's own "id" is documented as the domain's bare name, not
		// its ARN, so it is not claimed as an identity source here.
		Type: "aws_codeartifact_domain_permissions_policy",
		Components: []Component{
			sep("arn:aws:codeartifact:"),
			cloud(CloudRegion),
			sep(":"),
			cloud(CloudAccountID),
			sep(":domain/"),
			attr("domain"),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:domain/DOMAIN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same correction as aws_codeartifact_domain above, one level
		// deeper: the provider's documented import command (terraform
		// import aws_codeartifact_repository.example
		// arn:aws:codeartifact:us-west-2:012345678912:repository/tf-acc-test/tf-acc-test)
		// confirms the ARN is built from the required "domain" and
		// "repository" arguments plus the region and account of the cloud
		// the run is against. Same Cloud Control fallback shape as the
		// domain above (live/survey-full.json:
		// aws_codeartifact_repository.signals.list_resource is false;
		// CFN Registry AWS::CodeArtifact::Repository reports handlers.list
		// = true with no required list input).
		Type: "aws_codeartifact_repository",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codeartifact:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":repository/")),
			inAttr("arn", attr("domain")),
			inAttr("arn", sep("/")),
			inAttr("arn", attr("repository")),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:repository/DOMAIN/REPOSITORY",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// A named-singleton-child of the repository, the same shape as
		// aws_codeartifact_domain_permissions_policy above: row-gen marked
		// this "(property-child of AWS::CodeArtifact::Repository)
		// [evidence-only]", but its required "domain" and "repository"
		// arguments reconstruct the same ARN as aws_codeartifact_repository
		// just above, and the provider's documented import command uses
		// exactly that ARN.
		Type: "aws_codeartifact_repository_permissions_policy",
		Components: []Component{
			sep("arn:aws:codeartifact:"),
			cloud(CloudRegion),
			sep(":"),
			cloud(CloudAccountID),
			sep(":repository/"),
			attr("domain"),
			sep("/"),
			attr("repository"),
		},
		ImportSyntax:  "arn:aws:codeartifact:REGION:ACCOUNT:repository/DOMAIN/REPOSITORY",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Arn"
		// — the registry-says-server-assigned-but-the-provider-disagrees
		// shape, the opposite correction from aws_ecs_capacity_provider's
		// rejection earlier (that one's classic import command is ARN-only;
		// this one's is not). CodeBuild::Fleet is one of the few CodeBuild
		// CFN types that actually ships full registry handlers (create,
		// read, update, delete, list all true — unlike Project,
		// ReportGroup and SourceCredential below), but the provider's own
		// documented Import section offers the ARN only as the newer
		// identity-block form; its classic import command (terraform
		// import aws_codebuild_fleet.name fleet-name) uses the bare,
		// required "name" argument verbatim.
		Type:          "aws_codebuild_fleet",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; the provider documents id as the fleet's ARN always, not name
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// — but AWS::CodeBuild::Project ships every CFN Registry handler
		// false (create, read, update, delete, list), so row-gen's own
		// enumeration note for it ("not listable -> client-named only") is
		// flagging that the registry has nothing to say about this type
		// beyond its bare schema fields. The provider's own documented
		// Import section settles it independently: the classic import
		// command (terraform import aws_codebuild_project.name
		// project-name) uses the bare, required "name" argument, and the
		// provider's own Attribute Reference is explicit that id is "Name
		// (if imported via name) or ARN (if created via Terraform or
		// imported via ARN)" — an id that is not reliably the name, which
		// is why it is left out of IdentityAttrs below even though name
		// itself is claimed.
		Type:          "aws_codebuild_project",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// (itself the ARN, per the provider's own Attribute Reference:
		// "id - The ARN of Report Group"). AWS::CodeBuild::ReportGroup
		// ships every CFN Registry handler false, the same
		// registry-handler-less shape as Project above, so again the
		// provider's own documented Import section is the deciding
		// evidence rather than the registry's. Unlike Project,
		// ReportGroup's classic import command (terraform import
		// aws_codebuild_report_group.example
		// arn:aws:codebuild:us-west-2:123456789:report-group/report-group-name)
		// offers no bare-name alternative anywhere in the provider's docs
		// — but the ARN it names is built entirely from the required
		// "name" argument plus the region and account of the cloud the run
		// is against, the same account-derived shape as
		// aws_codeartifact_domain above, not an opaque ID. Taggable, but
		// with no CFN Registry list handler and no native provider list
		// resource either (live/survey-full.json:
		// aws_codebuild_report_group.signals.list_resource is false), so
		// this type reaches a live resource only when a caller supplies a
		// CloudContext; a context-less run finds nothing to enumerate it
		// against, the same open gap this table's aws_sns_topic comment
		// already documents for the account-derived category as a whole.
		Type: "aws_codebuild_report_group",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codebuild:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":report-group/")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:codebuild:REGION:ACCOUNT:report-group/NAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// A named-singleton-child of the project: row-gen marked this
		// "(property-child of AWS::CodeBuild::Project) [evidence-only]",
		// but the shape needs no new mechanism — its sole required
		// argument, project_name, is documented as literally equal to this
		// type's own id ("id - The name of the build project"), and the
		// provider's documented import command (terraform import
		// aws_codebuild_webhook.example MyProjectName) uses that same
		// value verbatim. Concrete whenever aws_codebuild_project above is.
		Type:          "aws_codebuild_webhook",
		Components:    []Component{attr("project_name")},
		ImportSyntax:  "PROJECTNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// — but AWS::CodeCommit::Repository ships every CFN Registry
		// handler false, the same registry-handler-less shape as the
		// CodeBuild trio above, so the registry's classification carries no
		// corroborating capability. The provider's own documented Import
		// section settles it independently: the classic import command
		// (terraform import aws_codecommit_repository.imported
		// ExistingRepo) uses the bare, required "repository_name" argument
		// verbatim. The provider exports repository_id as a separate,
		// distinct attribute ("The ID of the repository"), so it is not
		// claimed here.
		Type:          "aws_codecommit_repository",
		Components:    []Component{attr("repository_name")},
		ImportSyntax:  "REPOSITORYNAME",
		IdentityAttrs: []string{"repository_name"},
	},

	serverAssigned("aws_codeconnections_connection",
		"CodeConnections mints the connection's own ARN at create time, embedding a random connection ID the required \"name\" argument does not reconstruct (a fresh connection is also created PENDING and needs a manual console step to reach AVAILABLE, independent of its identity). CFN Registry AWS::CodeConnections::Connection reports handlers.list = true with no required list input, the same Cloud Control fallback shape as aws_efs_file_system in the storage batch, since the pinned provider itself has no native list resource for this type.",
		"arn:aws:codeconnections:REGION:ACCOUNT:connection/CONNECTIONID", "arn", "id"),
	serverAssigned("aws_codestarconnections_connection",
		"The predecessor CFN type of aws_codeconnections_connection above, from before the CodeStar Connections service was renamed CodeConnections; not deprecated as of the pinned v6.58.0 (confirmed against the provider's own docs, which carry no deprecation notice for either the resource or its attributes). Same shape: CodeStarConnections mints the connection's own ARN at create time, embedding a random connection ID the required \"name\" argument does not reconstruct. CFN Registry AWS::CodeStarConnections::Connection reports handlers.list = true with no required list input, the same Cloud Control fallback as its successor.",
		"arn:aws:codestar-connections:REGION:ACCOUNT:connection/CONNECTIONID", "arn", "id"),
	serverAssigned("aws_codestarnotifications_notification_rule",
		"CodeStar Notifications mints the rule's own ARN at create time, embedding a random rule ID; the required \"name\" and \"resource\" arguments (a display name and the ARN of the thing being watched) do not reconstruct it. CFN Registry AWS::CodeStarNotifications::NotificationRule reports handlers.list = true with no required list input, the same Cloud Control fallback shape as aws_efs_file_system in the storage batch.",
		"arn:aws:codestar-notifications:REGION:ACCOUNT:notificationrule/RULEID", "arn", "id"),

	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// the registry's own createOnlyProperties "ApplicationName" —
		// confirmed against the provider's documented import command
		// (terraform import aws_codedeploy_app.example my-application),
		// which uses the required "name" argument verbatim.
		Type:          "aws_codedeploy_app",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; the provider documents it as CodeDeploy's own assigned application ID, not the name
	},
	TypeIdentity{
		// row-gen proposed this correctly: client-named via the registry's
		// own createOnlyProperties "DeploymentConfigName" — confirmed
		// against the provider's documented import command (terraform
		// import aws_codedeploy_deployment_config.example
		// my-deployment-config), which uses the required
		// "deployment_config_name" argument verbatim, and its Attribute
		// Reference ("id - The deployment group's config name."), which is
		// the same string.
		Type:          "aws_codedeploy_deployment_config",
		Components:    []Component{attr("deployment_config_name")},
		ImportSyntax:  "DEPLOYMENT_CONFIG_NAME",
		IdentityAttrs: []string{"id", "deployment_config_name"},
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["ApplicationName", "DeploymentGroupName"],
		// composite, no separator in any schema). live/import-grammar.json's
		// own scraped Import section supplies the separator row-gen would
		// not guess: a colon, between the two required arguments already in
		// configuration (terraform import
		// aws_codedeploy_deployment_group.example
		// my-application:my-deployment-group), confirmed against the
		// provider's Argument Reference (required: app_name,
		// deployment_group_name, service_role_arn) and Attribute Reference
		// ("id - Application name and deployment group name.").
		Type: "aws_codedeploy_deployment_group",
		Components: []Component{
			attr("app_name"),
			sep(":"),
			attr("deployment_group_name"),
		},
		ImportSyntax:  "APP_NAME:DEPLOYMENT_GROUP_NAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// the provider's own identity schema (required: name), confirmed
		// against the documented import command (terraform import
		// aws_codepipeline.example example-pipeline), which uses the
		// required "name" argument verbatim.
		Type:          "aws_codepipeline",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (registry
		// primaryIdentifier ["Category", "Provider", "Version"], composite,
		// no separator in any schema). live/import-grammar.json's own
		// scraped Import section supplies both the separator and a
		// correction row-gen would not guess from the registry alone: a
		// colon joins all three (terraform import
		// aws_codepipeline_custom_action_type.example Build:terraform:1),
		// confirmed against the provider's Argument Reference (required:
		// category, provider_name, version — "provider_name", not the
		// registry's "Provider") and Attribute Reference ("id - Composed of
		// category, provider and version. For example, Build:terraform:1").
		Type: "aws_codepipeline_custom_action_type",
		Components: []Component{
			attr("category"),
			sep(":"),
			attr("provider_name"),
			sep(":"),
			attr("version"),
		},
		ImportSyntax:  "CATEGORY:PROVIDER:VERSION",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen proposed server-assigned via the registry's opaque "Id"
		// — the flat serverAssigned() template undersells it the same way
		// it undersold aws_sns_topic and aws_sqs_queue above: the
		// provider's own Identity Schema requires exactly one field, arn,
		// and its documented import command (terraform import
		// aws_codepipeline_webhook.example
		// arn:aws:codepipeline:us-west-2:123456789012:webhook:example)
		// confirms the value is built from the required "name" argument
		// plus the region and account of the cloud the run is against —
		// joined with a colon, not a slash, unlike the CodeArtifact ARNs
		// above.
		Type: "aws_codepipeline_webhook",
		Components: []Component{
			inAttr("arn", sep("arn:aws:codepipeline:")),
			inAttr("arn", cloud(CloudRegion)),
			inAttr("arn", sep(":")),
			inAttr("arn", cloud(CloudAccountID)),
			inAttr("arn", sep(":webhook:")),
			inAttr("arn", attr("name")),
		},
		ImportSyntax:  "arn:aws:codepipeline:REGION:ACCOUNT:webhook:NAME",
		IdentityAttrs: []string{"arn", "id"},
	},
	TypeIdentity{
		// row-gen proposed this correctly the first time: client-named via
		// live/import-grammar.json's scraped argument, confirmed against
		// the provider's documented import command (terraform import
		// aws_ecrpublic_repository.example example), which uses the
		// required "repository_name" argument verbatim, and its Attribute
		// Reference ("id - The repository name.") — the ECR-public
		// leftover from the IAM/ECR batch's own ECR section, which ratified
		// the registry-backed aws_ecr_repository but not its public
		// counterpart.
		Type:          "aws_ecrpublic_repository",
		Components:    []Component{attr("repository_name")},
		ImportSyntax:  "REPOSITORY_NAME",
		IdentityAttrs: []string{"id", "repository_name"},
	},
	TypeIdentity{
		// A named-singleton-child of the public repository, the same shape
		// as aws_ecr_repository_policy's own deferred entry in the IAM/ECR
		// batch — but this batch's own scope names the ECR-public leftover
		// explicitly, and the identity needs no new mechanism: its sole
		// required argument, repository_name, is what the provider's
		// documented import command (terraform import
		// aws_ecrpublic_repository_policy.example example) uses verbatim.
		// Concrete whenever aws_ecrpublic_repository above is.
		Type:          "aws_ecrpublic_repository_policy",
		Components:    []Component{attr("repository_name")},
		ImportSyntax:  "REPOSITORY_NAME",
		IdentityAttrs: nil,
	},

	// ---- Registry-ratified (#40, #44, #65): fifth batch, identity
	// ---- (Cognito, IAM leftovers, SSO Admin) ------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented import behaviour — its real Import,
	// Argument Reference and Attribute Reference sections, fetched from
	// github.com/hashicorp/terraform-provider-aws at the pinned v6.59.0 tag
	// — rather than accepted on the registry's word alone. Cohort estate:
	// live/e2e/estates/identity.
	//
	// Rejected outright, on independent verification against the
	// provider's real docs:
	//
	//   - aws_iam_group_membership: row-gen proposed server-assigned via
	//     the registry's opaque "Id" (AWS::IAM::UserToGroupAddition, whose
	//     registry entry ships every handler false — create, read, update,
	//     delete and list — a stub CFN type with no working handler at
	//     all). The real provider docs settle it further than the registry
	//     even tries to: v6.59.0's website/docs/r/iam_group_membership
	//     carries no Import section whatsoever, meaning this type simply
	//     is not importable in the pinned provider release. Not a
	//     composite this table could hand-write a separator for, and not a
	//     marker candidate either — genuinely absent evidence, not weak
	//     evidence.
	//   - aws_cognito_managed_login_branding: row-gen filed this
	//     needs-hand-separator (UserPoolId, ManagedLoginBrandingId). The
	//     real import id is exactly that pair, comma-joined — but
	//     ManagedLoginBrandingId is the provider's own Attribute
	//     Reference-only output ("ID of the managed login branding
	//     style"), never a configuration argument, so only the
	//     user_pool_id half is composable. Same shape as
	//     aws_cognito_user_pool_client below.
	//   - aws_cognito_user_pool_client: row-gen filed this
	//     needs-hand-separator (UserPoolId, ClientId). The real import id
	//     is UserPoolId/ClientId, slash-joined, but ClientId is Cognito's
	//     own server-assigned output (Attribute Reference only, never an
	//     argument) — no config value reconstructs it. Compounding, not
	//     the deciding factor: when generate_secret is set, the same
	//     resource also mints a client_secret attribute a live read can
	//     never recover, the same credential shape that excludes
	//     aws_iam_access_key by SURVEY.md's standing rule.
	//   - aws_cognito_risk_configuration and
	//     aws_cognito_user_pool_ui_customization: both key on the same
	//     unadmitted client_id half aws_cognito_user_pool_client's own
	//     rejection just named — ui_customization requires it outright,
	//     and risk_configuration's own real import id
	//     (user_pool_id, or user_pool_id:client_id when client_id is set —
	//     the provider's Argument Reference marks client_id Optional) is a
	//     conditionally-shaped composite this table's Components
	//     vocabulary has no way to express even before client_id's own
	//     problem is reached.
	//   - aws_identitystore_group and aws_identitystore_group_membership:
	//     row-gen filed both needs-hand-separator
	//     (IdentityStoreId/GroupId, IdentityStoreId/MembershipId — both
	//     confirmed slash-joined against the real docs). GroupId and
	//     MembershipId are each the provider's own Attribute
	//     Reference-only output (a UUID IdentityStore mints at create
	//     time), so only the identity_store_id half is composable — the
	//     same shape as the two Cognito rejections above, and, like
	//     aws_ssoadmin_permission_set below, scoped by an IAM Identity
	//     Center singleton this fork has no admitted resource for. Neither
	//     is taggable (IdentityStore's tagging API is scoped to
	//     principals, not groups or memberships), so there is no marker
	//     path either. A batch that gives aws_ssoadmin_permission_set's
	//     own singleton-scope precedent a second, content-matched
	//     enumeration mechanism (rather than this batch's tag-filtered
	//     one) could pick these back up.
	//
	// Already rejected, and not re-litigated here: aws_iam_saml_provider
	// and aws_iam_virtual_mfa_device were both rejected by the IAM/ECR
	// batch above on ARN-embedding grounds (the registry's read-only Arn
	// field is really a composite of a required name argument this
	// provider does not treat as reconstructible). aws_iam_access_key is
	// excluded the same way that batch excluded it: SURVEY.md's standing
	// credential rule (a create-only secret a live read can never recover).
	// This batch's own independent look at all three found nothing that
	// changes the earlier verdict.
	//
	// Deferred, evidence-only per row-gen with no pastable row to
	// hand-verify against — the same "not this batch's to decide" standard
	// the IAM/ECR batch's own aws_iam_role_policy_attachment slice used
	// for its siblings: aws_ssoadmin_customer_managed_policy_attachment,
	// aws_ssoadmin_customer_managed_policy_attachments_exclusive,
	// aws_ssoadmin_managed_policy_attachment,
	// aws_ssoadmin_managed_policy_attachments_exclusive,
	// aws_ssoadmin_permission_set_inline_policy and
	// aws_ssoadmin_permissions_boundary_attachment are all property-children
	// of AWS::SSO::PermissionSet that row-gen never generated a row for
	// (its own note: "parent aws_ssoadmin_permission_set is not itself
	// proposed"). That parent is admitted below, but hand-composing six
	// children row-gen gives no evidence for at all is a bigger lift than
	// this batch's mandate. aws_cognito_log_delivery_configuration is the
	// same shape on the Cognito side: evidence-only, untaggable, unlistable,
	// no pastable row. aws_identitystore_user and
	// aws_cognito_managed_user_pool_client are outside row-gen's scope
	// entirely — live/mapping.json carries no CFN type for either (the
	// first is real IAM Identity Center surface CloudFormation's
	// IdentityStore coverage does not model; the second adopts an existing
	// Cognito-managed client rather than creating one, the same
	// default_*-adopter shape as aws_default_vpc) — so row-gen's
	// registry-driven analysis never runs on them at all.

	serverAssigned("aws_cognito_identity_pool",
		"Cognito Identity mints the identity pool's own id (a REGION:UUID string) at create time; the identity_pool_name argument is client-chosen but is not the import identity.",
		"IDENTITYPOOLID", "id"),
	serverAssigned("aws_cognito_user_pool",
		"Cognito mints the user pool's own id (region_XXXXXXXXX) at create time; the name argument is client-chosen but is not the import identity.",
		"USERPOOLID", "id"),

	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque
		// "Id" (AWS::Cognito::IdentityPoolRoleAttachment). The real,
		// documented import id is not an independent token at all: it is
		// literally the parent identity pool's own id, verbatim
		// (terraform import aws_cognito_identity_pool_roles_attachment.example
		// us-west-2:b64805ad-cb56-40ba-9ffc-f5d8207e6d42) — the same
		// named-singleton-child shape as aws_s3_bucket_policy, at most one
		// attachment per pool. Concrete whenever
		// aws_cognito_identity_pool above is, through its own marker.
		Type:          "aws_cognito_identity_pool_roles_attachment",
		Components:    []Component{attr("identity_pool_id")},
		ImportSyntax:  "IDENTITYPOOLID",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator
		// (IdentityPoolId, IdentityProviderName). The provider's real
		// Import section and Argument Reference confirm a colon-joined
		// composite of two Required arguments, identity_pool_id (the
		// already-admitted pool's own marker-discovered id) and
		// identity_provider_name (a literal string already in
		// configuration) — the same concrete-composite shape as
		// aws_iam_role_policy's ROLENAME:POLICYNAME.
		Type: "aws_cognito_identity_pool_provider_principal_tag",
		Components: []Component{
			attr("identity_pool_id"),
			sep(":"),
			attr("identity_provider_name"),
		},
		ImportSyntax:  "IDENTITYPOOLID:IDENTITYPROVIDERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId,
		// ProviderName). The provider's real Import section and Argument
		// Reference confirm a colon-joined composite of two Required
		// arguments, user_pool_id (the already-admitted pool's own
		// marker-discovered id) and provider_name (a literal string
		// already in configuration) — the same shape as the principal tag
		// above.
		Type: "aws_cognito_identity_provider",
		Components: []Component{
			attr("user_pool_id"),
			sep(":"),
			attr("provider_name"),
		},
		ImportSyntax:  "USERPOOLID:PROVIDERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId,
		// Identifier). The provider's real Import section documents a
		// pipe-joined composite ("us-west-2_abc123|https://example.com"),
		// an unusual separator character this table has not used before —
		// confirmed directly against the raw provider docs source, not
		// inferred — of two Required arguments already in configuration:
		// user_pool_id (the already-admitted pool's own marker-discovered
		// id) and identifier (a literal string, e.g. an API's URI).
		Type: "aws_cognito_resource_server",
		Components: []Component{
			attr("user_pool_id"),
			sep("|"),
			attr("identifier"),
		},
		ImportSyntax:  "USERPOOLID|IDENTIFIER",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId, Username).
		// The provider's real Import section confirms a slash-joined
		// composite of two Required arguments already in configuration:
		// user_pool_id (the already-admitted pool's own marker-discovered
		// id) and username (a literal string).
		Type: "aws_cognito_user",
		Components: []Component{
			attr("user_pool_id"),
			sep("/"),
			attr("username"),
		},
		ImportSyntax:  "USERPOOLID/USERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId, GroupName —
		// the registry's own field name; the provider's Argument Reference
		// calls the same argument "name"). The real Import section
		// confirms a slash-joined composite of two Required arguments
		// already in configuration: user_pool_id (the already-admitted
		// pool's own marker-discovered id) and name (a literal group
		// name).
		Type: "aws_cognito_user_group",
		Components: []Component{
			attr("user_pool_id"),
			sep("/"),
			attr("name"),
		},
		ImportSyntax:  "USERPOOLID/NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (UserPoolId, GroupName,
		// Username — a three-part composite, beyond what any earlier batch
		// in this table has hand-wired). The provider's real Import
		// section confirms a comma-joined triple of three Required
		// arguments already in configuration: user_pool_id (the
		// already-admitted pool's own marker-discovered id), group_name
		// and username (both literal strings).
		Type: "aws_cognito_user_in_group",
		Components: []Component{
			attr("user_pool_id"),
			sep(","),
			attr("group_name"),
			sep(","),
			attr("username"),
		},
		ImportSyntax:  "USERPOOLID,GROUPNAME,USERNAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen proposed this needs-hand-separator on the registry's own
		// evidence (primaryIdentifier=[UserPoolId, Domain]). The real
		// provider docs disagree with the registry's own compound key: the
		// documented import command
		// (terraform import aws_cognito_user_pool_domain.main
		// auth.example.org) and Argument Reference both settle on the
		// domain argument alone — CloudFormation's AWS::Cognito::UserPoolDomain
		// models the domain as scoped by its pool, but the Terraform
		// resource's own import grammar does not require the scope at
		// all. Same shape as the RDS batch's aws_db_proxy_default_target_group
		// correction: the registry's compound key oversold what the
		// provider actually asks for.
		Type:          "aws_cognito_user_pool_domain",
		Components:    []Component{attr("domain")},
		ImportSyntax:  "DOMAIN",
		IdentityAttrs: []string{"domain"},
	},

	serverAssigned("aws_iam_openid_connect_provider",
		"IAM mints the OIDC provider's own ARN at create time, embedding the required url argument's host with its scheme stripped as a value the provider computes rather than one this table treats as reconstructible; the url argument itself is not the identity. Taggable, so recoverable by tag-filtered list — though the provider ships this type no native list resource in v6.59.0 (live/survey-full.json: list_resource=false), so today's only enumeration route for it is the same registry-only Cloud Control gap live/e2e/estates/storage/README.md documents for aws_efs_file_system, proven at internal/live/discovery's own test tier but not yet reachable from a real run.",
		"ARN", "arn", "id"),
	serverAssigned("aws_iam_policy",
		"IAM mints the policy's own ARN at create time — it embeds the name argument and, when path is set, that too, but the provider's identity schema requires the whole arn as one opaque string (required_for_import=[arn]), not built component-by-component the way aws_sns_topic's account-derived ARN is. Taggable and listable (live/survey-full.json: list_resource=true), so recoverable by ordinary tag-filtered list. row-gen's own registry evidence (Id) undersold this; the IAM/ECR batch above left it out as account-derived follow-on work, but the provider's own required_for_import already names the simpler, schema-literal marker path this row takes instead.",
		"ARN", "arn", "id"),
	TypeIdentity{
		// row-gen proposed this server-assigned via the registry's opaque
		// "Id"; the real, documented import id and Attribute Reference are
		// both the name argument alone (terraform import
		// aws_iam_server_certificate.certificate
		// example.com-certificate-until-2018), the same client-named shape
		// as aws_iam_role, aws_iam_user and aws_iam_group above.
		Type:          "aws_iam_server_certificate",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (PolicyName, GroupName).
		// The provider's real Import section and Argument Reference
		// confirm a colon-joined composite of two arguments already in
		// configuration, group and name (name Optional — Terraform assigns
		// a random one when omitted, the same idiom aws_iam_role_policy
		// above already accepts as "concrete in any realistic config") —
		// the group-policy sibling of that exact row.
		Type: "aws_iam_group_policy",
		Components: []Component{
			attr("group"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "GROUPNAME:POLICYNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// row-gen filed this property-child fold evidence-only, keyed on
		// aws_iam_group once ratified (client-named already, before this
		// batch). The provider's real Import section and Argument
		// Reference confirm a slash-joined composite of two Required
		// arguments already in configuration, group and policy_arn — the
		// group-policy-attachment sibling of aws_iam_role_policy_attachment
		// above, same standard of care: the attachment's own id is
		// provider-internal and is not the import id, so nothing may
		// derive an identity from it.
		Type: "aws_iam_group_policy_attachment",
		Components: []Component{
			attr("group"),
			sep("/"),
			attr("policy_arn"),
		},
		ImportSyntax:  "GROUPNAME/POLICYARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// Same shape as the group policy above, the user-policy sibling.
		// row-gen filed this needs-hand-separator (PolicyName, UserName);
		// the real Import section and Argument Reference confirm a
		// colon-joined composite of user and name (name Optional, same
		// idiom).
		Type: "aws_iam_user_policy",
		Components: []Component{
			attr("user"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "USERNAME:POLICYNAME",
		IdentityAttrs: []string{"id"},
	},
	TypeIdentity{
		// Same shape as the group policy attachment above, the
		// user-policy-attachment sibling. row-gen filed this property-child
		// fold evidence-only, keyed on aws_iam_user once ratified
		// (client-named already, before this batch); the real Import
		// section and Argument Reference confirm a slash-joined composite
		// of user and policy_arn.
		Type: "aws_iam_user_policy_attachment",
		Components: []Component{
			attr("user"),
			sep("/"),
			attr("policy_arn"),
		},
		ImportSyntax:  "USERNAME/POLICYARN",
		IdentityAttrs: nil,
	},

	serverAssigned("aws_ssoadmin_application",
		"SSO Admin mints the application's own ARN at create time, embedding a pre-existing IAM Identity Center instance ARN and a server-assigned application id; the name argument is client-chosen but is not the import identity. Taggable, so recoverable by tag-filtered list — the provider documents no dedicated data source for enumerating applications, the same enumeration caveat live/e2e/estates/storage/README.md already records for a type this table admits on identity grounds alone.",
		"APPLICATIONARN", "arn", "id"),
	TypeIdentity{
		// row-gen proposed this correctly the first time: the registry's
		// primaryIdentifier=[InstanceArn], entirely createOnlyProperties,
		// matches the provider's own real, documented import id (terraform
		// import aws_ssoadmin_instance_access_control_attributes.example
		// arn:aws:sso:::instance/ssoins-0123456789abcdef) — the instance_arn
		// argument alone. An IAM Identity Center instance is an
		// account-level singleton no CFN type and no provider resource
		// models (there is no aws_ssoadmin_instance type at all), so this
		// argument is always a literal string a configuration copies from
		// the aws_ssoadmin_instances data source rather than a reference to
		// any admitted resource — the same account-scoped-singleton shape
		// aws_ecr_registry_policy's own registry_id has above.
		Type:          "aws_ssoadmin_instance_access_control_attributes",
		Components:    []Component{attr("instance_arn")},
		ImportSyntax:  "INSTANCEARN",
		IdentityAttrs: []string{"instance_arn"}, // "id" intentionally omitted; see issue #44 non-goals
	},
	serverAssigned("aws_ssoadmin_permission_set",
		"SSO Admin mints the permission set's own ARN at create time; the name argument is client-chosen but is not the import identity, and the provider's documented import string additionally requires the instance ARN, comma-joined — the same account-level-singleton precedent aws_ecr_registry_policy's own registry_id already set: an IAM Identity Center instance pre-exists any resource, is never created by this fork, and has no admitted resource type of its own to be parent-derived through (no AWS::SSO::Instance CFN type exists, and there is no aws_ssoadmin_instance resource type). No single attribute of this type equals the whole two-ARN import string, so none is offered as an identity source.",
		"PERMISSIONSETARN,INSTANCEARN"),
	TypeIdentity{
		// row-gen filed this needs-hand-separator (six-part primary
		// identifier, InstanceArn/TargetId/TargetType/PermissionSetArn/
		// PrincipalType/PrincipalId, all createOnlyProperties). The
		// provider's real Import section confirms a comma-joined sextuple
		// in a specific documented order — principal_id, principal_type,
		// target_id, target_type, permission_set_arn, instance_arn — of
		// six Required arguments already in configuration. permission_set_arn
		// and instance_arn are references to the two already-admitted
		// marker types just above (aws_ssoadmin_permission_set,
		// unadmitted-instance-singleton), the same "a live parent ARN
		// feeds a literal argument" shape aws_lb_target_group_attachment's
		// target_group_arn already has. live/survey-full.json's own
		// mechanical pass reaches "parent-derived, admission: schema"
		// independently (every required_for_import attribute is a
		// same-named Required argument) — the same double-confirmation
		// aws_dynamodb_resource_policy's own row above records — but a
		// hand row is still written here, the same way that one was, both
		// for the explicit, doc-verified field order and for the cohort
		// estate coverage row.
		Type: "aws_ssoadmin_account_assignment",
		Components: []Component{
			attr("principal_id"),
			sep(","),
			attr("principal_type"),
			sep(","),
			attr("target_id"),
			sep(","),
			attr("target_type"),
			sep(","),
			attr("permission_set_arn"),
			sep(","),
			attr("instance_arn"),
		},
		ImportSyntax:  "PRINCIPAL_ID,PRINCIPAL_TYPE,TARGET_ID,TARGET_TYPE,PERMISSION_SET_ARN,INSTANCE_ARN",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		// row-gen filed this needs-hand-separator (ApplicationArn,
		// PrincipalType, PrincipalId). The provider's real Import section
		// confirms a comma-joined triple in a specific documented order —
		// application_arn, principal_id, principal_type — of three
		// Required arguments already in configuration. application_arn
		// is a reference to the already-admitted aws_ssoadmin_application
		// marker above, the same shape as permission_set_arn in the
		// account assignment just above.
		Type: "aws_ssoadmin_application_assignment",
		Components: []Component{
			attr("application_arn"),
			sep(","),
			attr("principal_id"),
			sep(","),
			attr("principal_type"),
		},
		ImportSyntax:  "APPLICATION_ARN,PRINCIPAL_ID,PRINCIPAL_TYPE",
		IdentityAttrs: nil,
	},

	// ---- Registry-ratified (#40, #44, #65): fifth batch, observability and
	// ---- eventing remainder (issue #65's ratification campaign). Same
	// ---- tools/row-gen pipeline as the earlier batches, cross-checked
	// ---- against live/import-grammar.json (the provider's documented
	// ---- Import sections, fetched at the pinned v6.59.0 tag) and, for
	// ---- several rows, against the provider's Argument Reference directly
	// ---- rather than accepted on the registry's word alone. Cohort estate:
	// ---- live/e2e/estates/observability.
	//
	// Amazon Managed Prometheus (AWS::APS::*, the "AMP" TF prefix
	// aws_prometheus_*) is deliberately out of scope: issue #68's concurrent
	// batch owns it, and this batch's own evidence-gathering pass over
	// row-gen's APS section stops at reading it, to avoid two agents
	// proposing the same admission.go/table.go rows at once. Amazon
	// Application Signals has no CloudFormation resource type in
	// live/mapping.json's roster at all (row-gen's service listing has no
	// ApplicationSignals section), so there is nothing here to ratify or
	// reject for it.
	//
	// CloudWatch: three corrections. row-gen filed
	// aws_cloudwatch_alarm_mute_rule and aws_cloudwatch_contributor_insight_rule
	// evidence-only because the registry's own primaryIdentifier for both is
	// a read-only Arn — but each provider doc disagrees, the same shape as
	// the messaging batch's aws_sns_topic_policy correction: both resources'
	// Argument Reference and Identity Schema require_for_import exactly one
	// argument already in configuration (name, rule_name respectively), and
	// the documented import command uses that value verbatim, not an ARN.
	TypeIdentity{
		Type:          "aws_cloudwatch_alarm_mute_rule",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_contributor_insight_rule",
		Components:    []Component{attr("rule_name")},
		ImportSyntax:  "RULE_NAME",
		IdentityAttrs: []string{"rule_name"},
	},
	// aws_cloudwatch_otel_enrichment: row-gen proposed server-assigned via
	// the registry's AccountId. The provider's own Argument Reference has no
	// required arguments at all (region is its only, optional, argument),
	// and its Import section's example ID is the region string alone
	// ("us-west-2") — a per-region account singleton, the same shape as
	// aws_ecr_registry_policy and aws_api_gateway_account above, neither of
	// which any configuration argument identifies.
	serverAssigned("aws_cloudwatch_otel_enrichment",
		"the OTel enrichment setting is a singleton per AWS region: its identity is the region the run is against, which pre-exists the resource and is never supplied by a configuration argument — the resource has no required arguments at all. Confirmed against the provider's own Import section, whose example ID is the region string alone.",
		"REGION"),

	// Logs. aws_cloudwatch_log_group is already admitted above (client-
	// assigned identity, path 1) and is not repeated here.
	//
	// Four property-children row-gen folds onto an already-ratified Logs
	// parent (aws_cloudwatch_log_data_protection_policy and
	// aws_cloudwatch_log_index_policy onto LogGroup,
	// aws_cloudwatch_log_delivery_destination_policy onto
	// DeliveryDestination, aws_cloudwatch_log_destination_policy onto
	// Destination) are deferred for the same reason the ApiGateway batch's
	// method/response children were: admitting a property-child needs a
	// parent-derived admission mechanism this table does not have yet. Their
	// identity work is not the blocker — only the mechanism is.
	serverAssigned("aws_cloudwatch_log_anomaly_detector",
		"the Logs service assigns the anomaly detector's ARN at create time; no argument reconstructs it. Confirmed against the provider's own Identity Schema (required: arn) — the row-gen proposal here already matched the provider's documented behaviour with no correction needed.",
		"ARN"),
	serverAssigned("aws_cloudwatch_log_delivery",
		"the Logs service assigns the delivery's own opaque id at create time; delivery_source_name and delivery_destination_arn describe what it connects, not what comes back. Confirmed against the provider's own Identity Schema (required: id) and its Import section's example (a short opaque token, not an ARN).",
		"ID"),
	TypeIdentity{
		Type:          "aws_cloudwatch_log_delivery_destination",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_delivery_source",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_destination",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_log_transformer",
		Components:    []Component{attr("log_group_arn")},
		ImportSyntax:  "LOG_GROUP_ARN",
		IdentityAttrs: []string{"log_group_arn"},
	},
	serverAssigned("aws_cloudwatch_query_definition",
		"the Logs service assigns the query definition's own UUID at create time; the query's name and query_string describe it but do not identify it. Confirmed against the provider's own Identity Schema (required: query_definition_id) — the row-gen proposal here already matched the provider's documented behaviour with no correction needed.",
		"QUERY_DEFINITION_ID"),
	// Three of row-gen's four Logs "needs hand separator" rows resolve
	// cleanly against live/import-grammar.json's scraped Import sections,
	// each confirmed required in the provider's own Argument Reference
	// (issue #65's own note that most needs-hand-separator rows are now
	// resolvable this way): aws_cloudwatch_log_metric_filter and
	// aws_cloudwatch_log_stream both join log_group_name and name with a
	// colon; aws_cloudwatch_log_subscription_filter joins the same two
	// arguments with a pipe instead — confirmed by fetching its docs
	// directly, not assumed from the other two's separator. Neither type's
	// own id attribute is asserted to equal the joined string (unconfirmed
	// against provider source), so IdentityAttrs stays nil, the same
	// caution aws_route's own composite above takes.
	TypeIdentity{
		Type: "aws_cloudwatch_log_metric_filter",
		Components: []Component{
			attr("log_group_name"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "LOG_GROUP_NAME:NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_cloudwatch_log_stream",
		Components: []Component{
			attr("log_group_name"),
			sep(":"),
			attr("name"),
		},
		ImportSyntax:  "LOG_GROUP_NAME:NAME",
		IdentityAttrs: nil,
	},
	TypeIdentity{
		Type: "aws_cloudwatch_log_subscription_filter",
		Components: []Component{
			attr("log_group_name"),
			sep("|"),
			attr("name"),
		},
		ImportSyntax:  "LOG_GROUP_NAME|NAME",
		IdentityAttrs: nil,
	},
	// The fourth: aws_cloudwatch_log_account_policy. Its two composed
	// arguments (policy_name, policy_type) are both required in the
	// provider's own Argument Reference, and its Import section's separator
	// is a colon, confirmed the same way as the three above.
	TypeIdentity{
		Type: "aws_cloudwatch_log_account_policy",
		Components: []Component{
			attr("policy_name"),
			sep(":"),
			attr("policy_type"),
		},
		ImportSyntax:  "POLICY_NAME:POLICY_TYPE",
		IdentityAttrs: nil,
	},
	// aws_cloudwatch_log_resource_policy: row-gen proposed client-named via
	// policy_name alone, reading only the registry's primaryIdentifier. The
	// provider's real Argument Reference says both policy_name and
	// resource_arn are individually optional but "exactly one ... must be
	// specified" — an account-scoped policy imports by policy_name verbatim,
	// a resource-scoped one by resource_arn verbatim, never both. This is
	// the same mutually-exclusive-alternatives shape aws_route's own
	// destination component already carries (attr's first-present-wins
	// reads whichever of several differently-named arguments the
	// configuration actually set), not a new mechanism: a config that sets
	// neither fails to resolve rather than guessing, the honest half-
	// coverage aws_route53_record's set_identifier caveat also accepts.
	TypeIdentity{
		Type:          "aws_cloudwatch_log_resource_policy",
		Components:    []Component{attr("policy_name", "resource_arn")},
		ImportSyntax:  "POLICY_NAME | RESOURCE_ARN (exactly one)",
		IdentityAttrs: nil,
	},

	// EventBridge/Events. aws_cloudwatch_event_rule was already rejected by
	// the messaging batch (needs a literal-default component this table
	// does not have, for event_bus_name silently defaulting to "default")
	// and is not repeated here. Its property-child,
	// aws_cloudwatch_event_target, folds onto it and is deferred with the
	// rest of this batch's property-children, for the same missing-
	// mechanism reason.
	TypeIdentity{
		Type:          "aws_cloudwatch_event_api_destination",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_archive",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_bus",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_connection",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_cloudwatch_event_endpoint",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	// aws_cloudwatch_event_permission and aws_cloudwatch_event_bus_policy
	// are documented synonyms — both map to the one CloudFormation resource
	// AWS::Events::EventBusPolicy, an allowlisted mapping-gen contradiction
	// (tools/mapping-gen/mapping_gen_test.go's Former2Contradictions
	// allowlist: "aws_cloudwatch_event_bus_policy manages a whole EventBridge
	// bus policy document; aws_cloudwatch_event_permission manages one
	// statement in it") — but they do not share a ratification verdict.
	// row-gen filed both needs-hand-separator over the registry's composite
	// [EventBusName, StatementId]. The provider's real Identity Schema
	// disagrees for each, in opposite directions:
	//
	//   - aws_cloudwatch_event_permission's Identity Schema requires only
	//     statement_id (event_bus_name is not part of it at all, only an
	//     optional account_id/region), and statement_id is a required
	//     configuration argument. Single-component, resolves concrete
	//     whenever the resource does. Ratified below, correcting row-gen's
	//     classification the same way aws_xray_sampling_rule below does.
	//   - aws_cloudwatch_event_bus_policy's Identity Schema requires
	//     event_bus_name, but the provider's own Argument Reference marks
	//     event_bus_name (Optional) — "if you omit this, the permissions are
	//     set on the default event bus" — so a real, valid configuration may
	//     have no argument for this component to read at all. This is
	//     exactly live/survey-full.json's own "needs-config-signal"
	//     classification for this type (identity attrs settable but not
	//     required arguments), and the same missing-literal-default gap the
	//     messaging batch already declined for aws_cloudwatch_event_rule.
	//     Rejected, not deferred: the gap is a table mechanism this batch
	//     does not build, not missing evidence.
	TypeIdentity{
		Type:          "aws_cloudwatch_event_permission",
		Components:    []Component{attr("statement_id")},
		ImportSyntax:  "STATEMENT_ID",
		IdentityAttrs: []string{"statement_id"},
	},

	// Step Functions remainder: aws_sfn_activity only, per this batch's own
	// scope (aws_sfn_state_machine is already admitted above;
	// aws_sfn_alias is a state-machine alias, out of this batch's named
	// scope and left for a future one).
	serverAssigned("aws_sfn_activity",
		"Step Functions assigns the activity's own ARN at create time; the name argument is client-chosen but the provider's documented import command uses the ARN, which wraps it in an account and a region the configuration does not carry — the same shape as aws_sfn_state_machine above.",
		"ARN"),

	// X-Ray.
	serverAssigned("aws_xray_group",
		"X-Ray assigns the group's own ARN at create time; group_name is a required argument but the provider's documented import command uses the ARN, not the name. Confirmed against the provider's own Import section.",
		"ARN"),
	TypeIdentity{
		Type:          "aws_xray_resource_policy",
		Components:    []Component{attr("policy_name")},
		ImportSyntax:  "POLICY_NAME",
		IdentityAttrs: []string{"policy_name"},
	},
	// aws_xray_sampling_rule: row-gen filed this evidence-only, reading only
	// the registry's read-only RuleARN. The provider's real Argument
	// Reference requires rule_name (already in configuration) and its
	// Import section's example ID is the rule name verbatim ("example-rule"),
	// the same registry-vs-provider mismatch the CloudWatch corrections
	// above share.
	TypeIdentity{
		Type:          "aws_xray_sampling_rule",
		Components:    []Component{attr("rule_name")},
		ImportSyntax:  "RULE_NAME",
		IdentityAttrs: []string{"rule_name"},
	},

	// Grafana. row-gen's proposal already matches the provider: no
	// argument in aws_grafana_workspace's schema is required (name is
	// optional and is not the identity even when set), and the provider
	// mints an opaque "g-..." workspace id at create time, confirmed
	// against the Import section's example.
	serverAssigned("aws_grafana_workspace",
		"Grafana assigns the workspace's own id (g-...) at create time; every argument, including the optional name, describes the workspace but does not identify it.",
		"ID"),

	// RUM.
	TypeIdentity{
		Type:          "aws_rum_app_monitor",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	// Synthetics. aws_synthetics_group_association folds onto
	// AWS::Synthetics::Group and is deferred with this batch's other
	// property-children.
	TypeIdentity{
		Type:          "aws_synthetics_canary",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		Type:          "aws_synthetics_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	// ---- Registry-ratified (#40, #44, #65): fifth batch, streaming and
	// ---- app integration ------------------------------------------------
	//
	// Same pipeline as the batches above: every row started as a
	// tools/row-gen proposal from live/registry.json, cross-checked against
	// the AWS provider's documented Argument Reference, Attribute Reference
	// and Import section (fetched from the provider's own website/docs/r/
	// source at the pinned v6.59.0 tag) and, for the types the pinned
	// release ships one, against its own ResourceIdentitySchema
	// (live/survey-full.json's required_for_import field) — not accepted on
	// the registry's classification alone. Cohort estate:
	// live/e2e/estates/streaming.
	//
	// MSK's scope this batch is deliberately narrow — clusters,
	// configurations and the serverless cluster, plus MSK Connect (which
	// live/mapping.json carries under AWS::KafkaConnect::*, a
	// via:"service-alias" row, not AWS::MSK::* itself). row-gen's MSK
	// section proposes six more types (aws_msk_cluster_policy,
	// aws_msk_replicator, aws_msk_single_scram_secret_association,
	// aws_msk_vpc_connection all pastable; aws_msk_scram_secret_association
	// and aws_msk_topic evidence-only) that this batch does not evaluate at
	// all — deferred to a future batch's scope, not rejected on the merits.
	// SWF never entered scope: live/mapping.json carries aws_swf_domain as
	// via:"cfn-unmodeled" (tools/mapping-gen/overlay.d/sweep-servicequotas-
	// xray.json's own note: "all three services have zero matching CFN
	// Registry types at all"), so row-gen prints nothing for it.
	//
	// Rejected, and deliberately absent from this table:
	//
	//   - aws_appsync_api: row-gen proposed server-assigned via the
	//     registry's ApiArn (AWS::AppSync::Api's primaryIdentifier). The
	//     provider disagrees: its documented import command (terraform
	//     import aws_appsync_api.example example-api-id) and Attribute
	//     Reference both name the identifier api_id, not the arn — the
	//     same registry-says-one-field-but-the-provider-imports-by-another
	//     shape the earlier batches' rejections established (e.g. the
	//     messaging batch's aws_cloudwatch_event_rule). api_id is itself a
	//     registry readOnlyProperty, so a corrected row is possible in
	//     principle, but this batch does not write one — see
	//     live/e2e/estates/streaming/README.md for why.
	//   - aws_appsync_api_cache, aws_appsync_api_key: row-gen proposed both
	//     server-assigned off registry readOnlyProperties (Id, ApiKeyId).
	//     Independent verification of live/registry.json itself finds both
	//     CFN types' handlers block is create/read/update/delete/list all
	//     false — the "registry-laggard" shape live/LIMITATIONS.md's
	//     Registry-laggard live services table already names both of these
	//     exact types for (they are two of that table's existing rows). A
	//     registry entry with no working handler at all supplies no real
	//     evidence, whatever its primaryIdentifier field claims; row-gen's
	//     classifier does not check handler liveness, so this is exactly
	//     the kind of proposal the ratification step exists to catch
	//     instead.
	//   - aws_appsync_domain_name_api_association: row-gen proposed
	//     server-assigned via the registry's ApiAssociationIdentifier
	//     (AWS::AppSync::DomainNameApiAssociation's primaryIdentifier). The
	//     provider disagrees outright, not just on which field: its
	//     documented import command (terraform import
	//     aws_appsync_domain_name_api_association.example example.com)
	//     imports by the client-supplied domain_name argument, already in
	//     configuration — this type is client-named, not server-assigned,
	//     and the registry's own claim is simply wrong for what this
	//     provider does.
	//   - aws_appsync_function: row-gen proposed server-assigned via the
	//     registry's FunctionArn (AWS::AppSync::FunctionConfiguration's
	//     primaryIdentifier, a single field). The provider disagrees: its
	//     documented import command (terraform import
	//     aws_appsync_function.example xxxxx-yyyyy) is a hyphen-joined
	//     composite of api_id (a configured argument) and function_id (a
	//     server-assigned output first available only after creation) —
	//     the registry's single-field claim understates the real shape the
	//     same way aws_cloudwatch_event_rule's did in the messaging batch,
	//     and the correction needs a component this table's vocabulary
	//     does not have (composing a configured argument with the type's
	//     own not-yet-created output), not just a separator guess.
	//   - aws_scheduler_schedule: row-gen itself never proposed this row —
	//     its "name" argument was GUESSED, not backed by an identity
	//     schema, live/import-grammar.json or the carve seed, so it landed
	//     evidence-only. Independent verification confirms row-gen's own
	//     caution was warranted: the provider's documented import ID is
	//     the composite group_name/name (terraform import
	//     aws_scheduler_schedule.example my-schedule-group/my-schedule),
	//     and its own v6.59.0 identity schema lists both group_name and
	//     name as required_for_import but neither as a required argument
	//     (live/survey-full.json: "identity attrs (group_name, name) are
	//     settable but not required arguments, so client-naming is
	//     unprovable from the schema"). This batch's mandate is to ratify
	//     what row-gen proposes; a row it never proposed stays out.

	serverAssigned("aws_mq_broker",
		"Amazon MQ assigns the broker's own id (a UUID) at create time; broker_name is client-chosen but names the broker, not its identity. Confirmed against the provider's documented import command (terraform import aws_mq_broker.example a1b2c3d4-d5f6-7777-8888-9999aaaabbbbcccc) and its Attribute Reference, which states id is \"the unique ID that Amazon MQ generates for the broker.\"",
		"ID", "id"),
	serverAssigned("aws_mq_configuration",
		"Amazon MQ assigns the configuration's own id (c-…) at create time; name is client-chosen but names the configuration, not its identity. Confirmed against the provider's documented import command (terraform import aws_mq_configuration.example c-0187d1eb-88c8-475a-9b79-16ef5a10c94f) and its Attribute Reference, which states id is \"the unique ID that Amazon MQ generates for the configuration.\"",
		"ID", "id"),

	serverAssigned("aws_msk_cluster",
		"MSK mints the cluster's own ARN at create time, embedding a UUID it assigns itself; cluster_name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's own v6.59.0 identity schema (required_for_import: arn) and its documented import command (terraform import aws_msk_cluster.example arn:aws:kafka:us-west-2:123456789012:cluster/example/279c0212-d057-4dba-9aa9-1c4e5a25bfc7-3).",
		"ARN", "arn"),
	serverAssigned("aws_msk_configuration",
		"MSK mints the configuration's own ARN at create time; name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_msk_configuration.example arn:aws:kafka:us-west-2:123456789012:configuration/example/279c0212-d057-4dba-9aa9-1c4e5a25bfc7-3). Untaggable — its Argument Reference names no tags block at all, and live/registry.json's own tagging.taggable is false — so it reaches this table only through the registry-backed path, not the marker path; see live/e2e/estates/streaming/README.md, \"Untaggable types.\"",
		"ARN", "arn"),
	serverAssigned("aws_msk_serverless_cluster",
		"MSK mints the serverless cluster's own ARN at create time; cluster_name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's own v6.59.0 identity schema (required_for_import: arn) and its documented import command (terraform import aws_msk_serverless_cluster.example arn:aws:kafka:us-west-2:123456789012:cluster/example/279c0212-d057-4dba-9aa9-1c4e5a25bfc7-3).",
		"ARN", "arn"),

	// MSK Connect: live/mapping.json's AWS::KafkaConnect::* rows reach
	// these three aws_mskconnect_* types by via:"service-alias", not a
	// direct AWS::MSK::* mapping — the CFN service is named KafkaConnect,
	// the TF resources keep the mskconnect_ prefix.
	serverAssigned("aws_mskconnect_connector",
		"KafkaConnect (MSK Connect) mints the connector's own ARN at create time; connector_name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_mskconnect_connector.example 'arn:aws:kafkaconnect:eu-central-1:123456789012:connector/example/264edee4-17a3-412e-bd76-6681cfc93805-3').",
		"ARN", "arn"),
	serverAssigned("aws_mskconnect_custom_plugin",
		"KafkaConnect mints the custom plugin's own ARN at create time; name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_mskconnect_custom_plugin.example 'arn:aws:kafkaconnect:eu-central-1:123456789012:custom-plugin/debezium-example/abcdefgh-1234-5678-9abc-defghijklmno-4').",
		"ARN", "arn"),
	serverAssigned("aws_mskconnect_worker_configuration",
		"KafkaConnect mints the worker configuration's own ARN at create time; name is client-chosen but does not reconstruct the ARN. Confirmed against the provider's documented import command (terraform import aws_mskconnect_worker_configuration.example 'arn:aws:kafkaconnect:eu-central-1:123456789012:worker-configuration/example/8848493b-7fcc-478c-a646-4a52634e3378-4').",
		"ARN", "arn"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[ConnectorProfileName], in
		// createOnlyProperties and not in readOnlyProperties — client-named,
		// row-gen proposed it correctly the first time. Confirmed against
		// the provider's own v6.59.0 identity schema (required_for_import:
		// name) and its documented import command (terraform import
		// aws_appflow_connector_profile.example example-profile). Untaggable
		// — its Attribute Reference exports only arn and credentials_arn,
		// no tags, and live/registry.json's own tagging.taggable is false
		// — so it reaches this table only through the registry-backed
		// path; see live/e2e/estates/streaming/README.md, "Untaggable
		// types."
		Type:          "aws_appflow_connector_profile",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[FlowName], client-named,
		// proposed correctly. Confirmed against the provider's own v6.59.0
		// identity schema (required_for_import: name) and its documented
		// import command (terraform import aws_appflow_flow.example
		// example-flow).
		Type:          "aws_appflow_flow",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"name"},
	},

	serverAssigned("aws_appsync_graphql_api",
		"AppSync mints the GraphQL API's own id at create time; the registry's primaryIdentifier for this type, ApiId, is exactly the value the provider's own documented import command uses (terraform import aws_appsync_graphql_api.example 0123456789) and exports as both id (\"API ID\") and arn (\"ARN\") — the one AppSync proposal in this batch's scope where the registry and the provider agree; see the rejected-proposals note above for the four AppSync siblings where they do not.",
		"ID", "id", "arn"),

	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], client-named, proposed
		// correctly (row-gen's argument line came from
		// live/import-grammar.json). Confirmed against the provider's
		// documented import command (terraform import aws_pipes_pipe.example
		// my-pipe) and its Attribute Reference, which states id is "Same as
		// name."
		Type:          "aws_pipes_pipe",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
	},
	TypeIdentity{
		// registry.json: primaryIdentifier=[Name], client-named, proposed
		// correctly. Confirmed against the provider's documented import
		// command (terraform import aws_scheduler_schedule_group.example
		// my-schedule-group) and its Attribute Reference, which states id
		// is "Name of the schedule group." aws_scheduler_schedule, the
		// sibling type one section below in the provider's own docs, is
		// not ratified here — see the rejected-proposals note above.
		Type:          "aws_scheduler_schedule_group",
		Components:    []Component{attr("name")},
		ImportSyntax:  "NAME",
		IdentityAttrs: []string{"id", "name"},
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
