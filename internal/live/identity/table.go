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
)

func buildTable(entries ...TypeIdentity) map[string]TypeIdentity {
	m := make(map[string]TypeIdentity, len(entries))
	for _, e := range entries {
		m[e.Type] = e
	}
	return m
}

// registerCohortTable folds one per-cohort fragment of the identity table
// into [DefaultTable]. Every table_cohort_*.go file in this package calls it
// from its own init, which is why the ratification batches no longer share
// one 11,000-line literal: a batch adds a file rather than appending to a
// map every other batch is appending to at the same time.
//
// Package-level var initializers all run before any init func, whatever
// order the files are in, so this never races DefaultTable's own
// construction above - the same guarantee table_recordbacked.go has always
// relied on.
//
// A type defined twice is refused rather than silently overwritten, the rule
// tools/mapping-gen/overlay.d already applies to its own fragments: two
// cohorts claiming one type is a merge accident, not an intention, and the
// last file in filename order winning it silently is exactly the failure
// this split exists to prevent.
func registerCohortTable(fragment map[string]TypeIdentity) {
	for k, v := range fragment {
		if _, dup := DefaultTable[k]; dup {
			panic("identity: type " + k + " is defined by more than one cohort fragment")
		}
		DefaultTable[k] = v
	}
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
