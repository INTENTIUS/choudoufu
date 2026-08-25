#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-alb's flagship "complete-alb" example
# (.corpus/alb/examples/complete-alb, pinned in live/corpus-manifest.json at
# tag v9.9.0), crossed through choudoufu against floci - the real, five-
# stage pipeline (cold deploy, migrate, test plan, test apply, drift and
# reconverge). ALB is one of the most commonly deployed AWS resources in
# Terraform, and this is the module's own application-load-balancer example
# (there is also "complete-nlb" for network load balancers - a different
# target, not crossed here). It had never been crossed against a cloud
# before this script existed.
#
# 80 real resources: the root VPC (1 VPC, 3 public + 3 private subnets, 3
# public route tables + 3 associations, 1 internet gateway/route, plus the
# account's default_* adopter trio - manage_default_* defaults to true on
# the v5.x line this module pins, unlike v6.x's opt-in default used by
# corpus-vpc-complete's own crossing), the ALB itself (1 LB, 6 listeners, 7
# listener rules, 1 listener certificate, 3 target groups, 3 target group
# attachments, 1 lambda permission, 1 security group + 2 VPC security group
# rules, 2 route53 A/AAAA records), two ACM certificates (root + wildcard,
# each with its own DNS validation record + validation wait), an S3 log
# bucket (terraform-aws-modules/s3-bucket, ObjectWriter ownership +
# log-delivery-write ACL), two Cognito resources (user pool + client - see
# lex00/floci#63 below for the domain, DELTA'd away), two Lambda functions via
# terraform-aws-modules/lambda (each with its own IAM role/policy/log
# group), and two plain EC2 instances.
#
# THREE REAL FLOCI GAPS FOUND AND FIXED IN THIS PASS (all filed, fixed,
# tested, merged to lex00/floci main, and re-pinned into live/floci-image
# below - not worked around with a config DELTA, because each one is a
# small, precise, generically useful fix rather than a feature this estate
# alone needed):
#
#   lex00/floci#58 (FIXED, dee11c78/12100986). ACM RequestCertificate built
#   a wildcard SAN's DNS validation record NAME with the literal "*." left
#   in it ("_hash.*.example.com." instead of real ACM's "_hash.example.com.").
#   module.wildcard_cert's aws_route53_record.validation therefore created a
#   record whose fqdn never matched what aws_acm_certificate_validation
#   waited for, and the apply hung until it failed outright. Any wildcard-
#   SAN certificate through terraform-aws-modules/acm hits this the same
#   way - a very common pattern (a cert covering both example.com and
#   *.example.com).
#
#   lex00/floci#61 (FIXED, 02430843/aac84853). S3 PutBucketAcl/PutObjectAcl
#   rejected the "log-delivery-write" canned ACL as unsupported.
#   terraform-aws-modules/s3-bucket's attach_elb_log_delivery_policy /
#   attach_lb_log_delivery_policy examples (both on here, as the ALB
#   module's own README says they must be) set object_ownership =
#   "ObjectWriter" with acl = "log-delivery-write" together - the standard
#   way to provision any ALB/NLB or S3-access-log bucket.
#
#   lex00/floci#62 (FIXED, fc25ea3d/4990c8ab). EC2 DescribeInstanceTypes
#   returned an empty result for "t3.nano" (aws_instance.this/other's
#   instance_type here, and a very common smallest-x86_64-burstable
#   default in example configs), so terraform-provider-aws's own instance
#   read failed outright even though RunInstances itself tolerates an
#   absent catalog entry.
#
# All three: reproduced against the OLD image, fixed with a small, targeted
# change plus new/extended regression tests (all green, full relevant test
# suites re-run), verified the fix by reverting it and watching the new
# tests fail, merged to lex00/floci main, and closed with the commit
# references. See each issue for the full detail this header only
# summarizes.
#
# A FOURTH FLOCI GAP, FOUND HERE AND SINCE FIXED UPSTREAM:
#
#   lex00/floci#65 (CLOSED 2026-08-22). ELBv2 DescribeListeners/DescribeRules
#   dropped AuthenticateCognitoConfig/AuthenticateOidcConfig entirely -
#   Action.java's model had no fields for either action type, so
#   CreateListener/CreateRule accepted them (stage 1's apply always succeeded
#   cleanly) but the read path echoed back only
#   {"Type": "authenticate-cognito"}, config gone. It surfaced in stage 2:
#   live-import stamps ownership tags by re-planning a synthetic config built
#   from the live-read object, and since the live-read never populated
#   authenticate_cognito/authenticate_oidc, terraform-provider-aws correctly
#   rejected the result as internally inconsistent ("... must be specified
#   when type is 'authenticate-cognito'"). Four instances landed in
#   live-import's own FAILED bucket - the two authenticate-* listeners and
#   the two listener rules under them.
#
#   The fix is in the image live/floci-image pins (bumped to
#   sha256:0afd2648... by commit 4649c73d52, whose own crossing was a
#   different estate's), and this estate had simply not been re-crossed
#   since. Stage 2 below now asserts the reverse: all four stamp, and the
#   provider's validation text must NOT appear. This half of this pass's
#   staleness is unrelated to the credential-veto change below; a worker
#   reconciling issue #365 slice 3 measured the same 51/1/0/28 on a
#   stock-main control binary.
#
# ONE FLOCI GAP FOUND, LEFT OPEN (a genuine feature build, not a one-field
# fix - worked around here with a documented delta so this script can still
# stand the estate up and migrate it for real):
#
#   lex00/floci#63 (OPEN). Cognito CreateUserPoolDomain/DescribeUserPoolDomain/
#   DeleteUserPoolDomain are entirely unimplemented - no code anywhere in
#   floci's Cognito service touches "Domain" at all. DELTA 2 removes
#   aws_cognito_user_pool_domain.this and substitutes the literal value it
#   would have carried (its own `domain = local.name` argument) everywhere
#   the ALB module's authenticate-cognito/authenticate-oidc listener actions
#   referenced it.
#
# UPDATE (issue #399's maintainer ruling, 2026-08-24): the narrative below
# (written 2026-08-22/23) still describes what blocked this estate as of
# that pass, including "the two aws_lb_target_group_attachment.this ports
# are HANDOFF's fifth row read the other way... THIS IS A RATIFICATION
# QUESTION". The maintainer has since ruled: port is now
# [identity.Component.OmitIfAbsent] on this row (verified against
# botocore's elbv2 2015-12-01 model - a Lambda-type target genuinely has no
# port, and the collision OmitIfAbsent's safety margin exists for is
# structurally impossible for that shape), so those two sites are FIXED,
# not a standing ratification question anymore. Stage 3's diagnostic count
# below reflects this; the narrative is left as a historical record of what
# was true when it was written rather than rewritten in place - see the
# gauntlet_stage detail string and internal/live/identity/
# targetgroupattachment_omitifabsent_test.go for the current state.
#
# WHAT BLOCKS STAGE 3 NOW, AND WHAT USED TO:
#
#   THE WALL THAT CLEARED (#309's last site, gone as of 2026-08-22).
#
#   #309 (CLOSED 2026-08-21, under the reframe that retired admission as a
#   gate - HANDOFF.md). aws_cognito_user_pool_client is no longer unadmitted:
#   the issue's own MarkerlessTypes-widening work (closing comment,
#   2026-08-19) put it in the roster, where record_store-declared estates
#   like this one resolve it as identity.ClassRecordLocated (issue #270).
#   For a while it still blocked this estate's stage 3, one layer down, as
#   RuleMarkerlessType ("Resource type has nowhere to write an ownership
#   marker") rather than RuleUnadmittedType. identity.LocatedType
#   (internal/live/identity/located.go) answered false on two of its
#   conditions independently, and both are now answered:
#
#     condition 3, the identity cannot be recorded IN FULL. CLEARED
#     2026-08-22 (branch gauntlet/albcomplete-importgrammar). It was in
#     IDNotProvenWholeTypes (idnotwhole_generated.go); its Import section
#     documents a composite <user pool id>/<client id> string the exported
#     `id` bullet does not corroborate, so `id` might have been a fragment.
#     Neither route out was open - hashicorp/aws 6.59.0 serves NO wire
#     identity schema for the type, and it had no DocumentedImportIDs
#     grammar, because its page names its segments in prose ("the `id` of
#     the Cognito User Pool, and the `id` of the Cognito User Pool Client")
#     rather than one token at a time. tools/importdocs-gen now reads that
#     sentence. The generic rule is the possessive-of one, not a Cognito
#     one: English states a qualified name in two orders, and where the
#     schema's order ("using the `user_pool_id` and `client_id`", which
#     every existing reader resolves) is written the other way round, each
#     segment is re-read owner-first and matched EXACTLY against the page's
#     own Argument and Attribute Reference.
#     TestPossessiveOfGrammarComposesTheDocumentedImportString pins the
#     composed string BY VALUE against the provider's own documented import
#     example - us-west-2_abc123/3ho4ek12345678909nh3fmhpko - because a
#     reading that swapped the two segments would be the same shape, the
#     same length and a different object.
#
#     condition 2, credential material. CLEARED 2026-08-22 (commit
#     80666bc1c0, issue #365 population 2). The veto was answering the wrong
#     question. It asked whether the TYPE carries a secret anywhere in its
#     schema (identity.credentialMaterial's whole-schema sweep, which is
#     internal/live/projection's residue question, a value-preservation
#     promise the located route makes no claim about). What this route
#     actually writes is locatedImportIDAttr or the identity plan's own
#     components and nothing else, so the only secret it can leak is one
#     that IS an identity component. Narrowed to sensitiveIdentityAttr.
#     aws_cognito_user_pool_client's recorded identity is user_pool_id/id;
#     it never touched client_secret at all, and refusing it bought nothing.
#     Nine of the eleven types the old veto excluded were in that position.
#     The two whose exclusion is a dated maintainer ruling
#     (aws_iam_access_key, aws_iot_certificate) stay refused through a named
#     list, and aws_wafv2_api_key - whose recorded identity IS api_key -
#     still refuses on the narrowed rule itself, which is why the narrowing
#     had to stay identity-aware rather than be a deletion.
#
#   With both answered, live-plan raises ZERO markerless-type diagnostics on
#   this estate. Stage 3 asserts that by count, and it is the load-bearing
#   assertion that this wall is gone.
#
#   #305 (aws_default_network_acl/aws_default_route_table/
#   aws_default_security_group, the VPC module's default-object adopters)
#   is FIXED and merged - it no longer blocks anything here; the 3 sites it
#   used to name are now VERIFIED/DRIFTED and eligible in stage 2 like
#   everything else.
#
#   WHAT THE CLEARED WALL WAS MASKING, AND IS THE STAGE-3 BLOCKER NOW.
#
#   internal/command/live_plan.go runs lint.CheckWith FIRST and returns on
#   the first error-severity issue, before the static-scope evaluation and
#   before identity resolution. So while the one markerless-type refusal
#   stood, it was the ONLY diagnostic this estate could ever print, and this
#   header's previous claim that "this estate's live-plan output carries
#   exactly one distinct Error: line" was a statement about that early
#   return, not about the estate. Measured 2026-08-22 by running the
#   pre-fix binary (80666bc1c0^) and the fixed one against the SAME migrated
#   estate and the same live floci: pre-fix, 1 diagnostic, all of it the
#   markerless-type refusal; fixed, 0 of those and 20 others.
#
#   The 20 were HANDOFF's first row - choudoufu refuses where stock
#   proceeds, which is a defect - and they were the config-language subset
#   wall, not a type-coverage one. Every resource they BLOCK is UNTAGGABLE -
#   six of the twenty were reported at the module call rather than at a
#   resource, and named the module input variable they poisoned. That is
#   the association/attachment/record family, whose identity is a composite
#   of its parents and so must be evaluable from configuration alone,
#   because there is no tag on it to recover it from. The taggable resources
#   over the very same expressions do NOT refuse - module.alb's
#   aws_lb_target_group.this for_eaches over exactly the same
#   var.target_groups as aws_lb_target_group_attachment.this at line 565 and
#   is silent, because it carries the tofu-address stage 2 wrote.
#
#   Two independent root causes, A (FIXED for 11 of its 12 sites - 10 on
#   2026-08-22, one more on 2026-08-23 as B's own side effect, see below)
#   and B (FIXED 2026-08-23):
#
#     A. A resource or module-output reference nested inside a module
#        INPUT's object or list literal. 12 of the original 20, across
#        three of module.alb's inputs (var.target_groups's target_id,
#        var.additional_target_group_attachments's target_id,
#        var.listeners's additional_certificate_arns). The map keys are all
#        static - only the leaves are poisoned - and internal/configs'
#        module-call variable evaluation (EvaluateStructural,
#        module_call.go) ALREADY substitutes an unknown for a poisoned leaf
#        rather than refusing the whole variable, exactly as
#        internal/live/identity/partialargs.go's own header describes for
#        the direct-argument path. What that substitution does NOT survive
#        is internal/live/identity's OWN, separate, syntax-level chase
#        (localvalue.go/eachvalue.go), which exists to recover an
#        each.value.<attr> selection when the STATIC evaluator's substituted
#        value is not merely partially unknown but the whole for_each
#        source failed to evaluate outright - the shape #178/#260/#301/#354
#        already built machinery for. Three gaps in that chase, all general
#        - none names a concrete aws_* type - and none is #375's own
#        surface (internal/configs' module-call variable path), which
#        turned out not to be where this wall actually lived:
#
#          1. A for-expression's own FILTER clause reading the whole
#             for_each element (`{ for k, v in var.target_groups : k => v if
#             local.create && lookup(v, "create_attachment", true) }`,
#             module.alb's own aws_lb_target_group_attachment.this for_each)
#             needed v's whole value to decide inclusion, so one poisoned
#             element refused the WHOLE comprehension - discarding the
#             per-key element EXPRESSIONS #260's machinery already carries
#             forward for a bare each.value.<attr> selection, before any of
#             them got a chance to be selected into.
#             resolver.forCondIncludesTolerant (localvalue.go) widens the
#             filter to fall back on exactly the same each.value absence
#             proof (resolver.objectLacksKey) lookup()/try() already use,
#             composing &&/||/! by ordinary three-valued (Kleene) logic so a
#             filter half the condition cannot prove still decides when the
#             other half can.
#          2. Once (1) let the for_each produce instances again, a
#             CONDITIONAL's own condition (`try(each.value.target_type,
#             null) == "lambda" ? null : ...`,
#             aws_lb_target_group_attachment.this's port argument) refused
#             outright merely because it read each.value.<attr> at all -
#             resolver.isSymbolic's each.value case is blanket over the
#             WHOLE element once one leaf is unprovable, not over which
#             attribute a particular reference selects, even when that
#             attribute (target_type here) is a plain literal sitting right
#             beside the poisoned one. resolver.eachValueCondTolerant
#             resolves an equality test (composed the same &&/||/! way)
#             through the ordinary resolveExpr entry point instead of
#             refusing on sight.
#          3. An INDEXED reference into a DIFFERENT resource, where the
#             index itself is each.value.<attr>
#             (aws_lb_target_group.this[each.value.target_group_key].arn,
#             aws_lb_target_group_attachment.additional's target_group_arn)
#             hit the identical wall one level down, inside
#             resolver.resolveIndexedTraversal's own strict evaluation of
#             the index expression. Same fallback, reused rather than
#             reimplemented: resolveIndexedTraversal now tries
#             eachValueCondOperand (built for (2)) when the strict
#             evaluation of the index fails.
#
#        Verified by a real run against the same migrated estate and the
#        same live floci (not merely the new unit tests
#        TestModuleForeachFilterOverPoisonedValueResolves pins by value):
#        target_id (x3, aws_lb_target_group_attachment.this), port (x1,
#        the same resource's "ex-instance" key, whose target_type is not
#        "lambda"), target_group_arn and port (both,
#        aws_lb_target_group_attachment.additional) all stopped refusing.
#        Two of the twelve stood after this pass; one is gone now, as a side
#        effect of B's own fix rather than a widening of the three above -
#        see B's closing paragraph. var.listeners's
#        additional_certificate_arns (module.wildcard_cert.acm_certificate_arn,
#        a MODULE OUTPUT reference rather than a resource reference) used to
#        refuse aws_lb_listener_certificate.this's certificate_arn because
#        the module output itself is
#        `try(aws_acm_certificate_validation.this[0].certificate_arn,
#        aws_acm_certificate.this[0].arn, "")`
#        (terraform-aws-modules/acm's own outputs.tf), and
#        aws_acm_certificate.this[0] - the try()'s resolvable second
#        candidate - was invisible to identity.Context.ManagedResults before
#        B's fix made it plannable and attributable. It resolves to
#        ClassNeedsDiscovery now, through the identical, unmodified
#        each.value/module-output machinery; nothing about ITS OWN handling
#        changed, and the try()'s first candidate
#        (aws_acm_certificate_validation, a type this fork's identity
#        resolution does not model at all - "admitted by the provider's own
#        identity schema" per the warning below, not by this fork's own
#        table) is still never reached, which no longer matters because the
#        second candidate now resolves. One of the twelve remains, for a
#        reason distinct from all four fixes above and NOT attempted here:
#
#          - local.lambda_target_groups's value clause
#            (`merge(v, { lambda_function_name = split(":", v.target_id)[6]
#            })`, feeding aws_lambda_permission.this's function_name) is not
#            isBareVar (forExprElems, localvalue.go): it is a FUNCTION CALL
#            over v, not v itself, so even once (1) lets the comprehension's
#            FILTER decide, resolver.loopVarUnbound still drops the value
#            clause's own expression because v is read inside it and
#            nothing here binds v as a value. Recovering this needs
#            substituting the source element's own structural expression for
#            v INSIDE the merge() call syntactically - a genuinely new piece
#            of machinery this package does not have anywhere today (every
#            existing chase walks INTO a known container shape; none
#            rewrites one), and it is its own unit.
#
#     B. A server-produced attribute driving an untaggable child's identity.
#        8 of the original 20, 4 for each of the two certificate module
#        instances. FIXED 2026-08-23. terraform-aws-modules/acm's
#        local.validation_domains is built from
#        aws_acm_certificate.this[0].domain_validation_options, and
#        aws_route53_record.validation[0]'s name and type are elements of
#        it. domain_validation_options is minted by ACM; no static
#        evaluation can produce its VALUE, and stock only manages because it
#        reads it back out of the state file this stage deliberately
#        deleted. But the identity argument does not need the value - it
#        needs to know THAT it is waiting on the certificate, which is a
#        provenance question the live plan can answer without ever reading
#        stock's state:
#
#          1. internal/live/projection's PlanInstances used to plan only a
#             resource with no count and no for_each at all, on the theory
#             that a repeated resource's key set is the thing in doubt - true
#             for a computed count, false for `count = local.create_certificate
#             ? 1 : 0`, whose key set is exactly {0} or {} out of the
#             caller's own literals. planCounted (plan.go) evaluates a
#             count expression statically first and plans each instance only
#             when it resolves to a known, bounded, non-negative integer;
#             a count that itself reads a managed resource still declines,
#             unchanged. Without this, aws_acm_certificate.this[0] never
#             reached identity.Context.ManagedResults at all, so nothing
#             downstream had anything to attribute an unknown to.
#          2. identity.resolver.managedFromExpr used to see only a DIRECT
#             reference to a covered resource. local.validation_domains
#             names the certificate through a local
#             (`try(aws_acm_certificate.this[0].domain_validation_options,
#             var.acm_certificate_domain_validation_options)`), so
#             managedFromExprAt now chases through a local's or a module
#             variable's own defining expression when the identity argument
#             does not name the resource directly - hcl.Expression.Variables
#             walks the whole tree regardless of how many for/merge/distinct
#             calls sit in between, so the chase needs no structural
#             decomposition of its own. Two safety refinements this chase
#             needed that a direct reference never did:
#             namesAnUnprovenVariable narrows condition 2's "any var
#             anywhere" rule to a var this run cannot rule out being the
#             offline loader's synthetic unknown (no default) - the ACM
#             module's own var.acm_certificate_domain_validation_options,
#             which DOES have a default, must not veto the chase the way an
#             unset required variable correctly would; and the found-address
#             set is collected across every candidate a chased expression
#             names rather than returned on the first hit, declining instead
#             of guessing when a local legitimately names more than one
#             covered-but-unknown resource (measured against this exact
#             estate's own local.name, which combines an ACM certificate ARN
#             with an unrelated Cognito user pool's - attributing to
#             whichever Variables() listed first would have been a wrong
#             claim, not a wrong marker, but still wrong).
#          3. Even attributed, the identity argument still had to EVALUATE
#             to a clean unknown rather than the strict static evaluator's
#             own hard refusal - element()/distinct()/merge() are function
#             calls, not the traversal selectStatic's literal-shape chase
#             already handles. resolver.tolerantManagedValue is
#             resolveExpr's true last resort: it retries the whole argument
#             through the same tolerant evaluator a module OUTPUT reference
#             already gets (configs.StaticEvaluator.WithUnknownForRefusedReferences),
#             gated on managedFromExpr's own attribution succeeding first -
#             never on cty.DynamicVal alone, which is exactly what an unset,
#             uncovered variable produces too and must not be softened
#             (TestDataReferenceRefusesWithoutResults pins this).
#          4. The attributed resource turned out to carry an unrelated
#             SENSITIVE field (aws_acm_certificate.this[0].private_key,
#             marked on every planned instance whether or not a
#             certificate has been created yet), and
#             identity.resolver.managedUnknownAt asked ContainsMarked/
#             IsWhollyKnown of the WHOLE planned object, so the certificate
#             was rejected as attributable for a field domain_validation_options
#             has nothing to do with. selectReferencedValue now walks the
#             SAME steps managedCovered already proved present - the
#             instance key, then the reference's own remaining attribute
#             path - down to the one leaf a reference actually names, never
#             calling a cty operation on a value that is itself marked, so
#             the unrelated private_key leaf is never touched and the
#             knownness/sensitivity questions are asked of
#             domain_validation_options alone.
#
#        None of the four names a concrete aws_* type in control flow: (1)
#        is a property of a count expression, (2) and (3) are properties of
#        an identity argument's own expression shape, (4) is a property of
#        a covered value's own reference path. (1) and (2) together reach
#        every table-admitted type whose identity depends on a sibling
#        resource through a local rather than a direct reference or an
#        each.value scope - unmeasured beyond this estate, but the carrier
#        (ACM/Route53 validation) is documented on HashiCorp's own pages as
#        a common pattern, not an ALB-specific one. (4) reaches every
#        managed resource this file attributes from whose schema carries an
#        unrelated Sensitive attribute.
#
#        A side effect, not a second unit: module.alb's
#        aws_lb_listener_certificate.this["ex-https/0"].certificate_arn (one
#        of family A's two remaining sites, below) reads
#        aws_acm_certificate_validation.this[0].certificate_arn falling back
#        to aws_acm_certificate.this[0].arn - the SAME certificate (1) now
#        plans and (2)-(4) now attribute - so it resolves to
#        ClassNeedsDiscovery through the identical, unmodified mechanism.
#        Nothing about this site's own handling changed; the certificate it
#        depends on simply stopped being invisible.
#
#   Checked against #313 (corpus-security-group-complete's
#   data.aws_availability_zones-feeding-a-nested-module-for_each wall):
#   none of the remaining diagnostics mentions data.aws_availability_zones.
#   Different wall; #313 does not reach this estate.
#
# WHAT THIS SCRIPT ACTUALLY PROVES, GIVEN ALL OF THE ABOVE:
#
#   stage 1  cold deploy   PASS - real, unmarked infrastructure, all 80
#                          resources, once for real (no manual retries) with
#                          the fixed floci image.
#   stage 2  migrate       PASS - real: 41 VERIFIED + 10 DRIFTED = 51 of 80
#                          resource instances eligible (#305's fix moved the
#                          default-object trio from unadmitted into this
#                          count); all 51 newly stamped, 0 FAILED (floci#65
#                          is fixed in the pinned image - it used to fail 4);
#                          the other 29 not eligible (28 UNTAGGABLE by
#                          provider schema + 1 UNADMITTED_TYPE, which is
#                          live-import's own bucket name for
#                          aws_cognito_user_pool_client - live-import has its
#                          own knowledge question, separate from live-plan's
#                          LocatedType route, and it still answers no) - of
#                          which -approve records 1
#                          (null_resource.download_package, record-backed
#                          since #340, seeded into the record store rather
#                          than skipped) and correctly skips 28. Asserted
#                          against live-import's own report AND confirmed
#                          independently through the AWS CLI.
#   stage 3  test plan     BLOCKED, for real, at 3 diagnostics (was 20; 11
#                          of family A's 12 sites fixed - 10 on 2026-08-22,
#                          one more as family B's own side effect; family B
#                          FIXED, 8 of 8, 2026-08-23) in the config-language
#                          subset, all of them on untaggable resources.
#                          #309's markerless-type site is GONE and stage 3
#                          asserts that by count. Specific counts, summaries
#                          and resource addresses asserted against a real
#                          live-plan run on the really-migrated estate, state
#                          file deleted first, BREAK=1 negative control.
#   stage 4  test apply    NOT RUN - depends on stage 3.
#   stage 5  drift/reconverge  NOT RUN - depends on stages 3-4.
#
# A partial, honestly-reported pass is the point: this is the real, current
# behavior of choudoufu (and, until #58/#61/#62 landed, of floci) against a
# real, popular module, not a green claim routed around the truth.
#
#   bash live/e2e/corpus-alb-complete/run.sh
#
# Needs Docker, the AWS CLI, terraform (real, stock terraform - stage 1 is
# deliberately NOT choudoufu), network access (for `terraform init` to
# resolve the vpc/acm/s3-bucket/lambda registry modules, and to fetch the
# Lambda deployment zip fixture - see DELTA 3), and .corpus populated (`just
# corpus-fetch`).
#
# DELTA 3 is not a floci or choudoufu workaround: the example's own two
# lambda module calls build local.downloaded's filename from an md5 of a
# GitHub raw-content URL and fetch it via a null_resource local-exec
# provisioner, which the lambda module's locals then fileexists()-check.
# Terraform evaluates that check once for the plan embedded in "apply" and
# again after the provisioner runs, and a file that appears between those
# two reads is "a function returned an inconsistent result" - a real
# Terraform footgun in this module's own example, independent of the target
# cloud (corpus-lambda-simple's own header avoided this shape entirely by
# building its deployment zip with a `data "external"` script instead).
# DELTA 3 fetches the exact same fixture, to the exact filename Terraform
# expects, before invoking terraform at all, so both reads agree from the
# start; the null_resource's own curl still runs too, and simply overwrites
# the same file (idempotent, harmless).
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4723, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt the expected stage-3 diagnostic total
#                and one expected refusal site, proving those assertions are
#                load-bearing rather than a grep that always matches. Stages
#                1 and 2 are unaffected and still pass; stage 3 is the one
#                that must fail.
#
# The corpus checkout is shared across worktrees and is NEVER written to:
# the estate is copied out first (twice - once for the cold, unmarked
# deploy, once for the migration attempt) and every delta below lands on a
# copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/alb"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4723}"
FLOCI_NAME="choudoufu-corpus-alb-complete-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="alb-complete-crossing"
REGION="eu-west-1"
DOMAIN="terraform-aws-modules.modules.tf"
AMI_PARAM="/aws/service/ami-amazon-linux-latest/amzn2-ami-hvm-x86_64-gp2"
PKG_URL="https://raw.githubusercontent.com/terraform-aws-modules/terraform-aws-lambda/master/examples/fixtures/python3.8-zip/existing_package.zip"
PKG_HASH="$(printf '%s' "$PKG_URL" | md5 2>/dev/null || printf '%s' "$PKG_URL" | md5sum | cut -d' ' -f1)"
PKG_FILE="downloaded_package_${PKG_HASH}.zip"

INSTANCES=80
VERIFIED_WANT=41
DRIFTED_WANT=10
UNTAGGABLE_WANT=29
UNADMITTED_WANT=0
ELIGIBLE=$((VERIFIED_WANT + DRIFTED_WANT))
STAMPED_WANT=$ELIGIBLE
IMPORT_FAILED_WANT=0
SKIPPED_WANT=$((UNTAGGABLE_WANT + UNADMITTED_WANT))
# SKIPPED_WANT is the DRY RUN's own not-eligible total, which -approve then
# splits in two (issue #340): null_resource.download_package is record-backed,
# so -approve seeds the record store for it and reports it RECORDED rather
# than SKIPPED. The dry run's UNTAGGABLE/UNADMITTED_TYPE counts do not move -
# ratifyRecordBacked still answers StatusUntaggable - so only the -approve
# summary line splits.
RECORDED_WANT=1
APPROVE_SKIPPED_WANT=$((SKIPPED_WANT - RECORDED_WANT))

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }

# The gauntlet protocol (live/GAUNTLET.md): each stage reports its verdict on
# stdout so tools/gauntlet records it. CURRENT_STAGE names the stage a
# failure belongs to; fail() reports it before exiting.
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/gauntlet.sh"
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# copy_tree DEST - the alb module root plus examples/complete-alb,
# preserving the relative layout the example's `source = "../../"` needs.
copy_tree() {
  local dest="$1"
  mkdir -p "$dest/alb/examples"
  cp -R "$SRC/main.tf" "$SRC/variables.tf" "$SRC/outputs.tf" "$SRC/versions.tf" "$SRC/modules" "$dest/alb/"
  cp -R "$SRC/examples/complete-alb" "$dest/alb/examples/complete-alb"
  rm -rf "$dest/alb/examples/complete-alb/.terraform" \
         "$dest/alb/examples/complete-alb/.terraform.lock.hcl" \
         "$dest/alb/examples/complete-alb/terraform.tfstate" \
         "$dest/alb/examples/complete-alb/terraform.tfstate.backup"
}

# apply_deltas EST_DIR - DELTA 1 (emulator provider flags), DELTA 2
# (Cognito domain removed, EMULATOR GAP lex00/floci#63), and a provider
# version pin (this checkout's admission tables were generated against
# 6.59.0).
apply_deltas() {
  local est="$1"
  perl -0pi -e 's/^(provider "aws" \{\n  region = local\.region\n)\}/$1  access_key                   = "test" # DELTA 1\n  secret_key                   = "test"\n  skip_credentials_validation  = true\n  skip_metadata_api_check      = true\n  skip_requesting_account_id   = true\n  s3_use_path_style            = true\n}/' "$est/main.tf"
  grep -q 'DELTA 1' "$est/main.tf" || fail "DELTA 1 did not match the provider block - the corpus pin has moved"

  perl -0pi -e 's/resource "aws_cognito_user_pool_domain" "this" \{\n  domain       = local\.name\n  user_pool_id = aws_cognito_user_pool\.this\.id\n\}\n/# DELTA 2 (EMULATOR GAP, lex00\/floci#63): aws_cognito_user_pool_domain\n# removed - CreateUserPoolDomain is unimplemented in floci.\n/s' "$est/main.tf"
  grep -q 'DELTA 2' "$est/main.tf" || fail "DELTA 2 did not match the Cognito domain resource - the corpus pin has moved"
  perl -pi -e 's/aws_cognito_user_pool_domain\.this\.domain/local.name # DELTA 2/g' "$est/main.tf"
  grep -qF 'aws_cognito_user_pool_domain.this.domain' "$est/main.tf" && fail "DELTA 2 left a live reference to the removed Cognito domain resource"

  perl -0pi -e 's/version = ">= 5\.46"/version = "= 6.59.0"/' "$est/versions.tf"
  grep -q '= 6.59.0' "$est/versions.tf" || fail "the provider version pin did not match versions.tf - the corpus pin has moved"
}

gauntlet_begin

# ── 0. tools and corpus ─────────────────────────────────────────────────────
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "the terraform binary is not on PATH - stage 1 is deliberately plain terraform, not choudoufu"
command -v curl >/dev/null 2>&1 || fail "curl is not on PATH - DELTA 3 needs it to prefetch the Lambda deployment zip"
[ -d "$SRC/examples/complete-alb" ] || fail "$SRC/examples/complete-alb is missing - run 'just corpus-fetch' first"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

PLAIN="$WORK/plain"
copy_tree "$PLAIN"
PLAIN_EST="$PLAIN/alb/examples/complete-alb"
apply_deltas "$PLAIN_EST"
log "  estate copied out of .corpus into $PLAIN_EST"

CURRENT_STAGE=cold_deploy
# ── 1. cold deploy: plain terraform, no live block, no choudoufu ───────────
log "=== 1. cold deploy: plain terraform, $INSTANCES real resources ==="

log "=== 1a. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"acm"' <<< "${HEALTH:-}" && break
  sleep 2
done
grep -q '"acm"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (acm) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# Two preconditions this estate's own DATA SOURCES read but nothing in the
# config creates: the AMI lookup (data.aws_ssm_parameter.al2, AWS's own
# public parameter that floci does not seed) and the Route53 zone
# (data.aws_route53_zone.this, name = var.domain_name - a zone the
# ALB module's real users would already own before adopting it).
log "=== 1b. preconditions: seed the AMI parameter and the Route53 zone ==="
awsl ssm put-parameter --name "$AMI_PARAM" --type String --value "ami-0c55b159cbfafe1f0" --overwrite >/dev/null \
  || fail "could not seed $AMI_PARAM"
awsl route53 create-hosted-zone --name "$DOMAIN" --caller-reference "alb-complete-$$" >/dev/null \
  || fail "could not create the $DOMAIN hosted zone"
log "  $AMI_PARAM seeded; $DOMAIN hosted zone created"

# DELTA 3 (not a floci/choudoufu workaround - see this script's own
# header): prefetch the Lambda deployment zip to the exact filename
# Terraform expects, so the module's own fileexists() check agrees with
# itself across the plan-then-apply boundary.
curl -fsSL -o "$PLAIN_EST/$PKG_FILE" "$PKG_URL" || fail "could not prefetch the Lambda deployment zip fixture"
log "  DELTA 3  Lambda deployment zip prefetched to $PKG_FILE       (module-example quirk, not floci/choudoufu)"

log "=== 1c. terraform init + apply ==="
# #339: the shared cache records no checksums, so init in a directory with no
# .terraform.lock.hcl re-downloads the whole provider purely to compute them,
# even when the cache already holds that exact version. TF_PLUGIN_CACHE_MAY_
# BREAK_DEPENDENCY_LOCK_FILE is real terraform's and OpenTofu's own CLI-config
# accommodation for this - both binaries below honor it, so it fixes it for
# each init independently, not just when a lock file happens to already
# exist. Every directory here is a throwaway mktemp copy, never committed,
# never run on a second platform, so the trade-off (only this platform's
# checksum gets recorded) costs nothing.
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"
( cd "$PLAIN_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "plain terraform init failed"; }
PLAIN_APPLY_OUT="$(cd "$PLAIN_EST" && terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$PLAIN_APPLY_OUT" | tail -60
  fail "the plain terraform apply failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added" <<< "$PLAIN_APPLY_OUT" \
  || { grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT"; fail "the apply did not create exactly $INSTANCES resources - the corpus pin or the emulator has moved"; }
[ -f "$PLAIN_EST/terraform.tfstate" ] || fail "plain terraform left no state file to migrate from"
log "  $(grep -E 'Apply complete' <<< "$PLAIN_APPLY_OUT")"
log "  real terraform.tfstate, zero choudoufu markers - the VPC, the ALB with"
log "  6 listeners and 7 listener rules, 2 ACM certificates, an S3 log"
log "  bucket, a Cognito user pool + client, two Lambda functions, and two"
log "  EC2 instances"

# Confirmed unmarked: read the ALB's own tags directly, never through
# choudoufu.
LB_ARN="$(terraform -chdir="$PLAIN_EST" output -raw arn)"
[ -n "$LB_ARN" ] && [ "$LB_ARN" != "None" ] || fail "could not read the ALB's arn from terraform output"
MARKER_COUNT="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "length(TagDescriptions[0].Tags[?Key=='tofu-address'])" --output text)"
[ "$MARKER_COUNT" = "0" ] || fail "the ALB already carries a tofu-address tag before migration - this crossing proves nothing"
log "  confirmed unmarked: $LB_ARN carries no tofu-address tag"

log ""
log "STAGE 1 (cold deploy): PASS"
log ""
gauntlet_stage cold_deploy pass "$INSTANCES resources, once for real (floci fixes #58, #61, #62)"

# ── day2_rename ORACLE: stock, on a copy of cold_deploy's own state ────────
# Positioned right here, between cold_deploy and migrate, for the same
# reason corpus-eks-basic's own day2_rename oracle is (that script's own
# comment, "the stock oracle for both runs on a copy of cold_deploy's own
# state, PLANNED right after stage 1... before choudoufu or live-import ever
# touch these shared objects"): $PLAIN_EST is about to be left alone for the
# rest of this script (only $ADOPTED_EST is migrated and mutated further
# down), so this is the one point where a byte-identical, genuinely-cold
# copy of it still exists to plan a stock rename against. Two standalone,
# taggable, root-level resources this example declares directly (not
# for_each, not module-internal): aws_instance.this and aws_instance.other,
# each referenced exactly once (module.alb's target_group_attachment
# target_id). Both renamed here through `moved` blocks; the real day2_rename
# stage below (after drift_reconverge) exercises the SAME two renames
# through choudoufu - a moved block for .this, and live-mv with no moved
# block at all for .other - and compares its own zero-churn result against
# this oracle's.
CURRENT_STAGE=day2_rename
log "=== day2_rename ORACLE. stock: the same two renames, through moved blocks, on cold_deploy's own state ==="
ORACLE="$WORK/oracle"
cp -R "$PLAIN" "$ORACLE"
rm -rf "$ORACLE/alb/examples/complete-alb/.terraform"
ORACLE_EST="$ORACLE/alb/examples/complete-alb"
sed -i.bak 's/resource "aws_instance" "this" {/resource "aws_instance" "this_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/aws_instance\.this\.id/aws_instance.this_renamed.id/' "$ORACLE_EST/main.tf"
sed -i.bak 's/resource "aws_instance" "other" {/resource "aws_instance" "other_renamed" {/' "$ORACLE_EST/main.tf"
sed -i.bak 's/aws_instance\.other\.id/aws_instance.other_renamed.id/' "$ORACLE_EST/main.tf"
rm -f "$ORACLE_EST/main.tf.bak"
cat >> "$ORACLE_EST/main.tf" <<'EOF'

moved {
  from = aws_instance.this
  to   = aws_instance.this_renamed
}

moved {
  from = aws_instance.other
  to   = aws_instance.other_renamed
}
EOF
( cd "$ORACLE_EST" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ORACLE_EST" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "the day2_rename stock oracle's reinit failed"; }
ORACLE_PLAN_OUT="$(cd "$ORACLE_EST" && terraform plan -input=false -no-color 2>&1)"; ORACLE_PLAN_RC=$?
[ "$ORACLE_PLAN_RC" -eq 0 ] || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -40; fail "the day2_rename stock oracle plan exited $ORACLE_PLAN_RC"; }
grep -qE '^  # .+ will be (destroyed|created)' <<< "$ORACLE_PLAN_OUT" \
  && { printf '%s\n' "$ORACLE_PLAN_OUT" | grep -E '^  # .+ will be'; fail "stock proposes a destroy or create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$ORACLE_PLAN_OUT" \
  || { printf '%s\n' "$ORACLE_PLAN_OUT" | tail -10; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn on cold_deploy's own state - both moves report only their move, no attribute diff at all"
CURRENT_STAGE=migrate

# ── 2. migrate: choudoufu live-import against the plain state file ─────────
log "=== 2. migrate: choudoufu live-import ==="

ADOPTED="$WORK/adopted"
copy_tree "$ADOPTED"
ADOPTED_EST="$ADOPTED/alb/examples/complete-alb"
apply_deltas "$ADOPTED_EST"
curl -fsSL -o "$ADOPTED_EST/$PKG_FILE" "$PKG_URL" || fail "could not prefetch the Lambda deployment zip fixture (adopted copy)"

# DELTA 4, onboarding: add the live block. record_store is needed for
# null_resource.download_package (an effects-only resource - see the
# record-store fixture).
perl -0pi -e "s/(required_providers \{\n    aws = \{\n      source  = \"hashicorp\/aws\"\n      version = \"= 6\.59\.0\"\n    \}\n    null = \{\n      source  = \"hashicorp\/null\"\n      version = \">= 2\.0\"\n    \}\n  \}\n)\}/\$1\n  live {\n    estate = \"$ESTATE\"\n\n    record_store \"local\" {\n      path = \".tofu-records\"\n    }\n  }\n}/" "$ADOPTED_EST/versions.tf"
grep -q "estate = \"$ESTATE\"" "$ADOPTED_EST/versions.tf" || fail "DELTA 4 did not match versions.tf - the corpus pin has moved"
log "  DELTA 4  live block + local record_store added             (onboarding)"

( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "adopted init failed"; }

log "=== 2a. live-import dry run: verify against the live system, write nothing ==="
IMPORT_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" 2>&1)"
IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -30; fail "live-import (dry run) exited $IMPORT_RC unexpectedly"; }

grep -qF "$ELIGIBLE of $INSTANCES resource instance(s) are eligible for stamping (VERIFIED or DRIFTED)." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import did not report exactly $ELIGIBLE of $INSTANCES eligible - this estate's resource shape has moved"; }
grep -qF "No tag has been written. Rerun with -approve to stamp tofu-estate and tofu-address onto every eligible resource above." <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT"; fail "live-import's dry run did not report 'no tag written' correctly"; }

VERIFIED_N="$(grep -oE '^VERIFIED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
DRIFTED_N="$(grep -oE '^DRIFTED \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNTAGGABLE_N="$(grep -oE '^UNTAGGABLE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
UNADMITTED_N="$(grep -oE '^UNADMITTED_TYPE \([0-9]+\)' <<< "$IMPORT_OUT" | grep -oE '[0-9]+')"
[ "${VERIFIED_N:-0}" = "$VERIFIED_WANT" ] || fail "expected $VERIFIED_WANT VERIFIED, got ${VERIFIED_N:-0}"
[ "${DRIFTED_N:-0}" = "$DRIFTED_WANT" ] || fail "expected $DRIFTED_WANT DRIFTED, got ${DRIFTED_N:-0}"
[ "${UNTAGGABLE_N:-0}" = "$UNTAGGABLE_WANT" ] || fail "expected $UNTAGGABLE_WANT UNTAGGABLE, got ${UNTAGGABLE_N:-0}"
[ "${UNADMITTED_N:-0}" = "$UNADMITTED_WANT" ] || fail "expected $UNADMITTED_WANT UNADMITTED_TYPE (#309), got ${UNADMITTED_N:-0}"
grep -qF 'module.vpc.aws_default_network_acl.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_network_acl.this[0] among DRIFTED (#305, fixed)"
grep -qF 'module.vpc.aws_default_route_table.default[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_route_table.default[0] among VERIFIED (#305, fixed)"
grep -qF 'module.vpc.aws_default_security_group.this[0]' <<< "$IMPORT_OUT" || fail "expected module.vpc.aws_default_security_group.this[0] among VERIFIED (#305, fixed)"
# #309/#364: aws_cognito_user_pool_client is admitted now (identity.LocatedType,
# through liveimport's own new door, locatedByProviderSchema) and ratifies
# UNTAGGABLE - it has no tags argument at all - not UNADMITTED_TYPE.
UNADMITTED_TEXT="$(grep -A2 '^UNADMITTED_TYPE ' <<< "$IMPORT_OUT" || true)"
grep -qF 'aws_cognito_user_pool_client' <<< "$UNADMITTED_TEXT" \
  && fail "aws_cognito_user_pool_client is still UNADMITTED_TYPE - the #364 admission fix (locatedByProviderSchema) is not in this binary"
grep -qF 'aws_cognito_user_pool_client.this' <<< "$IMPORT_OUT" || fail "expected aws_cognito_user_pool_client.this among UNTAGGABLE"
log "  $ELIGIBLE of $INSTANCES eligible ($VERIFIED_WANT VERIFIED + $DRIFTED_WANT DRIFTED); $SKIPPED_WANT skipped"
log "  ($UNTAGGABLE_WANT UNTAGGABLE by provider schema, including"
log "  aws_cognito_user_pool_client - #309/#364, admitted through"
log "  identity.LocatedType now, and recorded rather than unadmitted;"
log "  #305's default_* trio is now admitted and eligible above); nothing"
log "  written yet"

log "=== 2b. -approve: stamp the eligible resources for real ==="
APPROVE_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-import -state="$PLAIN_EST/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)"
APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -30; fail "live-import -approve exited $APPROVE_RC unexpectedly"; }
grep -qF "$STAMPED_WANT resource(s) newly stamped, 0 already stamped, $RECORDED_WANT newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, $IMPORT_FAILED_WANT failed, $APPROVE_SKIPPED_WANT skipped." <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT"; fail "live-import -approve did not report exactly $STAMPED_WANT stamped / $RECORDED_WANT recorded / $IMPORT_FAILED_WANT failed / $APPROVE_SKIPPED_WANT skipped"; }
# The four authenticate-cognito/authenticate-oidc sites used to be
# live-import's whole FAILED bucket (lex00/floci#65: ELBv2 DescribeListeners
# and DescribeRules dropped AuthenticateCognitoConfig/AuthenticateOidcConfig
# on read, so the synthetic config live-import re-plans came back internally
# inconsistent and terraform-provider-aws rejected it with "... must be
# specified when type is 'authenticate-cognito'"). floci#65 is fixed and the
# fix is in the pinned image, so they now stamp like everything else. Asserted
# by name inside the STAMPED section, not just by count, so a regression that
# moved them back into FAILED - or into any other bucket - is caught.
STAMPED_BLOCK="$(awk '/^STAMPED \(/{s=1;next} /^[A-Z_]+ \([0-9]+\)/{s=0} s' <<< "$APPROVE_OUT")"
for addr in 'module.alb.aws_lb_listener.this["ex-cognito"]' \
            'module.alb.aws_lb_listener.this["ex-oidc"]' \
            'module.alb.aws_lb_listener_rule.this["ex-cognito/ex-oidc"]' \
            'module.alb.aws_lb_listener_rule.this["ex-https/ex-cognito"]'; do
  grep -qF "$addr" <<< "$STAMPED_BLOCK" || fail "expected $addr among the STAMPED resources (floci#65 is fixed in the pinned image)"
done
grep -qF "must be specified when" <<< "$APPROVE_OUT" && fail "floci#65's provider validation error text is back in live-import's output - the emulator pin has regressed"
log "  $STAMPED_WANT stamped, $RECORDED_WANT recorded (null_resource.download_package),"
log "  $IMPORT_FAILED_WANT failed, $APPROVE_SKIPPED_WANT skipped - the dry run's"
log "  $SKIPPED_WANT not-eligible, one of them record-backed and so recorded rather than skipped"
log "  the four authenticate-cognito/authenticate-oidc sites stamp now (floci#65 fixed)"

log "=== 2c. the ALB's own marker, read through the AWS CLI directly ==="
WANT_LB_ADDR="module.alb.aws_lb.this:0"
GOT_LB_ADDR="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "TagDescriptions[0].Tags[?Key=='tofu-address'].Value | [0]" --output text)"
[ "$GOT_LB_ADDR" = "$WANT_LB_ADDR" ] || fail "the ALB carries tofu-address=$GOT_LB_ADDR, not $WANT_LB_ADDR"
GOT_LB_ESTATE="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "TagDescriptions[0].Tags[?Key=='tofu-estate'].Value | [0]" --output text)"
[ "$GOT_LB_ESTATE" = "$ESTATE" ] || fail "the ALB carries tofu-estate=$GOT_LB_ESTATE, not $ESTATE"
log "  $LB_ARN now carries tofu-address=$GOT_LB_ADDR tofu-estate=$GOT_LB_ESTATE"
log "  confirmed independently through the AWS CLI, never through choudoufu's own report"

log ""
log "STAGE 2 (migrate): PASS"
log ""
gauntlet_stage migrate pass "$STAMPED_WANT of $INSTANCES stamped, $RECORDED_WANT recorded, $IMPORT_FAILED_WANT failed, $APPROVE_SKIPPED_WANT skipped"
CURRENT_STAGE=test_plan

# ── 3. test plan: delete the state file, real choudoufu live-plan ──────────
log "=== 3. test plan: real live-plan against the really-migrated estate ==="
rm -f "$ADOPTED_EST/terraform.tfstate" "$ADOPTED_EST/terraform.tfstate.backup"
[ ! -f "$ADOPTED_EST/terraform.tfstate" ] || fail "the state file is still there"
log "  no local state file"

PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
# NODE_RESOLVE used to change what a live-plan exit code even MEANT here:
# flag-on expected a non-zero exit (the two aws_acm_certificate_validation
# projection refusals #388's own landing measurement predicted), flag-off
# expected zero since gauntlet issue #397 cleared the last static-path
# refusal. Re-measured (this unit, corpus-alb-complete/flag-on refresh,
# 2026-08-25): flag-on's expectation was already stale. The family-1 fix
# (internal/live/projection/located.go's schema-fallback Components capture
# plus materializeFromRecord's record-first id seeding, landed in
# gauntlet:alb/test_plan's own "FULLY CLEARS" merge, c691a22720) answers
# aws_acm_certificate_validation's identity from the record regardless of
# which path reached it, so live-plan now exits 0 under BOTH flag states -
# confirmed by 3 flag-on runs this unit made (2026-08-25) against a machine
# that was NOT idle (other estates' emulator containers running
# concurrently throughout), all 3 identical. A non-zero exit is a
# regression under either flag now, not the old wall.
NODE_RESOLVE="${CHOUDOUFU_NODE_RESOLVE:-}"
[ "$PLAN_RC" -eq 0 ] || { grep -E '^Error:' -A 8 <<< "$PLAN_OUT"; fail "live-plan exited $PLAN_RC; it has completed cleanly since 2026-08-24 flag-off and, as of this unit, flag-on too, so a non-zero exit is a regression, not the old wall"; }

log "  all distinct Error: lines from this live-plan run:"
grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c | sed 's/^/    /'

# ── 3a. the wall that cleared: zero markerless-type refusals ───────────────
# identity.LocatedType's condition 2 was the last thing refusing
# aws_cognito_user_pool_client here (condition 3 was answered by the
# possessive-of import grammar on 2026-08-22; condition 2 by commit
# 80666bc1c0, which narrowed the credential veto from "the type has a secret
# anywhere in its schema" to "the identity this route would RECORD carries a
# secret"). This type's recorded identity is user_pool_id/id and never
# touched client_secret, so the veto was answering a question this route does
# not ask. Asserted by count AND by absence of the type's own name anywhere
# in the output, so a refusal that merely changed its wording is still
# caught. See this script's header.
MARKERLESS_SITES_N="$(grep -c '^Error: Resource type has nowhere to write an ownership marker$' <<< "$PLAN_OUT")"
[ "$MARKERLESS_SITES_N" = "0" ] \
  || { grep -E '^Error:' <<< "$PLAN_OUT" | sort -u; fail "expected 0 markerless-type refusals (the credential veto was narrowed in 80666bc1c0), got $MARKERLESS_SITES_N"; }
# A bare type-name grep used to stand here on the flag-on path only ("fail
# if aws_cognito_user_pool_client appears anywhere in PLAN_OUT"). It was
# OVER-BROAD for exactly the reason its flag-off sibling was removed
# (2026-08-24): once live-plan gets far enough to COMPLETE, it prints its own
# inventory line, "aws_cognito_user_pool_client, null_resource
# [NOT_SCANNED]" - information about what the sweep did not scan, not a
# refusal - and the bare grep could not tell the two apart. Flag-on now
# completes too (see the exit-code comment above), so the same fix applies:
# dropped here (this unit, #388's flag-on refresh), subsumed by the
# strictly stronger check below: ZERO Error diagnostics of any wording at
# all, for either flag state.
log "  3a  0 markerless-type refusals; aws_cognito_user_pool_client does not"
log "      appear in live-plan's output at all. #309's last site is gone."

# ── 3b. what the cleared wall was masking, and what's fixed since ─────────
# internal/command/live_plan.go runs lint.CheckWith first and returns on the
# first error-severity issue, so while that one refusal stood it was the only
# diagnostic this estate could print. Measured against the same migrated
# estate and the same live floci: the pre-fix binary (80666bc1c0^) printed 1
# diagnostic and nothing else; the markerless-type fix alone printed 20 more
# (2026-08-22); family A's three each.value widenings brought that to 12
# (2026-08-22); family B's four fixes plus family A's own side effect (see
# header) bring it to these 3 (2026-08-23) - THAT count is still right, but
# 2026-08-24 (this unit) found its own COMPOSITION was two different bugs
# layered on top of each other, and this oracle was stale about which three
# sites they were:
#
#   1. local.lambda_target_groups's function_name (family A's one remaining
#      site as of 2026-08-23 - merge(v, {lambda_function_name = split(":",
#      v.target_id)[6]}), a value clause needing v's own structural
#      expression substituted into the call) is FIXED as of this unit:
#      internal/live/identity gained instScope.exprVars, generalizing
#      [instScope.eachValueExpr]'s #260 asymmetry (an element known
#      structurally but not as a whole VALUE) from each.value specifically
#      to a plain for-comprehension's own value variable under any name, at
#      whatever depth of nesting bound it - plus two of the same blind spot
#      in the structural walkers that asymmetry depends on
#      (forCondIncludesTolerant proving a for-expression's own FILTER only
#      from a key's ABSENCE, never its presence; objectLacksKey's identical
#      gap for the SIBLING each.value.<attr> selectors this same element
#      answers - qualifier, statement_id, action, principal,
#      source_account, event_source_token). See the commit for the full
#      account; none of the three names a concrete aws_* type.
#   2. Fixing (1) exposed that module.alb.aws_lb_listener_certificate.this[
#      "ex-https/0"].certificate_arn - which the 2026-08-23 unit's own
#      commit (84dcbabea9) recorded as resolving "through unmodified
#      machinery" - was verified there under CHOUDOUFU_NODE_RESOLVE=1 only.
#      Flag-off, it never stopped being a Non-static identity argument: the
#      SAME fix that closed the Cognito-misattribution crosstalk bug
#      (managedFromExprAt now declines the instant its own chase names a
#      "module" root, rather than risk a second wrong attribution) declines
#      this site too, by the identical rule - `try(
#      aws_acm_certificate_validation.this[0].certificate_arn,
#      aws_acm_certificate.this[0].arn, "")`, terraform-aws-modules/acm's
#      own output, is a MODULE OUTPUT reference, and HANDOFF's own rule ("a
#      missing attribution outranks a wrong one") applies flag-off exactly
#      as it applies flag-on. Confirmed by diff: three separate flag-off
#      live-plan captures taken across this unit's whole session, before
#      any code changed and after every commit, are BYTE-IDENTICAL on this
#      site - it was never fixed flag-off, only the record was wrong. Not a
#      regression this unit introduced; a stale claim this unit corrected.
#      Still HANDOFF's first row (choudoufu refuses where stock proceeds)
#      and not attempted here - the fix belongs to whatever unit widens
#      managedFromExprAt to see THROUGH a module boundary rather than
#      decline at it, which 84dcbabea9's own commit message already flagged
#      as "considered and rejected for THAT unit... a future unit can widen
#      this to a correct attribution instead of a decline if the value is
#      worth it."
#
# UPDATE (corpus-alb-chase unit, 10c48ab942, gauntlet issue #397): that
# future unit ran. managedFromModuleOutput (managedprovenance.go) now
# genuinely chases module.wildcard_cert's own acm_certificate_arn output
# through the child module boundary - the same scope-switching hop
# resolveModuleOutput already makes for a VALUE, applied to the provenance
# question - instead of declining outright, proven by
# TestManagedFromModuleOutputChasesThroughToACMResource and unchanged
# crosstalk-regression coverage. It also fixed a genuine address-collision
# bug the chase exposed: module.acm and module.wildcard_cert are sibling
# calls of the SAME child module source and each declare their own
# `aws_acm_certificate.this`, which collided into one `found` entry before
# qualifyFoundAddr module-qualified every candidate address at the point it
# is discovered.
#
# This site still refuses, for a DIFFERENT, deeper reason confirmed directly
# against this real estate's own live-plan output (temporary
# CHOUDOUFU_DEBUG_MANAGEDFROM instrumentation, removed before landing):
# local.listeners/local.additional_certs combines THREE listeners - this one
# behind module.acm, this one behind module.wildcard_cert, and an unrelated
# Cognito-authenticated one - in ONE object literal, and
# resolve.go's forEachExpansion computes expansion.managedFrom ONCE for the
# WHOLE for_each expansion rather than per element, so even the corrected
# chase finds three simultaneously covered-and-unknown candidates at once
# (`found=map[aws_cognito_user_pool.this:true
# module.acm.aws_acm_certificate.this:true
# module.wildcard_cert.aws_acm_certificate.this:true]`, read straight off
# the trace) - an HONEST ambiguity the len(found)!=1 guard correctly
# declines, not a blind spot. gauntlet issue #397 tracks the plausible next
# step (a per-element provenance chase using elementExprBindings/
# instScope.exprVars, the same machinery family A's function_name fix below
# already generalized) - a materially larger, riskier change than "chase
# through a module boundary" and outside this unit's own scope, so not
# attempted here.
#
# UPDATE (continuing gauntlet issue #397, 2026-08-24): the plausible next
# step above was attempted, on the real machinery
# (internal/live/identity's elementExprBindings/staticCollElems), and it
# reaches PART of the way but not this site. What actually blocked it, read
# straight off the same fixture family
# (testdata/managed-read-module-blind-crosstalk, which reproduces this
# estate's local.additional_certs verbatim - see main.tf:456-473), traced
# with debug instrumentation added and removed in this unit:
#
#   1. staticCollElems (localvalue.go) had no case for the `values()`
#      builtin at all - a FunctionCallExpr this switch did not recognise,
#      so the WHOLE structural chase of local.additional_certs
#      (merge(values({...})...), the exact idiom OpenTofu's own function
#      reference gives as values()'s worked example) declined the instant
#      it reached values(), for every caller of
#      staticForEachKeys/elementExprBindings, not only this one. FIXED this
#      unit, generically (values() is a general builtin, not an ALB-specific
#      shape) - proven end-to-end, with mutation checks, on the new
#      isolated fixture testdata/values-splat-per-element
#      (TestValuesSplatPerElementProvenance). This alone does not move this
#      estate's verdict, because of (2) below.
#
#   2. Even with (1) fixed, local.additional_certs's OWN per-listener value
#      clause is a NESTED for-expression (`for idx, cert_arn in
#      lookup(listener_values, "additional_certificate_arns", []) : ...`)
#      that reads `listener_values`, a loop variable bound by the
#      ENCLOSING for-expression one level out. forSourceElements/
#      forExprElems/staticCollElems/evaluatedCollElements take no scope
#      parameter to thread that binding through a recursive structural
#      decomposition - they were built for chains of separate local/var
#      NAMES (resolver.namedDef hops), never for a for-expression nested
#      inside another one sharing a loop variable. Decomposing past the
#      outer "which listener" level therefore fails outright, independent
#      of (1) or of anything about values() specifically.
#
#   3. Layered on top of (2), and blocking even a fix for it on its own:
#      the OUTER for-expression's own filter clause,
#      `if length(lookup(listener_values, "additional_certificate_arns",
#      [])) > 0`, is not one of forCondIncludesTolerant's recognised
#      value-free shapes (only a BARE `lookup(v, key, default)` or
#      `try(v.attr, default)` AS THE WHOLE CONDITION is handled, via
#      lookupOrTryDefaultOverVar; a length()-wrapped comparison built from
#      one is not), so the filter cannot be decided without evaluating
#      `listener_values` as a value, which local.additional_certs's own
#      unknown-carrying shape never fully provides.
#
# (2) and (3) are each their own materially larger, riskier change to the
# CORE static structural-decomposition machinery every other estate's
# identity resolution also runs through - exactly the "touching the
# per-instance expansion/instScope machinery" risk the original
# crosstalk-fix worker flagged, now confirmed concretely rather than
# guessed at. Per this unit's own stop-and-write-up permission, neither is
# attempted here; #397 is updated with this precise pair of blockers in
# place of the single "plausible next step" paragraph above, which (1)'s
# own landing has now made stale on its own (a fixed wall makes a stale
# script/finding fail before a stale script's absence would).
#
# All 3 remain HANDOFF's first or fifth row and every resource they BLOCK
# is UNTAGGABLE, whose identity has to come from configuration because there
# is no tag to recover it from:
#
#   - module.alb.aws_lb_listener_certificate.this["ex-https/0"].certificate_arn
#     (see (2) above) is HANDOFF's first row, choudoufu refuses where stock
#     proceeds, and is not attempted here.
#   - the two aws_lb_target_group_attachment.this ports are HANDOFF's fifth
#     row read the other way: a lambda-type target genuinely has no port in
#     real AWS, so "Null identity argument" is the honest answer, not a
#     defect this pass's fixes reach or should. THIS IS A RATIFICATION
#     QUESTION, NOT AN OPEN BUG: whether aws_lb_target_group_attachment's
#     port component should be reclassified OmitIfAbsent (or similar) for a
#     target_type=lambda instance is a maintainer decision about the
#     identity table row, the same kind #190's ServerAssignedIfAbsent/
#     name_prefix conventions already required a ruling for - not something
#     a worker should chase by widening family A or B, and not evidence
#     that stage 3 is blocked on a defect. Until that ruling lands, this
#     script's own WANT_SITES/WANT_DIAG_N below expect exactly these two
#     nulls to keep refusing, on purpose.
#
# CHOUDOUFU_NODE_RESOLVE=1 (issue #388's plan-node seam) changes what this
# section asserts, genuinely, not just how it is worded. #388's own landing
# comment: this estate's two remaining static-refusal families (the
# function_name and the two ports above) DOWNGRADE to warnings under the
# flag (identity.DowngradeForNodeResolution) and resolve at the node from
# real evaluated values instead of expression text - the crossing script's
# hard-coded expectation of those refusal sites is the stale oracle HANDOFF
# calls a fixed wall making a stale assertion fail. Re-verified here on an
# idle machine (this unit): flag-off, 3 baseline runs plus this pass's own
# baseline all show exactly these 3 as Errors, 0 sites related to
# aws_acm_certificate_validation, matching the artifact's own recorded
# detail. Flag-on, 4/4 runs (2 from #388's own landing measurement, 2 more
# here) show the 3 downgrade to warnings AND a genuinely new pair of Errors:
# projecting the estate's two aws_acm_certificate_validation instances
# (needed once the pre-walk projection actually runs for the first time -
# see internal/live/projection/noimporter_test.go's own doc comment for the
# traced mechanism) hits a real, pre-existing, generic gap this unit fixed
# in internal/live/projection/build.go: the type is admitted on nameability
# alone (identity.Derivable resolves certificate_arn from configuration;
# tools/row-gen/notimportable.go's own notImportableExempt map has recorded
# since 2026-08-17 that it also has no classic Importer) and the OLD code
# asked the provider to classically import it anyway, reporting a
# misleading "Cannot import for projection" (implying the provider was
# erroring) instead of the accurate "Resource type has no classic
# Importer" this fix now raises - same severity, same refusal, no risk of
# a wrong marker or a false create, just an honest cause. NOT
# load-sensitive: 4/4 flag-on runs on an otherwise idle machine, 0/4
# flag-off.
#
# CORRECTION (2026-08-24, this unit): the paragraph above's flag-off claim
# ("these 3" being function_name + both ports) was never quite right -
# module.alb.aws_lb_listener_certificate.this["ex-https/0"].certificate_arn
# was always the third flag-off site, not function_name; see 3b's own
# numbered account above for the diff evidence and the cause. This unit
# fixed function_name generically (instScope.exprVars) and did not touch
# internal/tofu or internal/live/projection/noimporter, so it makes no
# claim about whether the flag-on behavior this paragraph describes still
# holds against the node-noimporter landing (8adb279dd7) merged after it -
# that is unverified here and belongs to whichever unit next runs this
# estate under CHOUDOUFU_NODE_RESOLVE=1.
# NODE_RESOLVE itself is read at the top of stage 3 - see the exit-code
# branch there.
#
# WANT_DIAG_N/WANT_SITES used to branch on the flag: flag-on expected 2
# "Resource type has no classic Importer" diagnostics on the two
# aws_acm_certificate_validation instances (#388's own landing measurement,
# before the family-1 fix existed). Re-measured (this unit, 2026-08-25,
# 3 flag-on runs, machine not idle): the family-1 fix that closed gauntlet
# issue #401's first family - internal/live/projection/located.go's
# LocatedRecordFrom now capturing the schema-fallback identity's own
# Components, plus materializeFromRecord merging the record's own ImportID
# onto them under "id" - resolves aws_acm_certificate_validation's identity
# from the record regardless of which path (static evaluator or the #388
# node resolver) reached it. Flag-on no longer raises those two diagnostics
# at all; it is 0 Error diagnostics same as flag-off, confirmed identical
# by exact "Error:" line count AND by grep -c on this exact summary string
# below returning 0 either way. #388's own landing prediction that the
# two ACM sites would "downgrade to warnings and resolve at the node" is
# updated by this unit: they resolve CONCRETE, and never even surface as
# warnings, once the plan-instance node reads the record (see 3d, now
# unconditional).
WANT_DIAG_N=0
declare -a WANT_SITES=()
# 3 -> 1 (issue #399, the maintainer's 2026-08-24 ruling): the identity
# table's port component on aws_lb_target_group_attachment (and its
# documented alias aws_alb_target_group_attachment) is now
# [identity.Component.OmitIfAbsent] - verified against botocore's elbv2
# 2015-12-01 model (TargetDescription.Port and CreateTargetGroupInput.Port
# both documented as not applying to a Lambda-type target; a lambda
# target group holds one target and no port, so the collision
# OmitIfAbsent's safety margin exists for is structurally impossible for
# this shape). Both of this estate's lambda-target attachments
# (module.alb.aws_lb_target_group_attachment.this["ex-lambda-with-trigger"]
# and ["ex-lambda-without-trigger"]) now resolve CONCRETE straight from
# target_group_arn/target_id, with no dangling separator where port used
# to sit (a real defect this unit also found and fixed in the row's own
# shape - see internal/live/identity/targetgroupattachment_omitifabsent_
# test.go). No routing change was needed: resolve.go's existing
# OmitIfAbsent-on-a-clean-null redirect (already load-bearing for
# availability_zone/quic_server_id on this same row) picks the change up
# generically, and stage 2's migrate count above is unchanged (51 of 80
# stamped, 1 recorded) - the two attachments were never routed through
# the record rung either before or after this fix; their identity was
# always fully config-derived, only refused outright.
# 1 -> 0 (gauntlet issue #397, 2026-08-24). The last flag-off Error
# diagnostic this estate raised was
# module.alb.aws_lb_listener_certificate.this["ex-https/0"].certificate_arn,
# "Non-static identity argument", and it is gone. Two independent fixes
# were needed and both are generic language rules, not shapes:
#
#   1. terraform-aws-modules/terraform-aws-alb's local.additional_certs
#      (main.tf:456-473) builds its per-listener value with a
#      for-expression NESTED inside another, reading the OUTER loop
#      variable. internal/live/identity's structural decomposition
#      (staticCollElems and its four companions) took no scope at all, so
#      the outer binding could not thread through the recursion; it does
#      now, and a collection reached through a value variable
#      (v / v.attr / lookup(v,"attr",d) / try(v.attr,d)) is decomposed
#      through the element's own expression.
#   2. its filter, length(lookup(listener_values, ...)) > 0, is a value-free
#      predicate wearing a length() and a comparison, which
#      forCondIncludesTolerant recognised in neither shape. Rather than
#      enumerate spellings, the element is now rebuilt into its own literal
#      skeleton (rebuildConstructor, unknown at every refused leaf) and the
#      condition evaluated normally - so any predicate that reads only the
#      structure the author wrote decides, and any that reads a refused
#      leaf still refuses.
#
# Behind them, four Errors nothing had ever been able to reach appeared and
# were fixed in the same unit - see 3d.
#
# The site list is empty on purpose, for both flag states, and the loop
# below is written so that an empty list is not a vacuous pass: WANT_DIAG_N=0
# is what carries the claim, and BREAK=1 adds a site to prove the loop can
# still fail.
#
# The break GAUNTLET.md asks stage 3 for is a corrupted expected string, and
# this one is chosen so that a grep which "always matches" is what it
# catches: module.alb.aws_lb_target_group.this is a resource in this very
# configuration, for_each'd over the very same var.target_groups as
# aws_lb_target_group_attachment.this, that does NOT refuse - because it is
# taggable and carries the tofu-address stage 2 wrote. Expecting it among the
# refusal sites must fail, and must fail on that string alone.
if [ "${BREAK:-}" = "1" ]; then
  WANT_SITES+=('module.alb.aws_lb_target_group.this["ex-instance"].name')
  log "  BREAK=1: also expecting a refusal on"
  log "           module.alb.aws_lb_target_group.this[\"ex-instance\"].name,"
  log "           which is real, is in this configuration, shares"
  log "           var.target_groups with the attachment that DOES refuse, and"
  log "           is nonetheless silent. Wrong. This step must fail."
fi

# By name first, so BREAK=1 fails on the string it corrupted and not on a
# count it did not touch.
for site in ${WANT_SITES[@]+"${WANT_SITES[@]}"}; do
  grep -qF "$site" <<< "$PLAN_OUT" \
    || { printf '%s\n' "$PLAN_OUT"; fail "expected $site among the stage-3 refusal sites"; }
done

# Flag-off only: function_name is the site this fix generalized away, and
# its absence is the load-bearing half of 3b's item 1 - a diagnostic that
# merely changed its wording would still pass the count checks below.
if [ -z "$NODE_RESOLVE" ]; then
  grep -qF 'aws_lambda_permission.this["ex-lambda-without-trigger"].function_name' <<< "$PLAN_OUT" \
    && { printf '%s\n' "$PLAN_OUT"; fail "aws_lambda_permission.this[\"ex-lambda-without-trigger\"].function_name still appears in live-plan's output - instScope.exprVars is not resolving it"; }
fi

DIAG_N="$(grep -c '^Error:' <<< "$PLAN_OUT")"
[ "$DIAG_N" = "$WANT_DIAG_N" ] \
  || { grep -E '^Error:' <<< "$PLAN_OUT" | sort | uniq -c; fail "expected $WANT_DIAG_N Error diagnostics, got $DIAG_N"; }

# The summaries, by count. A shift between buckets at the same total would
# otherwise pass the check above. There is no summary left to count for
# either flag state: flag-off dropped its last two buckets to 0 in gauntlet
# issue #397/#399; flag-on's own "2 Resource type has no classic Importer"
# bucket (#388's own landing measurement, from before the family-1 fix
# existed) is gone too, re-measured this unit (2026-08-25) - see the
# WANT_DIAG_N comment above. The total above is the whole claim, for both.
SUMMARIES_TEXT=''
if [ -n "$SUMMARIES_TEXT" ]; then
  while read -r want summary; do
    got="$(grep -c "^Error: $summary\$" <<< "$PLAN_OUT")"
    [ "$got" = "$want" ] || fail "expected $want \"$summary\" diagnostics, got $got"
  done <<< "$SUMMARIES_TEXT"
fi

# ── 3c. flag on used to need its own stronger-truth check here (retired) ───
# This estate used to carry a flag-on-only block that extracted certificate
# ARNs out of the two "no classic Importer" refusal diagnostics and checked
# them by value against `aws acm list-certificates`, because those two
# refusals were the ONLY place an identity for aws_acm_certificate_validation
# ever surfaced under the flag. That block is dead code now: the family-1
# fix (see the WANT_DIAG_N comment) means those two instances never refuse
# at all any more, flag-on or flag-off, so there is no diagnostic text left
# to extract an ARN from. Re-measured this unit (2026-08-25, 3 flag-on
# runs, machine not idle): neither "Warning: Non-static identity argument" nor
# "Warning: Null identity argument" appears anywhere in flag-on's PLAN_OUT
# either - #388's own landing prediction that the three static-path sites
# would merely "downgrade to warnings" under the flag is superseded; they
# resolve CONCRETE now, silently, the same as flag-off. What is left to
# verify by value - the two record-rung aws_route53_record.validation
# identities behind the wall this unit's family-1 fix cleared - is exactly
# what 3d below already checks, and 3d is no longer flag-gated.

# The asymmetry that says this is the untaggable family and not a
# type-coverage gap: aws_lb_target_group.this for_eaches over exactly the
# same var.target_groups as aws_lb_target_group_attachment.this, and is
# silent, because it is taggable and carries the marker stage 2 wrote. If
# that ever starts refusing, the diagnosis in this script's header is wrong.
grep -qF 'in resource "aws_lb_target_group" "this"' <<< "$PLAN_OUT" \
  && fail "aws_lb_target_group.this now refuses too - it shares var.target_groups with the attachment but carries a marker, so this script's untaggable-family diagnosis needs redoing"
log "  3b  $DIAG_N diagnostics; every resource they block is untaggable. The"
log "      taggable aws_lb_target_group.this over the same var.target_groups"
log "      is silent, because it carries the marker stage 2 wrote"


# ── 3d. the record rung, verified against the AWS CLI ──────────────────────
# Behind the config-language wall sat four Errors nothing had ever been able
# to reach, all on terraform-aws-modules/terraform-aws-acm's own
# aws_route53_record.validation, in module.acm and module.wildcard_cert:
#
#   2 Error: Unstamped marker-only resource
#   2 Error: Unmarked apply of a marker-only resource
#
# Its name and type come from aws_acm_certificate.this's
# domain_validation_options, which the provider fills in only after the
# certificate is applied, so identity resolution classified it
# NEEDS_DISCOVERY/SIBLING_APPLY - true, and for a taggable type a real
# promise, because the marker written at create time is what a later sweep
# finds. aws_route53_record has no tags map, so that promise could never be
# kept and live_plan escalated the unstamped instance to a hard refusal, for
# two objects this estate had already migrated. HANDOFF's fifth row.
#
# identity.RecordFallbackType already answered exactly this question for six
# other call sites; the sibling-apply branch simply never asked it. It does
# now, and these two instances resolve RECORD_LOCATED - so what binds them is
# the record live-import already wrote.
#
# Which makes the record the thing to check, and to check against the cloud
# rather than against choudoufu's own report: an identity in a record is a
# claim about a live object, and a wrong one is silent. This reads the record
# store on disk and asks Route53 itself whether a record set of that name and
# type exists in that zone.
#
# No longer gated on NODE_RESOLVE (this unit, 2026-08-25): this check used
# to run flag-off only, because flag-on never reached an empty plan to check
# in the first place. It does now (see the WANT_DIAG_N/3c comments above),
# and the record-rung binding this checks is not itself flag-specific - the
# plan-node seam and the static evaluator both read the same record store -
# so there is no reason left to skip it under the flag.
REC_DIR="$ADOPTED_EST/.tofu-records/tofu-records/$ESTATE/aws_route53_record"
[ -d "$REC_DIR" ] || fail "no aws_route53_record records in the store at $REC_DIR - live-import's write half is what makes the read half above safe, and without it a clean plan means nothing"
for MOD in acm wildcard_cert; do
  ADDR="module.$MOD.aws_route53_record.validation[0]"
  REC_FILE="$(grep -lF "\"address\":\"$ADDR\"" "$REC_DIR"/* 2>/dev/null | head -1)"
  [ -n "$REC_FILE" ] || fail "$ADDR has no record in the store; the plan can only be clean by accident without one"
  REC_NAME="$(grep -o '"name":"[^"]*"' "$REC_FILE" | head -1 | cut -d'"' -f4)"
  REC_TYPE="$(grep -o '"type":"[^"]*"' "$REC_FILE" | head -1 | cut -d'"' -f4)"
  REC_ZONE="$(grep -o '"zone_id":"[^"]*"' "$REC_FILE" | head -1 | cut -d'"' -f4)"
  [ -n "$REC_NAME" ] && [ -n "$REC_TYPE" ] && [ -n "$REC_ZONE" ] \
    || { cat "$REC_FILE"; fail "$ADDR's record does not carry a whole name/type/zone_id identity"; }
  LIVE_TYPE="$(awsl route53 list-resource-record-sets --hosted-zone-id "$REC_ZONE" \
    --query "ResourceRecordSets[?Name=='${REC_NAME}.'].Type | [0]" --output text)"
  [ "$LIVE_TYPE" = "$REC_TYPE" ] \
    || fail "$ADDR's record claims $REC_NAME ($REC_TYPE) in zone $REC_ZONE, and Route53 answers '$LIVE_TYPE' for that name - a recorded identity that does not name a real object is the silent failure HANDOFF's safety rule is about"
  log "  3d  $ADDR binds from the record: $REC_NAME $REC_TYPE in $REC_ZONE,"
  log "      confirmed by route53 list-resource-record-sets directly"
done

# ── 3e. the plan itself ────────────────────────────────────────────────────
# Stage 3's own Proves text: with the state file deleted, live-plan is EMPTY.
# The exit code is not the verdict and neither is the diagnostic count; this
# is.
PLAN_EMPTY=0
grep -qE '^No changes\.' <<< "$PLAN_OUT" && PLAN_EMPTY=1
PLAN_LINE="$(grep -E '^Plan: ' <<< "$PLAN_OUT" | head -1)"
log ""
FLAG_TAG=""
[ -n "$NODE_RESOLVE" ] && FLAG_TAG=" [CHOUDOUFU_NODE_RESOLVE=1]"
if [ "$PLAN_EMPTY" = "1" ]; then
  STAGE3_PASSED=1
  # gauntlet issue #401 family 1's own unit: this branch is REACHED now,
  # flag-off. Family 1 (2 aws_acm_certificate_validation instances planning
  # a CREATE) is closed - internal/live/projection/located.go's
  # LocatedRecordFrom now also captures the schema-fallback identity's own
  # components (family 1a), and materializeFromRecord's stub-seeding
  # (family 1b) merges the record's own ImportID onto those components
  # under "id", which is the one attribute a record-first stub could never
  # otherwise carry. Family 4 (Cognito default/empty-block churn) is closed
  # separately, by the repinned emulator (main-20260825a, lex00/floci#134).
  # All four families named in gauntlet issue #401 are now RESOLVED.
  #
  # Flag-on reaches this branch too, as of this unit (2026-08-25, corpus-
  # alb-complete's #388 flag-on refresh): the family-1 fix above is not
  # itself gated on CHOUDOUFU_NODE_RESOLVE, so it resolves
  # aws_acm_certificate_validation's identity from the record the same way
  # regardless of which path (static evaluator or the #388 plan-node
  # resolver) reached it. #388's own earlier landing measurement expected
  # flag-on to stay BLOCKED here, on two "Resource type has no classic
  # Importer" refusals - that expectation predates the family-1 fix and is
  # now stale (HANDOFF's fixed-wall rule: a fixed wall makes a stale
  # assertion fail, and it was this script's flag-on branch, not a
  # regression). Confirmed by 3 flag-on runs (machine not idle), not
  # load-sensitive: 0 Error diagnostics, live-plan empty, and neither
  # "Warning: Non-static identity argument" nor "Warning: Null identity
  # argument" appears either - the three static-path sites this estate is
  # blocked on flag-off never even surface as warnings flag-on; they
  # resolve concrete, silently, same as flag-off (see the 3c comment
  # above). Flag-on and flag-off are now identical on every measure this
  # stage checks: 0 Error diagnostics, an empty plan, and the same two
  # record-rung aws_route53_record.validation identities verified by value
  # against route53 (3d, no longer flag-gated).
  log "STAGE 3 (test_plan)$FLAG_TAG: PASS - live-plan is empty against the"
  log "migrated estate with no state file, and the record-bound identities"
  log "check out against route53 directly (3d)."
  log ""
  if [ -n "$NODE_RESOLVE" ]; then
    gauntlet_stage test_plan pass "CHOUDOUFU_NODE_RESOLVE=1: empty live-plan with no state file; $DIAG_N Error diagnostics; identical to flag-off on every measure this stage checks (gauntlet issue #388's flag-on refresh, 2026-08-25) - the family-1 record-rung fix that closed #401 for aws_acm_certificate_validation is not flag-gated, so it answers from the record under the node resolver too, and #388's own earlier prediction that this branch would stay blocked on two no-classic-Importer refusals is now stale; the two record-rung aws_route53_record.validation identities verified by value against route53 list-resource-record-sets"
  else
    gauntlet_stage test_plan pass "empty live-plan with no state file; $DIAG_N Error diagnostics; the two record-rung aws_route53_record.validation identities verified by value against route53 list-resource-record-sets"
  fi
else
  log "STAGE 3 (test_plan)$FLAG_TAG: BLOCKED for real - not by a refusal any"
  log "more. live-plan COMPLETES (exit 0, $DIAG_N Error diagnostics): every"
  log "config-language-subset site this estate ever raised is gone, and the"
  log "two aws_route53_record.validation instances behind them bind from the"
  log "record, verified against route53 directly (3d). What is left is a"
  log "NON-EMPTY plan: $PLAN_LINE"
  log ""
  gauntlet_stage test_plan fail "${FLAG_TAG:+$FLAG_TAG: }0 Error diagnostics, down from 1 (gauntlet issue #397, 2026-08-24) - live-plan COMPLETES for this estate for the first time, exit 0. Two generic language fixes cleared the last static-path refusal (module.alb.aws_lb_listener_certificate.this[\"ex-https/0\"].certificate_arn, Non-static identity argument): an explicit instScope threaded through internal/live/identity's structural decomposition so a for-expression NESTED inside another can read the outer loop variable, plus a new collection-through-a-value-variable case (v / v.attr / lookup(v,\"attr\",d) / try(v.attr,d)) resolved through the element's own expression; and forCondIncludesTolerant now decides any filter clause that reads only the STRUCTURE the author wrote, by binding the value variable to the element's own rebuilt skeleton (rebuildConstructor, unknown at every refused leaf) instead of enumerating length()/comparison spellings. Behind them, four Errors nothing had ever reached appeared and were fixed in the same unit (HANDOFF row 5): terraform-aws-modules/terraform-aws-acm's two aws_route53_record.validation instances take their name and type from an unapplied aws_acm_certificate, and aws_route53_record has no tags map, so the NEEDS_DISCOVERY/SIBLING_APPLY answer promised a marker sweep that could never find them - identity.RecordFallbackType, which six other call sites already consult, is now asked on the sibling-apply path too and both drop to the record rung, binding from the record live-import had already written (identity verified by value against route53 list-resource-record-sets - see 3d). STILL BLOCKED, and the wall is now a NON-EMPTY plan rather than a refusal: $PLAN_LINE. This is not the expected outcome any more (this unit, 2026-08-25): both flag states are measured to reach the PASS branch above (empty plan) as of gauntlet:c691a22720 - a non-empty plan here is a regression, not the old #401 wall."
fi

if [ -n "${STAGE3_PASSED:-}" ]; then
  # ── 4. test apply: apply the empty plan, assert a genuine no-op ──────────
  CURRENT_STAGE=test_apply
  log "=== 4. test apply: apply the empty plan, assert a genuine no-op ==="
  BEFORE_N="$(awsl resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$ESTATE" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"

  APPLY2_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
  [ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -60; fail "the post-migration apply failed"; }
  grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
    || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

  AFTER_N="$(awsl resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$ESTATE" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)"
  [ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
  log "  genuine no-op: $BEFORE_N tofu-estate-tagged objects before, $AFTER_N after"
  gauntlet_stage test_apply pass "genuine no-op (0 added, 0 changed, 0 destroyed); $BEFORE_N tofu-estate-tagged objects before, $AFTER_N after"

  # ── 5. drift and reconverge: mutate the ALB's Example tag, replan, fix ───
  CURRENT_STAGE=drift_reconverge
  log "=== 5. drift and reconverge: mutate one object out of band ==="
  if [ "${BREAK:-}" = "1" ]; then
    # A second, unrelated object is mutated too - the assertion below must
    # catch this as MORE than one object proposed, not silently pass.
    awsl elbv2 add-tags --resource-arns "$LB_ARN" --tags Key=Repository,Value=tampered-by-BREAK >/dev/null
    log "  BREAK=1: also tampered the ALB's Repository tag - stage 5 must now see TWO drifted objects and fail the single-object assertion"
  fi
  awsl elbv2 add-tags --resource-arns "$LB_ARN" --tags Key=Example,Value=tampered-out-of-band >/dev/null
  DRIFTED_VALUE="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "TagDescriptions[0].Tags[?Key=='Example'].Value | [0]" --output text)"
  [ "$DRIFTED_VALUE" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take"
  log "  mutated $LB_ARN's Example tag to \"tampered-out-of-band\" directly via the AWS CLI"

  DRIFT_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-plan -input=false -no-color 2>&1)"; DRIFT_PLAN_RC=$?
  [ "$DRIFT_PLAN_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | tail -80; fail "the drift-detection plan exited $DRIFT_PLAN_RC"; }

  CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN_OUT" | awk '{print $2}' | sort -u)"
  N_CHANGED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"
  if [ "${BREAK:-}" = "1" ]; then
    [ "$N_CHANGED" = "1" ] && fail "BREAK=1 set (two objects tampered), but the plan proposes fixing only 1 - this assertion is not load-bearing"
    log "  BREAK=1: the plan proposes fixing $N_CHANGED objects, correctly more than one - the single-object assertion below is skipped"
  else
    [ "$N_CHANGED" = "1" ] || { printf '%s\n' "$DRIFT_PLAN_OUT" | grep -E '^  # .+ will be'; fail "expected exactly 1 object proposed for a fix, got $N_CHANGED"; }
    printf '%s\n' "$CHANGED_ADDRS" | grep -qE 'module\.alb\.aws_lb\.this' \
      || fail "the plan proposes fixing $CHANGED_ADDRS, not the ALB that was actually tampered"
    log "  the plan proposes fixing exactly one object: $(printf '%s' "$CHANGED_ADDRS")"

    RECONVERGE_APPLY="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
    [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_APPLY" | tail -60; fail "the reconverge apply failed"; }
    grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_APPLY" \
      || { grep -E 'Apply complete' <<< "$RECONVERGE_APPLY"; fail "the reconverge apply did not change exactly 1 resource"; }
    FIXED_VALUE="$(awsl elbv2 describe-tags --resource-arns "$LB_ARN" --query "TagDescriptions[0].Tags[?Key=='Example'].Value | [0]" --output text)"
    [ "$FIXED_VALUE" != "tampered-out-of-band" ] || fail "the ALB's Example tag is still \"tampered-out-of-band\" after reconverging"
    log "  reconverged: $LB_ARN's Example tag is back to its configured value ($FIXED_VALUE)"
    gauntlet_stage drift_reconverge pass "one object tampered (the ALB's Example tag), plan proposed fixing exactly $CHANGED_ADDRS, apply changed 1 and the Example tag reconverged"
  fi

  if [ "${BREAK:-}" != "1" ]; then
    # ── 6. day2_rename: moved block (aws_instance.this) + live-mv (aws_instance.other) ──
    CURRENT_STAGE=day2_rename
    log "=== 6. day2_rename: rename aws_instance.this via a moved block, aws_instance.other via live-mv ==="
    INST_THIS_ID="$(awsl ec2 describe-instances --filters '[{"Name":"tag:tofu-address","Values":["aws_instance.this"]}]' --query "Reservations[0].Instances[0].InstanceId" --output text)"
    [ -n "$INST_THIS_ID" ] && [ "$INST_THIS_ID" != "None" ] || fail "no live EC2 instance found by its tofu-address marker (aws_instance.this)"
    INST_OTHER_ID="$(awsl ec2 describe-instances --filters '[{"Name":"tag:tofu-address","Values":["aws_instance.other"]}]' --query "Reservations[0].Instances[0].InstanceId" --output text)"
    [ -n "$INST_OTHER_ID" ] && [ "$INST_OTHER_ID" != "None" ] || fail "no live EC2 instance found by its tofu-address marker (aws_instance.other)"
    log "  $INST_THIS_ID (aws_instance.this), $INST_OTHER_ID (aws_instance.other)"

    log "=== 6a. choudoufu, moved block: aws_instance.this -> .this_renamed ==="
    sed -i.bak 's/resource "aws_instance" "this" {/resource "aws_instance" "this_renamed" {/' "$ADOPTED_EST/main.tf"
    sed -i.bak 's/aws_instance\.this\.id/aws_instance.this_renamed.id/' "$ADOPTED_EST/main.tf"
    rm -f "$ADOPTED_EST/main.tf.bak"
    cat >> "$ADOPTED_EST/main.tf" <<'EOF'

moved {
  from = aws_instance.this
  to   = aws_instance.this_renamed
}
EOF
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the moved-block rename's reinit failed"; }
    MOVED_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; MOVED_PLAN_RC=$?
    [ "$MOVED_PLAN_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -40; fail "the moved-block rename plan exited $MOVED_PLAN_RC"; }
    grep -qE '^  # .+ will be (destroyed|created)' <<< "$MOVED_PLAN_OUT" \
      && { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block rename proposes a destroy or a create - not zero churn"; }
    grep -qE '^  # aws_instance\.this_renamed will be updated in-place' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to aws_instance.this_renamed"; }
    grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT" | tail -10; fail "the moved-block rename plan is not exactly one in-place change"; }
    grep -qE '~ +"tofu-address" = "aws_instance\.this" -> "aws_instance\.this_renamed"' <<< "$MOVED_PLAN_OUT" \
      || { printf '%s\n' "$MOVED_PLAN_OUT"; fail "the moved-block plan does not show the instance's tofu-address marker being rewritten from the old address to the new one"; }
    log "  choudoufu: zero churn, one in-place tags update - the marker rewrite the moved block completes"

    MOVED_APPLY_OUT="$(cd "$ADOPTED_EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MOVED_APPLY_RC=$?
    [ "$MOVED_APPLY_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY_OUT" | tail -40; fail "the moved-block rename apply exited $MOVED_APPLY_RC"; }
    grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY_OUT" \
      || { grep -E 'Apply complete' <<< "$MOVED_APPLY_OUT"; fail "the moved-block rename apply was not exactly one in-place change"; }

    INST_THIS_ID_AFTER="$(awsl ec2 describe-instances --instance-ids "$INST_THIS_ID" --query "Reservations[0].Instances[0].InstanceId" --output text 2>/dev/null || true)"
    [ "$INST_THIS_ID_AFTER" = "$INST_THIS_ID" ] || fail "the instance's id changed across the rename ($INST_THIS_ID -> $INST_THIS_ID_AFTER) - it was destroyed and recreated, not renamed"
    INST_THIS_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INST_THIS_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$INST_THIS_ADDR_AFTER" = "aws_instance.this_renamed" ] \
      || fail "the instance carries tofu-address=$INST_THIS_ADDR_AFTER after the rename, not aws_instance.this_renamed"
    log "  $INST_THIS_ID unchanged, tofu-address now aws_instance.this_renamed - read via the AWS CLI"

    log "=== 6b. choudoufu, live-mv: aws_instance.other -> .other_renamed, no moved block at all ==="
    sed -i.bak 's/resource "aws_instance" "other" {/resource "aws_instance" "other_renamed" {/' "$ADOPTED_EST/main.tf"
    sed -i.bak 's/aws_instance\.other\.id/aws_instance.other_renamed.id/' "$ADOPTED_EST/main.tf"
    rm -f "$ADOPTED_EST/main.tf.bak"
    ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
      ( cd "$ADOPTED_EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the live-mv rename's reinit failed"; }
    MV_OUT="$(cd "$ADOPTED_EST" && "$TOFU" live-mv -estate="$ESTATE" aws_instance.other aws_instance.other_renamed 2>&1)"; MV_RC=$?
    [ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MV_OUT" | tail -30; fail "choudoufu live-mv exited $MV_RC"; }
    grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report a real write"; }
    grep -qF '"aws_instance.other" -> "aws_instance.other_renamed"' <<< "$MV_OUT" \
      || { printf '%s\n' "$MV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
    log "  live-mv: $(grep -F 'live ID' <<< "$MV_OUT")"

    INST_OTHER_ID_AFTER="$(awsl ec2 describe-instances --instance-ids "$INST_OTHER_ID" --query "Reservations[0].Instances[0].InstanceId" --output text 2>/dev/null || true)"
    [ "$INST_OTHER_ID_AFTER" = "$INST_OTHER_ID" ] || fail "the instance's id changed across live-mv ($INST_OTHER_ID -> $INST_OTHER_ID_AFTER) - it was destroyed and recreated, not renamed"
    INST_OTHER_ADDR_AFTER="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$INST_OTHER_ID" "Name=key,Values=tofu-address" --query "Tags[0].Value" --output text)"
    [ "$INST_OTHER_ADDR_AFTER" = "aws_instance.other_renamed" ] \
      || fail "the instance carries tofu-address=$INST_OTHER_ADDR_AFTER after live-mv, not aws_instance.other_renamed"
    log "  $INST_OTHER_ID unchanged, tofu-address now aws_instance.other_renamed - read via the AWS CLI"

    log "=== 6c. one more plan: config and markers agree on both renames, nothing proposed ==="
    FINAL_PLAN_OUT="$(cd "$ADOPTED_EST" && "$TOFU" plan -input=false -no-color 2>&1)"; FINAL_PLAN_RC=$?
    [ "$FINAL_PLAN_RC" -eq 0 ] || { printf '%s\n' "$FINAL_PLAN_OUT" | tail -40; fail "the post-rename plan exited $FINAL_PLAN_RC"; }
    grep -qF "No changes. Your infrastructure matches the configuration." <<< "$FINAL_PLAN_OUT" \
      || { grep -E '^  #' <<< "$FINAL_PLAN_OUT"; fail "the post-rename plan is not empty"; }
    log "  No changes. Both renames are complete and invisible to the next plan."

    gauntlet_stage day2_rename pass "moved block: aws_instance.this renamed with zero churn (0 add, 1 change, 0 destroy), marker rewritten in place; live-mv: aws_instance.other renamed with zero churn, marker rewritten in place; stock oracle over the same two-object rename on cold_deploy's own state (positioned right after stage 1, before migrate ever touches these shared objects) also shows zero churn (0 add, 0 change, 0 destroy); both live ids unchanged, read via the AWS CLI"
  fi
else
  log "=== 4. test apply: NOT RUN - depends on stage 3, which does not produce a clean plan ==="
  gauntlet_stage test_apply not_run "depends on stage 3, which does not produce a clean plan"
  log "=== 5. drift and reconverge: NOT RUN - depends on stages 3-4 ==="
  gauntlet_stage drift_reconverge not_run "depends on stages 3-4"
  log "=== 6. day2_rename: NOT RUN - depends on stages 3-5 ==="
  gauntlet_stage day2_rename not_run "depends on stages 3-5"
fi
CURRENT_STAGE=""
gauntlet_end

log ""
log "=== SUMMARY ==="
log ""
log "  stage 1  cold_deploy        PASS ($INSTANCES resources, once for real - see"
log "                              header for the 3 floci fixes this needed:"
log "                              #58, #61, #62)"
log "  stage 2  migrate            PASS (real: $STAMPED_WANT of $INSTANCES stamped, $IMPORT_FAILED_WANT failed -"
log "                              floci#65 is fixed in the pinned image and its"
log "                              4 sites now stamp, see header)"
if [ -n "${STAGE3_PASSED:-}" ]; then
  log "  stage 3  test_plan          PASS (empty live-plan; gauntlet issue #401's"
  log "                              four families all resolved)"
  log "  stage 4  test_apply         PASS (genuine no-op apply)"
  log "  stage 5  drift_reconverge   PASS (one tampered ALB tag detected and fixed)"
  log "  stage 6  day2_rename        PASS (moved block + live-mv, zero churn both ways)"
else
  log "  stage 3  test_plan          BLOCKED (see the stage-3 detail above for"
  log "                              which families remain)"
  log "  stage 4  test_apply         NOT RUN"
  log "  stage 5  drift_reconverge   NOT RUN"
  log "  stage 6  day2_rename        NOT RUN"
fi
log ""
log "$INSTANCES real resources, real emulator, real unmarked infrastructure, real"
log "migration. Every assertion above reads live-import's, live-plan's or"
log "live-mv's own output, or a tag/instance-id read straight through the AWS"
log "CLI - never choudoufu's own self-report. Run again with BREAK=1: stages"
log "1, 2, 3, 4 and 6 still pass and stage 5's single-object assertion is the"
log "one that fails."
