# route53-cloudfront cohort

Cohort: `route53-cloudfront`. Ratified by: the fourth registry-backed
ratification batch against #40's strategy and #44's row generator, covering
issue #65's "Route53 remainder and CloudFront" suggestion. Mechanism: #48
(see `live/e2e/estates/example/README.md` for the proof-of-mechanism cohort,
and `live/e2e/estates/lambda/README.md` for the first registry-backed
cohort this one follows).

This cohort exercises every type this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`: 30
types across five CFN services (Route53, Route53Profiles,
Route53RecoveryControl, Route53Resolver, CloudFront), rejecting 13 more and
landing one, `aws_cloudfront_origin_access_control`, that briefly looked
like a deferral before turning out not to need one — see "Rejected" and
"A near-deferral that wasn't" below.

Two types this batch's scope names, `aws_route53_zone` and
`aws_route53_record`, are already admitted from before the registry
pipeline existed and are not repeated here; several rows below are wired as
composites through the zone marker the same way `aws_route53_record`
already is, following that row's own precedent exactly (issue #65's own
instruction).

## Coverage map

### Marker path (server-assigned, taggable)

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_route53_health_check.app` | Route 53 mints the health check's own id (a UUID); nothing in configuration reconstructs it. |
| `aws_route53profiles_association.app` | Route 53 Profiles mints the association's own id (`rpa-id-…`). |
| `aws_route53profiles_profile.app` | Route 53 Profiles mints the profile's own id (`rp-…`); the `name` argument is client-chosen but is not the import identity. |
| `aws_route53recoverycontrolconfig_cluster.app` | Route53 Recovery Control mints the cluster's own ARN. |
| `aws_route53recoverycontrolconfig_control_panel.app` | Route53 Recovery Control mints the panel's own ARN. |
| `aws_route53recoverycontrolconfig_safety_rule.app` | Route53 Recovery Control mints the safety rule's own ARN. |
| `aws_route53_resolver_endpoint.app` | Route53Resolver mints the endpoint's own id (`rslvr-in-…`). |
| `aws_route53_resolver_firewall_domain_list.app` | Route53Resolver mints the domain list's own id (`rslvr-fdl-…`). |
| `aws_route53_resolver_firewall_rule_group.app` | Route53Resolver mints the rule group's own id (`rslvr-frg-…`). |
| `aws_route53_resolver_firewall_rule_group_association.app` | Route53Resolver mints the association's own id (`rslvr-frgassoc-…`). |
| `aws_route53_resolver_query_log_config.app` | Route53Resolver mints the query log config's own id (`rqlc-…`). |
| `aws_route53_resolver_rule.app` | Route53Resolver's identity schema requires the rule's own server-assigned id (`rslvr-rr-…`). |
| `aws_cloudfront_anycast_ip_list.app` | CloudFront mints the anycast IP list's own id. |
| `aws_cloudfront_connection_function.app` | CloudFront mints the connection function's own id. |
| `aws_cloudfront_connection_group.app` | CloudFront mints the connection group's own id. |
| `aws_cloudfront_distribution.app` | CloudFront's identity schema requires the distribution's own server-assigned id. Precedented in `live/SURVEY.md` before row-gen existed; reclassified `wired` this batch — see "Verifying by hand". |
| `aws_cloudfront_distribution_tenant.app` | CloudFront mints the distribution tenant's own id. |
| `aws_cloudfront_multitenant_distribution.app` | CloudFront mints the multi-tenant distribution's own id, the same shape as the plain distribution above. |
| `aws_cloudfront_trust_store.app` | CloudFront mints the trust store's own id. |
| `aws_cloudfront_vpc_origin.app` | CloudFront mints the VPC origin's own id. |

### Marker path, list plus content match (server-assigned, untaggable)

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_route53_resolver_rule_association.app` | Untaggable, but the provider ships a native list resource for this one type in the family, whose results carry `resolver_rule_id` and `vpc_id` — the same two required arguments the association's create call takes — so listing and matching by that content recovers a specific instance, the mechanism `aws_eip` above uses with a tag standing in for content instead. |
| `aws_cloudfront_origin_access_control.app` | Untaggable (CloudFront has no per-OAC tagging API), but the required `name` argument is AWS-enforced unique per account, so listing every OAC and matching on `name` recovers a specific instance without a tag. `live/SURVEY.md` worked this classification out by hand before row-gen existed; this batch honors it rather than second-guessing it — see "A near-deferral that wasn't". |

### Client-named path

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_cloudfront_function.app` | Identity is the `name` argument, already in config. row-gen itself declined to propose this (its provider has no Identity Schema for the type in v6.58.0), but `live/import-grammar.json`'s docs-derived evidence and `live/registry.json`'s own row agree with the provider's documented import command. |
| `aws_cloudfront_key_value_store.app` | Identity is the `name` argument, already in config — row-gen proposed this correctly the first time, confirmed against the provider's own identity schema. |

### Account-derived path

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_cloudfront_realtime_log_config.app` | row-gen proposed server-assigned via the registry's `Arn`; right that the ARN is the identity, but this is the account-derived shape `aws_sns_topic`/`aws_sqs_queue` already have, not an opaque id — the ARN is built from the required `name` argument plus the run's account, with no region segment since CloudFront is global. |

### Parent-derived path

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| `aws_route53_hosted_zone_dnssec.app` | Named-singleton-child of the zone, the same shape as `aws_s3_bucket_policy`: at most one per hosted zone, and the import id is literally the parent zone's own id, verbatim — through the `aws_route53_zone` marker. |
| `aws_route53_key_signing_key.app` | Composite `hosted_zone_id,name`, comma-separated per the provider's own documented import command — the zone's id through its marker, `name` a client-chosen argument. row-gen classified this "needs hand separator"; independent verification against the docs supplies the separator. |
| `aws_route53_zone_association.app` | Composite `zone_id:vpc_id`, colon-separated per the provider's own Identity Schema — both already-admitted parents (`aws_route53_zone`'s marker, `aws_vpc`'s marker). A composite of two required identity attributes is exactly what `SynthesizeTypeIdentity` refuses to self-admit, so this needs the hand row despite having a full Identity Schema. |
| `aws_route53_resolver_firewall_rule.app` | Composite `firewall_rule_group_id:firewall_domain_list_id`, colon-separated per the provider's own documented import command — both already ratified above, in this same batch, as their own marker rows. Only the standard-rule shape; an advanced rule (`dns_threat_protection` set instead) simply fails to resolve rather than guessing, the same honest half-coverage `aws_route53_record`'s own `set_identifier` caveat accepts. |
| `aws_cloudfront_monitoring_subscription.app` | Named-singleton-child of the distribution, the same shape as the DNSSEC config above: at most one per distribution, and the import id is literally `distribution_id` — confirmed directly in the provider's own Attribute Reference ("id … corresponds to the distribution_id"). |

## Rejected

`tools/row-gen`'s Route53, Route53Profiles, Route53RecoveryControl and
Route53Resolver sections proposed several types this batch's independent
verification rejects — every one confirmed server-assigned on the
provider's own docs (row-gen was right about that much), but genuinely
unrecoverable: untaggable (no `live/tag-verbs.json` service-wide tagging
operation covers the type, and the provider's own schema carries no `tags`
argument), not an account- or parent-wide singleton the way
`aws_ecr_registry_policy`'s account-scoped ECR registry id is (the IAM/ECR
batch's own precedent for admitting an untaggable server-assigned type
anyway), and not composable from an admitted parent's value the way the
parent-derived rows above are:

- **`aws_route53_cidr_collection`** — row-gen proposed server-assigned via
  the registry's opaque `Id`, confirmed against the provider's Import
  section. Route53's own tagging operation, `ChangeTagsForResource`
  (`live/tag-verbs.json`), takes a `ResourceType` parameter AWS documents as
  accepting only `healthcheck` and `hostedzone` — a CIDR collection is
  neither, and the provider's schema for this type carries no `tags`
  argument at all. An account may hold arbitrarily many collections with no
  marker to tell them apart.
- **`aws_route53_cidr_location`** — row-gen folded this into
  `AWS::Route53::CidrCollection`'s own registry entry and proposed
  parent-derived admission "once [the collection] is ratified". On
  independent verification its import grammar does turn out to be exactly
  that composite (`COLLECTIONID,LOCATIONNAME`, confirmed against the
  provider's Import section and Argument Reference) — the same shape as the
  parent-derived rows above. It stays out anyway, and only for the reason
  just above: its parent is rejected, so nothing admits the live collection
  id a `cidr_collection_id` argument would need to resolve to.
- **`aws_route53_query_log`** — row-gen folded this into
  `AWS::Route53::HostedZone`'s own registry entry and proposed
  parent-derived admission through `aws_route53_zone`, the same
  speculative note as the fold above. Verification does not bear it out:
  the provider's documented import id
  (`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`) is a bare, opaque UUID the
  query-logging subscription's own create call mints — not `hosted_zone_id`,
  and not any concatenation of it with another configured value. Query
  logging is not its own CloudFormation-registered resource type at all (a
  nested property of `AWS::Route53::HostedZone`), so there is no Cloud
  Control list handler to enumerate it by either, and the type carries no
  tags argument. row-gen's own "no pastable row" verdict was the honest
  one; the fold's speculative note does not survive independent
  verification.
- **`aws_route53recoverycontrolconfig_routing_control`** — row-gen proposed
  server-assigned via the registry's opaque `RoutingControlArn`, confirmed
  against the provider's own Import section and Attribute Reference.
  Unlike its three Route53RecoveryControl siblings ratified above, this
  type's schema carries no `tags` argument, and the ARN's own
  routing-control segment (`d5d90e587870494b` in the documented example) is
  a second, independent server-minted token — not the required `name`
  argument the provider docs also list, so it is not literally composable
  from configuration plus an admitted parent's ARN either.
- **`aws_route53_resolver_dnssec_config`** — row-gen proposed
  server-assigned via the registry's opaque `Id`, confirmed against the
  provider's Import section (`rdsc-…` id, not the type's sole `resource_id`
  argument, which names a VPC — `aws_vpc` is already admitted, but the
  DNSSEC config's own import id is a second, unrelated server-minted token,
  so unlike `aws_route53_hosted_zone_dnssec` above, whose import id *is*
  literally its parent's own id, this one cannot be built as a
  parent-derived composite). Untaggable.
- **`aws_route53_resolver_query_log_config_association`** — row-gen
  proposed server-assigned via the registry's opaque `Id`, confirmed
  against the provider's Import section (`rqlca-…` id, unrelated to the
  `resolver_query_log_config_id`/`resource_id` arguments it composes from
  at create time). Untaggable, same shape as the DNSSEC config just above.
- **`aws_route53profiles_resource_association`** — row-gen proposed
  server-assigned via the registry's opaque `Id`, confirmed against the
  provider's Import section (`rpa-id-…` id, unrelated to its
  `profile_id`/`resource_arn`/`name` arguments). Unlike its two
  Route53Profiles siblings ratified above, `live/registry.json` shows no
  `Tags` in this type's `createOnlyProperties`, and
  `live/survey-full.json` confirms the provider's schema carries none
  either.
- **`aws_cloudfront_cache_policy`, `aws_cloudfront_continuous_deployment_policy`,
  `aws_cloudfront_key_group`, `aws_cloudfront_origin_access_identity`,
  `aws_cloudfront_origin_request_policy`, `aws_cloudfront_public_key`,
  `aws_cloudfront_response_headers_policy`** — row-gen proposed all seven
  server-assigned via each type's registry-opaque `Id`, confirmed against
  the provider's Import sections (each imports by a bare, server-minted id
  unrelated to the type's own required arguments). None carries a `tags`
  argument, an account may hold arbitrarily many of each, and none is a
  parent-derived composite the way `aws_cloudfront_monitoring_subscription`
  is. `aws_cloudfront_origin_access_identity` in particular has no strong
  content-match key either (its only argument, `comment`, is optional and
  not AWS-enforced unique), which is what tells it apart from
  `aws_cloudfront_origin_access_control` just below.

Out of scope, never proposed by row-gen at all: `aws_route53_resolver_config`
(evidence-only — its `resource_id` argument name is guessed, not backed by a
provider identity schema or the carve seed) and the whole
Route53RecoveryReadiness service (all four types evidence-only, same
guessed-argument reason) — issue #65's recipe names "Route53Profiles/
RecoveryControl if proposed" and is silent on RecoveryReadiness for exactly
that reason.

## A near-deferral that wasn't

`aws_cloudfront_origin_access_control` looked, at first, like it would
repeat the messaging batch's `aws_sns_topic_subscription` story: identity
sound (server-assigned via list-plus-content match, honoring
`live/SURVEY.md`'s own prior hand analysis rather than row-gen's flatter
"server-assigned" proposal), but untaggable *and* one of `live/survey.json`'s
curated 68 rows — exactly the combination that obligated
`live/LIMITATIONS.md`'s "Untaggable types cannot be removed by the sweep"
entry and got the messaging batch's type deferred rather than ratified,
because that entry's derivation only covered the curated 68 intersected
with the admission table at the time.

It does not repeat that story, because issue #54 landed between the two
batches: `tools/survey-gen/untaggable_render.go` now derives that entry's
roster from `live/survey-full.json`'s taggability signal across the
*entire* registry-backed roster, not the curated 68 alone
(`internal/live/stamp/stamp_test.go`'s `untaggableOutsideCuratedSurvey`
split that the deferral leaned on is gone — see that file's own comment).
Admitting an untaggable curated-68 type is now the same mechanical doc
fixup any other untaggable type already needs (`go run ./tools/survey-gen
-render` picks it up, see the diff this batch's commit carries in
`live/LIMITATIONS.md` and `live/SURVEY.md`), not the hand-edit the
messaging batch's stated mandate put out of scope. So the type is ratified
above, not deferred, and `live/SURVEY.md`'s own row for it moves from
`blocked-emulator` to `wired` alongside `aws_cloudfront_distribution`'s —
see "Verifying by hand".

## Untaggable types

Eight of the thirty rows above carry no `tags` argument in the AWS
provider: `aws_route53_hosted_zone_dnssec`, `aws_route53_key_signing_key`,
`aws_route53_zone_association`, `aws_route53_resolver_firewall_rule`,
`aws_route53_resolver_rule_association`,
`aws_cloudfront_monitoring_subscription`, `aws_cloudfront_origin_access_control`
and `aws_cloudfront_realtime_log_config` — the same shape the twelve
untaggable types `live/e2e/estate/README.md` names, the Lambda batch's
`aws_lambda_layer_version`, and the messaging batch's
`aws_cloudwatch_dashboard`. All eight **are** added to
`live/LIMITATIONS.md`'s "Untaggable types cannot be removed by the sweep"
entry and to `internal/live/stamp/stamp_test.go`'s
`untaggableAdmittedTypes` pin — none of them needs the
untaggable-outside-curated-68 workaround the Lambda and messaging batches'
untaggable rows needed, because issue #54 retired that workaround; see "A
near-deferral that wasn't" above for the one row in this batch,
`aws_cloudfront_origin_access_control`, that would have needed it under the
old derivation.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"route53-cloudfront-cohort"`, distinct from every other cohort's own tag. |
| `route53-cloudfront.tf` | All thirty ratified types. No supporting resources: every parent-derived row's parent (`aws_route53_zone`, `aws_vpc`, or another type ratified in this same batch) is either already covered by `live/e2e/estate/` or is itself a coverage row here, and `tools/estate-gen`'s generic pass reads placeholder cross-references or literals for the rest. |

### Overrides

`tools/estate-gen/overrides.go` carries seventeen entries for this cohort —
the residual, hand-written surface issue #56's own design keeps "visible
and rare": a provider-side requirement `terraform validate` catches that
the wire schema alone does not name (an enum member no
`configschema.Attribute` marks, a block the schema calls Optional+Computed
but the provider requires present in practice, a set-nesting block whose
`MinItems` the generic pass's identical placeholders collapse to fewer
distinct elements than the minimum). Every entry cites the exact
`terraform validate` (and, for `aws_route53_resolver_firewall_rule`, the
`terraform apply`-time) error it exists to fix; the command above
regenerates `route53-cloudfront.tf` with every override applied, and each
resource block there carries its own `# overrides: ...` comment naming
which one (or `# overrides: none` for the thirteen rows the generic
required-only pass alone rendered validate-clean).

## Gating

Nothing here runs against a live or emulated cloud automatically. `go test
./internal/live/lint/... ./internal/live/identity/...` picks this directory
up through `internal/live/flocitest.FixtureDirs` (#48's union pin) and
checks it by static HCL parse — `TestAdmissionTableCoversEstate` and
`TestTableCoversFixtureTypes` require `admittedTypesV0` and `DefaultTable`
to cover exactly the union of `live/e2e/estate/` and every `estates/*`
cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci integration
test still stand up only the demo estate. Wiring cohort estates into the
gated apply-against-floci tier is separate follow-on work, the same note
every earlier cohort's README already makes.

## Verifying by hand

```
docker run -d --rm -p 4620:4566 --name tofu-route53-cloudfront-cohort-verify \
  ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34

export AWS_ENDPOINT_URL=http://localhost:4620 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-route53-cloudfront-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0) this
fixture pins — confirming every argument name above is real, not just
registry-plausible.

An `apply` against the pinned `lex00/floci` image was run by hand during
ratification (not wired into any automated tier — see "Gating" above), and
found the emulator's actual coverage narrower than its health check
advertises, the same split every earlier batch found:

- `aws_route53_health_check.app` and `aws_cloudfront_distribution.app`
  created (and read back) cleanly — the distribution took 30s but returned
  a real id (`EZP84OQ4P3LEU5`), confirming `live/SURVEY.md`'s note that
  lex00/floci#29's lifecycle fix landed in the image this checkout pins,
  closing the gap that row's `blocked-emulator` status used to name. A
  standalone check with the raw AWS CLI (not wired into this fixture, since
  it needs no Terraform state to demonstrate) confirmed
  `aws_cloudfront_origin_access_control` creates and lists cleanly too.
- `aws_cloudfront_function.app` and `aws_route53_hosted_zone_dnssec.app`
  both accepted their create call but then failed the provider's
  post-create read-back (`DescribeFunction`/`EnableHostedZoneDNSSEC`
  returning a bare `404 UnknownError`) — the same "advertises the service,
   not the operation" gap the messaging batch found for CloudWatch.
- `aws_cloudfront_multitenant_distribution.app` hit a distinct emulator
  bug: the provider reported "Provider produced inconsistent result after
  apply" (`.restrictions` and `.tenant_config` block counts changed between
  the plan and the read-back), a floci response-shape gap, not a
  configuration error — `terraform validate` and the plan both accepted the
  resource cleanly.
- `aws_cloudfront_key_value_store.app`, `aws_cloudfront_monitoring_subscription.app`,
  `aws_cloudfront_vpc_origin.app`, `aws_route53_resolver_endpoint.app`,
  `aws_route53_resolver_rule.app`, `aws_route53_resolver_rule_association.app`
  and `aws_route53_zone_association.app` all failed outright with `404`s —
  `UnknownOperationException` for the three Route53Resolver create calls
  (the operations are not implemented at all, not merely unadvertised) and
  bare `UnknownError`/`NoSuchDistribution` for the CloudFront and Route53
  Zone Association ones.

None of this is evidence against any row's admission above: identity and
enumeration are properties of the provider and the registry, not of one
emulator's completeness, the same standing the messaging batch's own
"Verifying by hand" section established for its three CloudWatch rows.
`choudoufu#26` is the umbrella tracking issue.
