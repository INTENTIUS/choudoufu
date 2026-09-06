# The harness

What this fork is driving down, and what it believes while it does.

Both halves are registries in `internal/live/harness`, and everything below
between a `harness-gen` marker pair is rendered from them by
`go run ./tools/harness-gen` (`just harness`). No figure here is typed by
hand; each one comes from the measurement function that owns it, run against
the committed artifacts at generation time. Change an artifact without
re-rendering and `TestHarnessDocIsCurrent` goes red, which is deliberate:
one of these ratchets sat above its own ledger for two separate batches
because the constant and the file it bounded had no way to disagree out loud.

The prose around the spans is hand-written and must stay free of figures.
`TestHandWrittenProseCarriesNoFigures` enforces that.

## Burndown

Each entry is a quantity with a direction and a bound. Two rules the entries
have to answer for, both of which came from an incident rather than from
taste:

**A ratchet pins its denominator.** Every count here is against some roster,
and shrinking the roster is always the cheaper way to make the count fall.
Each entry names the roster and a floor, or states in writing that it has no
roster to shrink. The floor is checked before the bound, and a roster below
its floor stops the measurement rather than merely failing it: a count
against a roster that shrank is not a smaller problem, it is a different
measurement.

**A ratchet does not measure itself.** A number read out of an artifact and
checked against that same artifact agrees with itself whatever it says. Each
entry names what it measures and the external thing it is held to, and the
registry refuses an entry where those are the same.

Each entry also records its instrument and what that instrument cannot see.
That field is not decoration. `tools/refusal-probe`'s default mode cannot see
the stamp layer, cannot see any rule that returns false without provider
schemas, and reports every resource of a non-AWS estate as unadmitted - and
"upper bound" is true of its site counts and false of its verdicts, because
blocked configurations *rise* when schemas are supplied. A reader who does
not know an instrument's blind spots will read its zeroes as evidence.

<!-- harness-gen:begin burndown -->
| quantity | now | bound | denominator | tracker |
| --- | ---: | ---: | --- | --- |
| [`mapping-unclassified`](#mapping-unclassified) | 13 | at most 13 | `live/mapping.json row count` at 1699, floor 1600 | #53 |
| [`markerless-veto-admitted-overlap`](#markerless-veto-admitted-overlap) | 0 | at most 0 | `internal/live/identity.MarkerlessTypes` at 159, floor 100 | #249 |
| [`rowgen-annotation-rulings`](#rowgen-annotation-rulings) | 151 | at most 151 | `live/rowgen-mismatches.json summary.admitted_total` at 1049, floor 850 | #132 |
| [`rowgen-unannotated-mismatches`](#rowgen-unannotated-mismatches) | 0 | at most 0 | `live/rowgen-mismatches.json summary.compared` at 1023, floor 800 | #132 |
| [`unreached-types`](#unreached-types) | 463 | at most 613 | `live/survey-full.json counts.types` at 1699, floor 1600 | #245, #246 |

<a id="mapping-unclassified"></a>
### `mapping-unclassified`

No row in live/mapping.json is a shrug: a via:"none" row with only the generic unexplained note, meaning nobody has said either what CloudFormation type it corresponds to or why it corresponds to none.

Now **13 unclassified rows**, at most **13**. At the bound.

recomputed from 1699 rows and it agrees with counts.unclassified.

- Measured on live/mapping.json counts.unclassified.
- Held against the artifact's own rows, and live/survey-full.json for the roster size. The bound is checked against a count recomputed from the rows rather than against the summary field, so a summary that disagrees with its own body fails instead of passing. The denominator is pinned to the provider survey, which mapping-gen does not write.
- Instrument: the committed mapping artifact read as JSON. Deliberately not a regeneration: tools/mapping-gen's TestMappingJSONMatchesCommittedInputs already ties the artifact to its inputs, and this ratchet stays independent of that test's shape.
- Denominator `live/mapping.json row count`, measured at 1699 against a floor of 1600. The unclassified count is a subset of the rows, so dropping TF types from the mapping roster lowers it without classifying anything. The row count must also equal the provider survey's own type count, which is what makes this floor external to mapping-gen rather than a second number mapping-gen writes.

What the instrument cannot see:

- It counts rows nobody has classified, not rows classified wrongly. A row folded onto the wrong parent reads as classified here.
- The taxonomy's three terminal buckets (tf-only, cfn-unmodeled, deprecated-service) are classifications, so moving a row into one of them lowers this count without teaching anything new about the type.

Where the bound has been:

- 754 via:"none" rows before the first classification pass, 713 after.
- 13 today, with the family sweeps landed and enforceNoBareNone on.

<a id="markerless-veto-admitted-overlap"></a>
### `markerless-veto-admitted-overlap`

No row in internal/live/identity.DefaultTable names a type the derived markerless rule vetoes. A row for a vetoed type is the shipped table contradicting a derived veto, and it subtracts from unreached-types without anything supporting it.

Now **0 types in both the admission table and the markerless veto**, at most **0**. At the bound.

veto reason: the provider mints this type's identity and the type has no tags argument, so every instance would need marker discovery to be found again and there is nowhere to write the marker.

- Measured on internal/live/identity.DefaultTable.
- Held against internal/live/identity.MarkerlessTypes, and through it live/survey-full.json's signals.taggable. The two rosters are different derivations from different evidence even though one -emit run writes both: the table's rows come from the ratified rows plus the import-doc grammar, the veto from the provider survey's own taggability signal. internal/live/stamp's TestPinnedTaggabilityMatchesTheSurvey ties that signal to the run-time marker writer, so the chain ends at the provider schema rather than at another row-gen output.
- Instrument: two in-process Go maps intersected. No artifact, no provider, no network.
- Denominator `internal/live/identity.MarkerlessTypes`, measured at 159 against a floor of 100. The overlap goes to zero two ways: by retracting the offending rows, which is the point, or by emptying the veto roster, which is not. The rule vetoes 150 types on the pinned release and that population is a property of how many provider types have no tags argument, so a collapse to double digits is a rule change and not a provider one.

What the instrument cannot see:

- It cannot see a type the rule should veto but does not - it bounds the contradiction, not the rule's recall.
- A row pasted by hand for a vetoed type is what this catches; tools/row-gen's PROPOSE stage has never been able to offer one, so the generated path is not the risk.

Where the bound has been:

- 77 for as long as the rule was applied only to what may be admitted next, while 77 rows an earlier batch let through stayed in the table.
- 0 on 2026-08-16, once -emit filtered the emitted rows by the same roster. Zero is the ceiling and the floor: a non-zero count means a row reached the table by a route -emit does not filter.

<a id="rowgen-annotation-rulings"></a>
### `rowgen-annotation-rulings`

tools/row-gen/annotations.json is a list of named extractor gaps that only ever shrinks. With unruled mismatches held at zero, nothing else stops the ledger growing, because adding a ruling is always easier than fixing an extractor.

Now **151 rulings**, at most **151**. At the bound.

every ruling names one of the 1023 types the mismatch artifact carries, over 1049 admitted types.

- Measured on tools/row-gen/annotations.json.
- Held against live/rowgen-mismatches.json. Every ruling has to name a type the mismatch artifact compared, and row-gen writes that artifact from the shipped table rather than from the ledger. A ruling for a type nothing compares is a ruling nothing can retire.
- Instrument: the committed ledger read as JSON, cross-checked against the committed mismatch artifact's type list.
- Denominator `live/rowgen-mismatches.json summary.admitted_total`, measured at 1049 against a floor of 850. The cheapest way to delete a ruling is to un-admit the type it names, which moves the type into tools/row-gen/rejected.json and lowers this count while removing support. Pinning the admitted total makes that trade visible.

What the instrument cannot see:

- Size is not quality. A ruling whose Exit names no reachable fix counts the same as one that does; tools/row-gen's TestAnnotationsAgreeWithMismatches is what forbids a stale one.
- Like the mismatch count, this is generator-autonomy debt. It ranks no user-visible work.

Where the bound has been:

- 128 at the ratchet's introduction: 107 genuine mismatches plus 21 types with no proposal to compare.
- 122, 119, 116 through 2026-08-15 and 16 as classifyUnmapped, tryDocumentedShorterForm and the plain-prose enumeration signal each retired a batch of rulings.
- 95 once the ten record-backed effects rows were derived inside -emit instead of carried as unreproduced table rows. That bump also recorded that the constant had already been stale by nine, which is the failure a ratchet is supposed to make visible.
- 93 on 2026-08-16 when this entry was migrated into the harness: the committed ledger was already two below its own const, so for the second time in two days the number was not bounding anything. Nothing was found to have deleted the two; the const was lowered to the measurement rather than the measurement explained.
- 92 the same day, and this one is accounted for. The cloud-singleton admission retired aws_arczonalshift_autoshift_observer_notification_status's ruling, whose own recorded exit condition was "retire when the vocabulary covers an unschemed example that IS a cloud value" - which is exactly the rule that landed. row-gen -convergence demanded the deletion rather than permitting it, and this entry reported the resulting slack within the hour. That is the first time this ledger has fallen for a reason its own annotation predicted.
- 93 on 2026-08-17: the reviewed upward bump this entry's own rule allows for a newly admitted type the classifier cannot reproduce. aws_iam_user_group_membership is the first row whose import ID has a variable number of segments - one per element of a set-typed argument - and every grammar rule in importprecedence.go compares a FIXED segment count against a fixed argument count. The ruling's exit names the missing evidence rather than the missing rule: importdocs-gen scrapes an argument's name and whether it is required, and nothing anywhere in the artifacts says the argument is a collection.
- 96 on 2026-08-17: the same reviewed upward bump, for three types issue #274's markerless-veto two-source exception admits. aws_cognito_risk_configuration, aws_detective_member and aws_lambda_function_event_invoke_config each have a composite CloudFormation primaryIdentifier with no read-only property AND an import-grammar row whose Import section names no server-provided segment - the two independent sources markerless.go now reads agree the identity is argument-built. All three are still classified server-assigned by tryOpaqueOverride: the scrape pinned only the FIRST of several documented import forms on each page, and that one form's example does not split against the registry's composite primaryIdentifier, which is exactly the shape tryOpaqueOverride reads as "the doc shows one opaque value". Each ruling's exit names the same missing capability: keeping every documented import form, not one pinned example, so a composite rule can test the registry's primaryIdentifier against whichever form demonstrates the split.
- 97 on 2026-08-17: the same reviewed upward bump, fixing a wrong-marker defect rather than admitting a new type. aws_lambda_permission's ratified row omitted the qualifier the provider's Import section documents as a second, optional form, so two declarations differing only in qualifier resolved to one identity and collided under the duplicate-identity guard. The corrected row adds a component that is present with its own ':' separator in one documented form and wholly absent (separator included) in the other - identity.Component.OmitIfAbsent, a new field, since the table had no way to express a segment that vanishes together with its own separator. classify.go still pins only the first documented example and has no rule for this shape, so the fresh proposal cannot reproduce the fix. The ruling's exit names the same missing capability the entries above already do: keep every documented import form, not one pinned example.
- 98 on 2026-08-17 (issue #286): the same reviewed upward bump, one more type. aws_lb_target_group_attachment, aws_alb_target_group_attachment and aws_route53_zone_association already carried fold-child rulings, so adding their OmitIfAbsent trailing segments (availability_zone, quic_server_id, vpc_region) moved no count - the rows were already unreproduced for an unrelated reason and remain so. aws_route53_record had none: its three-component row was reproduced exactly until this fix added a fourth, optional set_identifier segment the provider documents as a fourth 'if the record also contains a set identifier, append it' form. Same missing capability as 97: classify.go pins one documented example and has no rule for a trailing segment present in a longer form and wholly absent in a shorter one.
- 122 on 2026-08-18 (issue #245's 'needs hand separator' slice): the same reviewed upward bump, 24 newly admitted types. Every one has a composite CFN registry primaryIdentifier, which routes bucketNeedsHandSeparator and proposes no row regardless of what import-grammar.json knows - the classifier never reaches the composed_of_arguments rule for this bucket at all. live/import-grammar.json's own separator field independently confirms all 24 hand-chosen separator characters, but composed_of_arguments is unset or only partially resolved for every one of them: five are a mixed server-assigned-plus-argument composite (aws_kendra_data_source, aws_kendra_faq, aws_lb_trust_store_revocation, aws_ssm_maintenance_window_target, aws_signer_signing_profile_permission - a segment the scraper's argument-name matcher cannot resolve because it names no real Argument Reference entry, or names an Optional auto-generated one the same way aws_lambda_permission's statement_id already does); ten have a registry that under-counts the doc's real argument count because it omits provider-defaulted arguments from primaryIdentifier (the eight QuickSight aws_account_id-prefixed types, plus the two ServiceCatalog association types); two have a registry field order or field set that plainly disagrees with the doc's own worked example (aws_internet_gateway_attachment's AttachmentType, aws_redshift_endpoint_authorization's reversed order); the rest are plain scrape gaps where composed_of_arguments never resolved despite a matching separator. Each ruling's exit names its own shape rather than a shared catch-all. 98 + 24 = 122.
- 143 on 2026-08-18 (issue #245's 'fold-child' slice): the same reviewed upward bump, 21 newly admitted types (aws_app_cookie_stickiness_policy, aws_shield_protection_health_check_association and 19 others), each a property-child of an already-admitted CFN parent. bucketFoldChild never proposes Components at all - classify.go's own doc comment states the child's composite shape needs a human's separator and shape choice regardless of how clean the import-grammar evidence is - so every one of the 21 is unreproduced by construction, the same standing every other fold-child ruling in this ledger already carries. Two further fold-child candidates in the same unreached population (aws_cloudformation_stack_instances, aws_cloudformation_stack_set_instance) were left unratified because their CFN parent, aws_cloudformation_stack_set, is not itself admitted yet; three more (aws_autoscaling_group_tag, aws_autoscaling_traffic_source_attachment, aws_ssoadmin_customer_managed_policy_attachment) have their identity-bearing argument nested inside a sub-block Component.Attrs cannot read; one (aws_wafv2_web_acl_rule_group_association) has a conditional identity shape branching on which of two mutually exclusive nested blocks is populated; one (aws_lightsail_container_service_deployment_version) has a purely server-assigned, non-configurable differentiator (version); one (aws_ssm_default_patch_baseline) has an ambiguous identity where the doc's own alternate import forms suggest operating_system alone is the true key, not a fold of the parent baseline id; and 14 have no Import section in the provider's docs at all, so no separator has any evidenced source. None of those 22 were added to rejected.json: the parent-pending two are ratifiable once their parent is, and the rest are a generator/resolver capability gap, not a closed question. 122 + 21 = 143.
- 146 on 2026-08-18 (issue #305): the same reviewed upward bump, three newly admitted types - aws_default_network_acl, aws_default_route_table, aws_default_security_group, terraform-aws-vpc's 'adopt the account's default object instead of creating one' idiom, hit by name in four separate real-estate crossings the same night (vpc-complete, rds-complete-postgres, security-group-complete, and reachable through ecs-fargate and autoscaling-complete's own vpc dependency). All three are live/mapping.json via=tf-only rows (no CloudFormation model of an 'adopt an existing default object' resource), so classifyUnmapped always proposes bucketEvidenceOnly for them regardless of their own import-grammar evidence; none of applyImportGrammarPrecedence's upgrade rules run against an evidence-only proposal for a shape this plain (no cloud/account singleton, no confirmed guess). All three are ratified server-assigned, the same shape as their non-default siblings aws_network_acl, aws_route_table and aws_security_group: taggable per live/survey-full.json, and AWS itself mints exactly one default of each per VPC, assigning its own id before the resource block first applies - the required default_network_acl_id/default_route_table_id argument and the optional vpc_id argument each name a parent, not a fresh identity this table derives. Each ruling's exit names the same missing capability: classifyUnmapped has no rule proposing bucketServerAssigned for a cfn-unmodeled type from import-grammar evidence (sole_id_part.source==own-id, or an import_id_example sharing a same-service sibling's id-prefix convention) at all. 143 + 3 = 146.
- 147 on 2026-08-19 (issue #310): the same reviewed upward bump, one newly admitted type. aws_autoscaling_traffic_source_attachment's documented import ID (autoscaling_group_name,traffic_source_type,traffic_source_identifier) is fully client-specified, but its second and third components are the `type` and `identifier` attributes of a required, max_items:1 `traffic_source` nested block, not top-level arguments - the doc's own flattened segment names ('traffic_source_type', 'traffic_source_identifier') are prose shorthand the scrape's argument match cannot resolve, so only the first segment lands in import-grammar.json's arguments list and the fresh proposal stays fold-child with no components. The filed issue's own hypothesis - that the provider identity schema's schema-fallback walk stops at top-level attributes - turned out not to be why: this type carries no identity schema at all in v6.59.0, so identity.Derivable never reaches it regardless of nesting. The real gap was narrower and new: identity.Component gained a Block field so a ratified row can read an identity component out of a named singular nested block, additive over every row before it (no existing row sets it, so no existing resolution changes), and this type's row is ratified using it. The ruling's exit names the same missing generator capability the field's own resolver-side counterpart does not close: resolveArgName matching a flattened prose segment against a nested block's own leaf attribute name, plus a fold-child composite rule proposing a Block-bearing Component. 146 + 1 = 147.
- 151 on 2026-08-20 (issue #326): the same reviewed upward bump, four newly admitted types and the first non-AWS ones this ledger has ever carried. kubernetes_config_map, kubernetes_namespace, kubernetes_storage_class and kubernetes_cluster_role_binding are hand-ratified from the real, current hashicorp/kubernetes provider docs (the offline cache has no Kubernetes provider data), reusing the Block-field mechanism #310 built for aws_autoscaling_traffic_source_attachment to read metadata.name (all four) and metadata.namespace (kubernetes_config_map only) out of each type's required metadata block. This is not the ledger's usual shape: every prior ruling names a type classify.go reaches but disagrees with; these four have no fresh proposal to disagree with at all, because classifyAll only ever iterates live/mapping.json, which is entirely AWS's own CloudFormation-backed evidence and carries zero rows for any kubernetes_* type - a true not_in_mapped_set case, counted by row-gen -convergence's summary.not_in_mapped_set (15 to 19) rather than compared and mismatched. kubernetes_config_map is the type issue #326 named directly: corpus-eks-basic's test_plan stage was blocked because this type had no marker-carrying identity row, so its resources could never resolve an identity for the stamp layer to write to. Each ruling's exit names the same missing generator capability: classify.go has no second evidence source to propose a non-AWS provider type from at all, so the ruling retires only once row-gen gains one (a Kubernetes-provider import-grammar scrape analogous to importdocs-gen's AWS one). 147 + 4 = 151.

<a id="rowgen-unannotated-mismatches"></a>
### `rowgen-unannotated-mismatches`

Every admitted row tools/row-gen's classifier fails to reproduce carries a ruling in tools/row-gen/annotations.json naming what a fuller extraction would have to capture. This counts the ones that do not.

Now **0 unruled mismatches**, at most **0**. At the bound.

recomputed from 1023 compared rows: 147 unmatched, every one of them named by one of the ledger's 151 rulings.

- Measured on live/rowgen-mismatches.json summary.unannotated_mismatches.
- Held against tools/row-gen/annotations.json. The value is recomputed as genuine_mismatches minus annotated and cross-checked against the ledger's own size, so the artifact's summary field cannot be the only witness to its own claim. row-gen writes the artifact; the ledger is hand-authored and reviewed.
- Instrument: the committed mismatch artifact plus the committed ledger, both read as JSON. Not a regeneration - tools/row-gen's TestMismatchLedgerMatchesCommitted is the drift half.
- Denominator `live/rowgen-mismatches.json summary.compared`, measured at 1023 against a floor of 800. A mismatch count falls when the compared set shrinks. The compared set is the admitted types the mapping reaches, so a loadMapping filter or an un-admission lowers this count without any extractor improving.

What the instrument cannot see:

- This is generator-autonomy debt and not user-visible coverage. tools/row-gen/emit.go:41 copies every field of a ratified row verbatim, so a mismatch changes nothing a user experiences. Issue #695 deleted the adopted-unchanged ratio this artifact's predecessor led with, for exactly that reason: it was read as coverage three sessions running.
- It compares only the mapped set. The admitted types with no fresh proposal at all - admitted_total minus compared - are outside this number; the -emit gate holds them to the same bar separately.

Where the bound has been:

- 241 after the ratify-remainder batch; 215 once importdocs-widen's parse and the import-precedence rules landed; 194 once the fold-row guard came out.
- 114 through issue #132's seven extractor commits, then 0 once every remaining mismatch was ruled and -emit began refusing an unruled one. It stays 0: a new unannotated mismatch is either a regression or an unruled admission.

<a id="unreached-types"></a>
### `unreached-types`

Every type the pinned provider serves is in one of three rosters - admitted by internal/live/identity.DefaultTable, vetoed by hand in tools/row-gen/rejected.json, or vetoed by the derived markerless rule. This counts the ones in none of them, where naming the type in a configuration is a hard resolve error with no ledger entry saying why.

Now **463 provider resource types**, at most **613**, so the bound is stale by 150 and should be lowered to the measurement.

1049 admitted, 101 hand-vetoed, 159 markerless-vetoed, over a roster of 1699.

- Measured on internal/live/identity.DefaultTable, tools/row-gen/rejected.json and internal/live/identity.MarkerlessTypes.
- Held against live/survey-full.json. tools/survey-gen writes it from the provider's own GetProviderSchema response, and none of the three rosters under test contributes a type to it. No edit to the admission table or either veto ledger can make this measurement agree with itself.
- Instrument: the three rosters read in process (two Go maps and one committed JSON file) against the committed provider survey. No provider plugin, no network.
- Denominator `live/survey-full.json counts.types`, measured at 1699 against a floor of 1600. This count is a difference against the roster, so deleting rows from live/survey-full.json lowers it exactly as effectively as admitting a type does, and is the cheaper edit. hashicorp/aws has never lost a hundred resource types in a release.

What the instrument cannot see:

- It counts hard resolve errors only. internal/live/lint's schema fallback (identity.SynthesizeTypeIdentity) admits some of this population at run time when a real provider plugin is present - 60 of them when the count stood at 669 - and that rescue needs a plugin, so a ratchet that subtracted it could not run in the fast tier.
- It describes one provider. A type from any other provider is outside both the roster and this number.
- It says nothing about whether an admitted row is correct, only that the type was reached.

Where the bound has been:

- 669 while the hand ledger stood at 949/81 and again at 944/86 - the batch that moved five types from the ledger into the table did not change this count at all, which is why it exists rather than a count of the ledger.
- 665 when the markerless rule landed (#249); 649 while that rule read only the CloudFormation registry's verdict.
- 621 once tools/importdocs-gen's soleid scrape settled 28 untaggable types the registry models nothing for.
- 615 on 2026-08-17, three of them from single ratifications rather than a batch. The last is aws_s3control_storage_lens_configuration, and it is the first row admitted with no annotation over a documented account-id slot: a composite proposal now reads the cloud_default the argument's own bullet states and renders the segment as a Cloud component, so the classifier reproduces the ratified row instead of needing a ruling for it.
- 613 on 2026-08-17: aws_iam_account_alias and aws_s3_account_public_access_block, two client-named single-argument rows the import grammar already pinned. The account public access block's account_id component carries the same documented cloud-id default as aws_s3control_storage_lens_configuration above; renderClientNamedEntry gained the fallback the composite path already had (#241) so the pasted row spells it rather than an empty identity.
<!-- harness-gen:end burndown -->

## Assumptions

Load-bearing claims the project relies on while it measures anything. The
field that matters is the consequence: "this is true" is worth much less
than "if this stops being true, here is what becomes wrong". Every claim
below has a check behind it in `internal/live/harness`, and the registry
refuses an entry that has a claim but no check, or a claim but no
consequence.

<!-- harness-gen:begin assumptions -->
<a id="checked-layers-are-lint-identity-dataread-stamp"></a>
### `checked-layers-are-lint-identity-dataread-stamp`

Everything an offline report says is derived from four fully checked analysis passes - lint, identity, dataread and stamp - plus projection, which is checked only where it needs no cloud, and discovery, which is not checked at all. All three lists are named rather than omitted.

**If this stops being true.** A clean verdict is a narrow claim, and how narrow is exactly this list. A pass added to internal/live and joined to neither list makes every clean count overstate, silently, in the direction that looks like progress. This is the shape that has appeared three times (#156, #164, #171): a check whose unit does not match the unit of the thing it guards.

- `checked: lint, identity, dataread, stamp`
- `partial: projection (2 of 27 refusals)`
- `unchecked: discovery`

Evidence: internal/live/check/catalog.go's CheckedLayers, PartiallyCheckedLayers and UncheckedLayers, cross-checked against all three of the committed corpus artifact's own header lists, share included. internal/live/check's TestLayersClassifyEveryLivePackage is what forbids a new package joining no list; this holds the three lists themselves to their recorded contents. Projection moved from unchecked to partial when #224's two exported provider-free entry points finally got a caller. Discovery stays wholly unchecked, and #261's plan to move it was measured and refused: of the four refusals its provider-free declared scan can raise, two are caller-bug guards check.Analyze cannot trip, one ("One marker value for two declared addresses") needs two declared addresses escaping to one marker value, which markerkey's excluded runes and #178's reversible key escaping make unreachable for anything identity resolves, and the fourth measures the same quantity as lint.RuleOverlongAddress, an already fully checked layer - see internal/live/check's TestLintCoversTheDeclaredScan.

Tracker: #102

<a id="corpus-artifact-currency"></a>
### `corpus-artifact-currency`

live/corpus-refusals.json is dated against the newest commit touching internal/live, and the gap between them - zero or not - is reported rather than left for a reader to re-derive.

**If this stops being true.** Every quoted corpus figure is read as describing HEAD. When the artifact instead describes a tree several behaviour-changing commits old, a before/after comparison, a ranking, or a closed-issue figure can be wrong by exactly the size of what those commits changed, with nothing at the point of reading saying so.

- `live/corpus-refusals.json`
- `internal/live`

Evidence: git log over live/corpus-refusals.json's own path versus internal/live's newest touching commit. This is the instrument the original scouting pass (issue #256 item 7) proposed and used by hand once; this makes it something every reader gets without re-deriving it.

Tracker: #256

<a id="credential-exclusions-are-sanctioned"></a>
### `credential-exclusions-are-sanctioned`

Exactly 3 provider types are excluded from admission on credential-material grounds with no route to admission at all, they are all in the hand veto ledger, and none of them is admitted.

**If this stops being true.** Type-for-type coverage is the bar, and this credential exclusion is its one remaining sanctioned hole. It has moved twice: down from four after ruling 5 (2026-08-23) moved aws_iam_access_key and aws_iot_certificate onto strict { secrets } instead, where they are admitted by default; back up to three after issue #431's provider-wide sweep (tools/credential-sweep) found aws_wafv2_api_key already refused unconditionally by internal/live/identity.LocatedType's own sensitiveIdentityAttr check, with no ledger entry naming it. Either direction of drift is the same failure this assumption exists to catch: an exclusion nobody decided (this ID's own history before #431 - the sanctioned list undercounted a refusal the code already performed), or an exclusion that shrinks the coverage denominator without anybody deciding to (a veto with no route at all, added without joining this list). This has already drifted in that second direction once before: aws_secretsmanager_secret_version sat on tools/survey-gen's ops-excluded list reading "credential" until the 2026-08-16 ruling that the marker goes into a tag and never into the secret.

- `aws_appstream_directory_config`
- `aws_ivs_playback_key_pair`
- `aws_wafv2_api_key`

Evidence: CLAUDE.md's sanctioned list, checked against tools/row-gen/rejected.json's own reason text and against internal/live/identity.DefaultTable. See credentialReason for what the text half of this cannot see.

Tracker: the type-coverage ruling; no issue - the list is a standing exclusion, not work.

<a id="measurement-artifacts-are-commit-dated"></a>
### `measurement-artifacts-are-commit-dated`

Every committed measurement artifact whose numbers get quoted is tracked and reachable from HEAD, so any figure taken from one can be dated to a commit.

**If this stops being true.** A number that cannot be dated outlives the tree it describes. A site total measured on a branch was propagated into three committed files and was wrong by exactly the size of a class a later merge had emptied; the one copy that survived contact was the one that named its commit. An artifact regenerated and quoted but not committed is the same failure with nothing at all to point at.

- `live/corpus-refusals.json`
- `live/rowgen-mismatches.json`
- `live/mapping.json`
- `live/survey-full.json`
- `live/cohort-acceptance.json`
- `live/identity-sources.json`

Evidence: git ls-tree over HEAD. None of these artifacts carries a commit field of its own, so the tree is the only date available - which is itself worth knowing, and is why the quoting rule is to name the commit rather than the file.

Tracker: no issue; this is the measuring-choudoufu skill's first instruction made executable.

<a id="onboarding-non-blocking-ids"></a>
### `onboarding-non-blocking-ids`

check.ClassifyOnboarding treats exactly four refusal IDs as something other than language-blocked - the state backend, an unadmitted type, a logical resource and an eligible pre-plan data read - and every other refusal the live path can produce puts an estate on the language-blocked rung.

**If this stops being true.** Every ranking of which configurations are close to working under live markers is computed from this classification. A fifth non-blocking ID moves estates up the ladder with no configuration becoming any more applyable, which is exactly what the markerless retraction had to avoid; a missing one buries work that is nearly done. Three agents re-implemented this classifier in Python on separate days to check it, which is the cost of it living in a switch statement nothing asserts.

- `Resolves at plan time via a data-source read lands on data-read-eligible`
- `logical-resource lands on admissions-only`
- `state-backend lands on backend-only`
- `unadmitted-type lands on admissions-only`

Evidence: internal/live/check/ladder.go's own switch, driven rather than read.

Tracker: #179 for the data-read rung; #102 for the ladder.
<!-- harness-gen:end assumptions -->

## Adding to either registry

Add the entry to `Burndown()` or `Assumptions()` in
`internal/live/harness`, keeping the slice ordered by ID, then run
`just harness`. The shape checks will tell you what is missing; they are
deliberately blunt about it, because an entry that looks complete and proves
nothing is the thing this exists to replace.

Moving a bound down is routine and needs no ceremony beyond re-rendering.
Moving one up is admitting something got worse, and belongs in the entry's
own history with the reason.
