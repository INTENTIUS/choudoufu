// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package identity classifies the identity of every managed resource
// instance in a configuration, using nothing but the configuration itself.
// It is the first half of stateless mode's answer to "what already exists":
// before anything is read from the cloud, this package decides which
// resources OpenTofu can already name, which ones it can name once their
// parents are known, and which ones only a discovery pass can find.
//
// # The three classes
//
// Every managed resource instance lands in exactly one class. One input
// besides the configuration can move an instance between them, and only one:
// a [CloudContext], the account and region of the cloud the run is against,
// which [ResolveIn] takes and [Resolve] leaves empty. It exists because a
// handful of AWS import identities are a configured name wrapped in the
// account and the region (an SQS queue URL, an SNS topic ARN), and nothing
// in a configuration says which account it will be applied to. A run that
// supplies neither value is not an error and is not a guess: those instances
// classify [ClassNeedsDiscovery], naming the property they lack, and marker
// discovery finds them. That fallback is what lets this package take the
// parameter without acquiring a cloud.
//
//   - [ClassConcrete]: the import identity is fully computable from
//     configuration right now, and [Resolution.ImportID] holds it. This is
//     the roadmap's admission path 1 (client-assigned identity): an S3
//     bucket name, an IAM role name, a log group name. It also covers a
//     resource whose identity is composed from parents that are themselves
//     concrete, such as an aws_iam_role_policy_attachment built from a
//     client-named role and a literal policy ARN, or an
//     aws_s3_bucket_policy built from its bucket's name.
//   - [ClassParentDerived]: the identity is known symbolically but not
//     concretely, because part of it is a parent's live, server-assigned
//     ID. [Resolution.Formula] holds the composition: which parent
//     instances, which literal fragments, and in what order. The projection
//     builder (P1.3) renders the formula into a real import ID once the
//     parents' live IDs are known. This is admission path 3.
//   - [ClassNeedsDiscovery]: the identity is assigned by the server and
//     appears nowhere in configuration, so it can only come from marker
//     discovery (P2). [Resolution.Reason] says why. This is admission
//     path 2.
//
// The distinction between the second and third classes is the one thing
// this package exists to make explicit. A parent-derived identity is
// *computable* from configuration in the sense that its shape, its inputs,
// and its composition are all fixed by the config; it is *not* computable
// as a string, because one of its inputs is a vpc-, subnet-, or rtb- ID
// that only the cloud knows. Modelling that as "unknown" would throw away
// everything the configuration does determine, and modelling it as "known"
// would be a lie. It is its own class, carrying a formula rather than a
// value.
//
// # What is evaluated statically, and how
//
// Resolution never contacts a provider and never reads state. Two
// mechanisms do all the work:
//
// Static evaluation. Expressions that contain no resource references are
// evaluated through the configuration's own [configs.StaticEvaluator] (the
// same machinery that evaluates module sources and backend arguments),
// extended with a child scope holding each.key/each.value and count.index
// for the instance being resolved. That covers literals, string templates,
// input variables, locals, functions, path.* and terraform.workspace, and
// arbitrary composition of those. Input variable values come from the
// [configs.StaticModuleCall] the configuration was loaded with, so a
// declared default is used when the caller supplied nothing, exactly as
// the rest of OpenTofu would.
//
// Structural resolution. An expression that does reference a managed
// resource is never evaluated; it is matched structurally. A bare
// traversal (aws_route_table.main.id, each.value.id where each.value is a
// parent instance) becomes a reference to that parent's identity
// attribute. A string template becomes a sequence of parts, each one
// either a literal or a parent reference, which is what makes a formula
// like "${aws_route_table.main.id}_0.0.0.0/0" expressible. A parent
// reference resolves to a literal when the parent is itself concrete, and
// to a symbolic part otherwise.
//
// Everything else is an error, not a guess. Specifically: an expression
// that mixes a resource reference into a function call or an arithmetic
// operation, a reference to an attribute that is not one of the parent
// type's identity attributes (a parent's cidr_block is not recoverable
// from config, and pretending otherwise would silently produce a wrong
// import ID), a reference to a data source or a module output, an unknown
// or null value, a sensitive value, and a value that cannot be converted
// to a string.
//
// # Expansion
//
// Instances, not resources, are classified, so count and for_each have to
// be expanded first:
//
//   - No count or for_each: one instance, [addrs.NoKey].
//   - count = <static number>: instances 0 through n-1. The number must be
//     statically evaluable, which makes the count = var.enabled ? 1 : 0
//     idiom work unchanged.
//   - for_each = <static map, object, or set of strings>: one instance per
//     key, sorted.
//   - for_each = <bare reference to another managed resource>: the keys are
//     propagated from that resource's own expansion, resolved recursively.
//     This case matters more than it looks: it is how
//     aws_route_table_association.this gets keyed off aws_subnet.this
//     without anyone knowing a single subnet ID. The parent must itself be
//     for_each-expanded, since a count-expanded resource is a tuple and
//     OpenTofu rejects a tuple as a for_each argument. Within such an
//     instance, each.value is the corresponding parent instance, so
//     each.value.id is a reference to that parent's live ID.
//   - lifecycle { enabled = <static bool> }: false yields zero instances.
//
// An expansion that is not statically knowable (a count whose value comes
// from a resource attribute, a for_each built by a computation over
// resource attributes) is an error naming the expression. Guessing a
// cardinality would silently drop or invent instances, which is the one
// failure mode a projection must never have.
//
// # Ambiguity
//
// Two instances of one resource type that resolve to the same identity are
// an error naming both addresses. They would bind to a single live
// resource, and a projection that binds one cloud object to two managed
// addresses plans nonsense. This is the same rule the marker path applies
// to two live resources claiming one address: name the ambiguity, never
// pick a winner.
//
// # The type table
//
// Per-type identity knowledge lives in [DefaultTable], a v0 hardcoded table
// covering thirty-one AWS types. See table.go for what it holds and for the
// note on replacing it with provider-served resource identity schemas.
//
// A type absent from it is no longer automatically outside the subset.
// [SynthesizeTypeIdentity] builds an entry from the provider's own resource
// identity schema for the shape that needs no inference at all - one identity
// attribute, named after the configuration argument that supplies it - when
// the caller had the schemas to hand ([Context.Schemas]). A caller that had
// none, which is every caller running before a plugin has started, gets the
// table and nothing else. Composite identities stay hand-written, because the
// character that joins their halves into an import ID is in no schema.
//
// # Checking the table against the provider's own schemas
//
// The table is asserted by hand, which for most of this package's life
// meant it could not be wrong in any way a test could see. It can now:
// providers serve resource identity schemas (opentofu#2854's
// providers.Schema.IdentitySchema), and [VerifyTable] checks the table's
// claims against them, with [Derivable] reporting the types those schemas
// would admit with no table entry at all.
//
// Neither runs during resolution. GetProviderSchema needs a plugin process
// and this package stays cloud-free and process-free, so the check happens
// one phase later, where a projection is already talking to a configured
// provider (internal/live/projection/schema_check.go is the seam and
// records what is done with each kind of result). The table is still what
// classification runs on.
//
// Against the real AWS provider (6.58.0) the check confirms most of the
// table's entries, cannot speak to aws_ecs_cluster because the provider
// serves no identity schema for it, and diverges on two. The archetype is
// aws_route_table_association: the provider identifies an association by
// the rtbassoc- ID it assigns, while the table builds the association's
// documented import *string* out of subnet and route table. Both are
// accurate about different things, which is the shape most divergences
// will have and the reason they are warnings.
//
// The other is aws_sns_topic, and it is the same shape once more. The
// provider says a topic is identified by its arn, which is true; the table
// says it is identified by the name in configuration wrapped in the run's
// region and account, which is also true and is the only one of the two a
// configuration can act on. The entry names the provider's attribute in its
// IdentityAttrs, so the check agrees about what the identity *is* and
// reports only that the table composes it out of an argument (name) the
// identity schema does not mention. See [CloudContext].
//
// # The Optional+Computed cohort, and what decides it
//
// An identity schema alone admits only types whose identity attributes are
// *required* arguments - 162 of the AWS provider's 468 identity-schema
// types - because Optional+Computed is ambiguous and that ambiguity is
// exactly where the interesting types live. The AWS provider's legacy-SDK
// schemas mark aws_s3_bucket's bucket argument Optional+Computed and mark
// aws_vpc's id attribute Optional+Computed too. Read from the schemas
// alone the two are the same shape, and they are opposite answers: bucket
// is the archetypal client-assigned name, id is the archetypal
// server-assigned one.
//
// A configuration separates them, per instance and without inference. A
// block that writes bucket = "…" is naming the object it owns; a block that
// writes bucket_prefix, or writes bucket = var.name with the variable
// defaulting to null, or writes nothing - which is every aws_vpc block ever
// written - is letting the cloud name it. [ConfigSignal] is that reading,
// collected on [Resolve]'s own walk (and by [ScanConfig] where there is no
// resolution to piggyback on), and [DerivableWith] is [Derivable] with it
// folded in. A type is admitted this way only when every instance of it in
// the configuration sets every attribute the provider requires for import;
// disagreement between two blocks of one type is reported as disagreement,
// because a row that half the configuration contradicts would import the
// wrong object for the other half.
//
// The signal is a claim, not a proof: it says the configuration asserts a
// name, which is the assertion an import has to act on either way. It costs
// nothing beyond a walk that was already happening, it reaches types the
// table has never heard of, and it never leaves this package's constraints -
// no provider, no process, no cloud read.
//
// [Report] is the machine-readable form, for the generator that would
// otherwise be hand-writing rows: which types the schemas admit, which ones
// this configuration admits, and - for a survey with no configuration in
// front of it - which ones are waiting on a configuration and exactly which
// arguments it would have to set.
//
// What is still missing before the table can shrink:
//
//   - The inference layer has no schema behind it. An identity schema names
//     the attributes that identify a type; it does not say which
//     configuration argument supplies each one. That is one hop for
//     aws_route (identity attribute route_table_id, read from the
//     route_table_id argument) and no hop at all for the types whose
//     identity attribute is the id the provider assigns. Nothing in the
//     protocol closes that gap, so the table's Components survive it.
//   - Import by identity is not wired. Once a projection imports by
//     identity object rather than by import-ID string, the separator
//     characters in Components stop mattering and half of what the table
//     asserts goes away on its own. Everything below this package is
//     already there - providers.ImportTarget carries an Identity cty.Value,
//     both plugin protocols marshal it, and internal/tofu's own import path
//     uses it - so the remaining work is entirely in this package and the
//     projection: [Resolution] would have to carry named identity
//     attributes rather than one concatenated string, which means
//     [Component] gaining the identity attribute each one supplies, and
//     [Formula] and its rendering becoming per-attribute. That is the
//     inference layer above, written down in the one place it can be
//     checked, rather than a way of avoiding it.
//
// # Output
//
// [Resolve] returns a [Result]: a lookup from resource instance address to
// [Resolution], plus the needs-discovery list the roadmap asks for. It is
// deliberately a plain data structure with no behaviour beyond rendering a
// formula, so that P1.3 can consume it directly.
package identity
