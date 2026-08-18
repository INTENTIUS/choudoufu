# choudoufu build targets. `just --list` shows everything.

# Build the choudoufu binary into the current directory
build:
    go build ./cmd/choudoufu

# Unit tests. Integration tiers skip unless their env vars are set.
test:
    go test ./...

# Exactly what .github/workflows/ci.yml runs, in order, so a red main is
# something you find here rather than on GitHub. `env -u PWD` is needed for
# the test step and only locally: /Users/alex/checkouts is a symlink and
# os.Getwd() honours PWD, which the Linux runner does not have to care about.
# TestCIRunsEveryForkOwnedTestPackage (live/ci_coverage_test.go) keeps the
# package list here and in the workflow from drifting apart.
#
# Run exactly what CI runs, in order, before pushing.
ci:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "==> gofmt (fork-owned packages)"
    out="$(gofmt -l internal/live cmd site tools live internal/command)"
    if [ -n "$out" ]; then echo "gofmt needed on:"; echo "$out"; exit 1; fi
    echo "==> build"
    go build ./cmd/choudoufu
    echo "==> fast test tier"
    env -u PWD go test ./internal/live/... ./tools/... ./live/ ./cmd/... ./internal/command/
    echo "==> docs site build"
    (cd site && go run . -out public/)
    echo "==> CI steps passed"

# Check whether background subagents (dispatched via the Agent tool) are
# still writing, without reading their full transcripts into context.
# Usage: just agent-progress <task-id> [task-id...]
agent-progress *ids:
    bash .claude/scripts/agent-progress.sh {{ids}}

# Floci integration tier: needs Docker and the AWS CLI.
test-floci:
    make test-floci

# Issue #64's estate-scale benchmark against floci. ESTATE_BENCH_N=<n> just bench-estate sets the size (default 200).
bench-estate:
    make bench-estate

# The demo: real estate on a local emulator, state file deleted mid-run, plans stay exact. Needs Docker, ~2 minutes, exit 0 = every claim held.
demo:
    bash live/e2e/run.sh --expect 5

# Issue #73's record-backed lifecycle end to end: a record_store declared, the
# four RECORD_ADMITTED types created, re-planned clean, replaced and destroyed.
# No Docker and no AWS - null, time and random are cloud-free providers, so this
# runs against a local directory in well under a minute. It is the only
# end-to-end exercise the record-backed class has, which is why it is a recipe
# rather than a script you have to know about.
demo-records:
    bash live/e2e/record-store/run.sh

# Issue #255's estate-wide tagging sweep end to end: a resource's block
# deleted, the live resource found through ONE Resource Groups Tagging API
# call by the command wiring a user actually runs, and a control run showing
# the per-type fallback cannot see it at all. Needs Docker and the AWS CLI;
# runs on its own port (4601) so it can run beside `just demo`.
demo-tagging-sweep:
    bash live/e2e/tagging-sweep/run.sh

# The create-over-existing defect, end to end and pinned: a needs-discovery
# resource whose type loses its tags on the provider's list path is invisible
# to marker discovery, so a live-plan proposes creating what the estate
# already owns and an apply then creates a second one, once per run. Exit 0
# means the defect is still there; when it goes red the fix has landed and the
# script says which assertions to invert. Needs Docker and the AWS CLI; runs
# on its own port (4602) so it can run beside `just demo`.
demo-create-over:
    bash live/e2e/create-over/run.sh

# Component.PerElement end to end: a set-valued identity tail rendered one
# sorted segment per element, binding a live object with no state file and no
# tag - aws_iam_user_group_membership is untaggable, so the identity has no
# carrier and re-derives from the declaration. Two of the three memberships
# declare their groups OUT OF ORDER, and the run asserts the declared-order
# string never appears: a set has no order on the wire, so only the rendered
# string can tell a sorted identity from a copied one. Needs Docker and the
# AWS CLI; runs on its own port (4604) so it can run beside `just demo`.
demo-per-element:
    bash live/e2e/per-element/run.sh

# Issue #270's record-located class end to end: an object with nowhere to
# carry an ownership marker, whose id the provider minted at create time,
# found again after the state file is deleted - by the estate's record store
# and by nothing else. aws_cloudfront_public_key's id appears nowhere in the
# configuration, so the run's rendered identity is checked against the
# EMULATOR's own answer rather than against the record it read; the run then
# points one record at the other key's object and requires that check to
# fail. Ends by deleting a record and proving a lost one costs an announced
# duplicate, never a deletion. Needs Docker and the AWS CLI; runs on its own
# port (4605) so it can run beside `just demo`.
demo-record-located:
    bash live/e2e/record-located/run.sh

# Issue #274's crossing, on a real third-party estate rather than a fixture:
# .corpus/mastino/prod-eu-west/services/message-queue, 28 aws_sqs_queue in a
# module and one aws_iam_policy in the root, kept by its authors in a
# Terraform Cloud workspace. The estate is copied out of .corpus and never
# written to; the four onboarding deltas it needs are applied to the copy and
# each one is asserted. It applies 29 resources, writes no terraform.tfstate
# at all, replans empty twice, and every one of the 29 rendered import
# identities is checked as a string - the emulator answers a wrong-region SQS
# queue URL with the right queue's ARN, so the plan verdict cannot tell a
# wrong region from a right one and only the string can. BREAK=1 corrupts one
# expected string and the run must then fail in step 6 and nowhere else.
# Needs Docker, the AWS CLI and a fetched corpus; runs on its own port (4632)
# so it can run beside `just demo`.
demo-corpus-message-queue:
    bash live/e2e/corpus-message-queue/run.sh

# Issue #274's step 6: a real third-party estate crossed against a real
# emulator. .corpus/mastino/global/dns is 63 instances of DataCite's own
# production DNS - two hosted zones sharing the name datacite.org, told apart
# by their markers alone, and 59 untaggable records that carry no marker and
# do not need one. Applied, stripped of its state file, replanned empty twice,
# with every rendered identity checked against Route 53's own answer rather
# than against a verdict. Steps 4 and 7 pin the two defects this estate found
# on its first contact with a cloud. Needs Docker, the AWS CLI and a populated
# .corpus (`just corpus-fetch`); runs on its own port (4605) so it can run
# beside `just demo`.
demo-corpus-crossing:
    bash live/e2e/corpus-crossing/run.sh

# Issue #274's step 6, on a terraform-aws-modules EXAMPLE rather than one
# org's private estate: .corpus/iam/examples/iam-policy, the configuration a
# new user copies first when they reach for the aws provider. Two
# aws_iam_policy instances behind the iam-policy module - one from a literal
# policy document, one from a rendered aws_iam_policy_document data source -
# applied, stripped of their state file, and replanned empty twice, with
# both rendered identities checked against IAM's own answer. The estate
# needed no provider pin and no backend edit, and its root outputs mean
# OpenTofu never prints a literal "Plan: 0 to add" line on any run against
# it - see the script's header for why, and why step 5 asserts the absence
# of a resource action header instead. BREAK=1 corrupts one expected
# identity string and the run must then fail in step 5 and nowhere else.
# Needs Docker, the AWS CLI and a populated .corpus (`just corpus-fetch`);
# runs on its own port (4680) so it can run beside `just demo`.
demo-corpus-iam-policy:
    bash live/e2e/corpus-iam-policy/run.sh

# Issue #274's step 6 on .corpus/iam/examples/iam-oidc-provider, whose
# central object is findable only by enumerating the account:
# aws_iam_openid_connect_provider has a server-assigned ARN, so a run that
# cannot list it concludes it does not exist and creates a SECOND one - with
# every plan verdict staying clean, because creating a resource the run
# believes is absent is not an error. Step 7 is that assertion: IAM still
# holds one OIDC provider after a second apply. Three instances cover three
# identity shapes at once - a server-assigned ARN, a name_prefix role whose
# name IAM assigns, and an untaggable attachment whose identity is its two
# endpoints. Step 5 shows force_detach_policies needing a record_store and
# proves it does not settle without one. BREAK=1 corrupts one expected
# identity by a single host label and step 5b must be the only step that
# goes red. Needs Docker, the AWS CLI, outbound HTTPS to GitHub for the
# module's own tls_certificate read, and a populated .corpus; runs on its
# own port (4692) so it can run beside `just demo`.
demo-corpus-oidc-provider:
    bash live/e2e/corpus-oidc-provider/run.sh

# Issue #274's step 6 on a government department's own root module:
# .corpus/govuk-infrastructure/terraform/deployments/chat-evaluation-ci. Same
# server-assigned-ARN subject as demo-corpus-oidc-provider and a different
# estate - GDS's hand-written root rather than a module example - so it adds
# a provider-level default_tags block the markers have to merge into rather
# than replace, two names derived from variable defaults, and the #268 delta
# in its Terraform Cloud `cloud {}` form. Four instances, applied, state file
# deleted, replanned empty twice, every identity checked as a string against
# IAM's own answer, and step 7 asserts IAM still holds ONE OIDC provider.
# BREAK=1 drops the "-policy" suffix off one expected ARN and step 5 must be
# the only step that goes red. Needs Docker, the AWS CLI, outbound HTTPS to
# GitHub for the estate's own tls_certificate read, and a populated .corpus;
# runs on its own port (4693) so it can run beside `just demo`.
demo-corpus-govuk-oidc:
    bash live/e2e/corpus-govuk-oidc/run.sh

# A PINNED DEFECT, not a passing crossing: exit 0 means it is still there.
# Both .corpus/mastino/prod-eu-west/services/analytics-worker and
# .../datafile-generator die on "Listed resource with no identity" over
# aws_ecs_task_definition, and they still do. This runs DataCite's own
# resource block and its own container_definitions file against floci: it
# applies, it carries its marker, ListTaskDefinitions answers, and the
# provider resolves the revision ARN - and then the cold replan refuses,
# because the generated row looks this type up by `id` while the provider's
# own identity schema (live/survey-full.json) says family + revision. The
# script asserts both halves: the refusal BY NAME and the enumeration
# working, so a regression in either is not hidden by the other. This is one
# resource rather than the whole estate because these two estates read
# twelve data sources between them and the emulator has had to grow into
# them one at a time; the private hosted zone that used to be named here is
# no longer among the gaps, and elbv2 never was - that service is fully
# implemented and registered under its signing name, elasticloadbalancing,
# which is the key /_localstack/health reports it under. Needs Docker, the
# AWS CLI and a populated .corpus; runs on its own port (4694).
demo-corpus-ecs-taskdef:
    bash live/e2e/corpus-ecs-taskdef/run.sh

# Issue #274's cloudfront leg, and the first live-cloud contact for the
# unique-name discovery mechanism (aws_cloudfront_cache_policy,
# aws_cloudfront_origin_request_policy - "unique-name" in
# live/survey-full.json), landed the same day this script did. It found and
# fixed a real bug: EVERY unique-name type failed its first apply
# unconditionally, before discovery ever ran, because
# internal/command/live_plan.go's statelessStampGaps re-derived stamping
# severity without consulting identity.DiscoveryCause.BindsByName() the way
# internal/live/stamp's own mustStamp() already did. Step 5 pins the fix and
# fails on the pre-fix message if it regresses.
#
# The FULL 16-instance .corpus/govuk-infrastructure cloudfront estate does
# NOT cross, though it no longer fails where it used to. It spans two
# provider configurations (default and an aliased us-east-1 "global" one)
# with discovery-needing resources on both sides, which was a hard refusal
# before any resource was touched until issue #283 made discovery run one
# scoped pass per configuration. Step 3 asserts that refusal is gone and
# pins where the estate stops instead: aws_wafv2_web_acl, declared on
# aws.global, has no list operation the provider serves. Step 6 isolates the two
# unique-name resources (extracted verbatim, not retyped) and finds a
# SEPARATE, floci-only gap: Cloud Control's List/GetResource for these two
# types answers with a flat Properties shape, not AWS's own documented one
# (Name nested under CachePolicyConfig / OriginRequestPolicyConfig -
# live/registry.json's unique_name_property), so the crossing itself
# (delete state, replan empty) cannot complete against floci - choudoufu
# correctly refuses rather than binding wrong. Needs Docker, the AWS CLI and
# a populated .corpus; runs on its own port (4694) so it can run beside
# `just demo`.
demo-corpus-cloudfront:
    bash live/e2e/corpus-cloudfront/run.sh

# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/salesforce-api,
# 6 instances - the lambda-residue defect (filename, source_code_hash,
# publish never settling without a record_store) at twice the population,
# since this estate deploys TWO filename-zipped Lambdas rather than one,
# plus the aws_cloudwatch_event_rule/target pair and two aws_lambda_permission
# instances riding along. Same two-phase shape as demo-records: PHASE 1 with
# no record_store reproduces the defect and shows applying it does not
# settle it; PHASE 2 adds one record_store block, applies once, and replans
# empty twice. All 6 rendered identities are checked against the emulator's
# own answer. BREAK=1 corrupts one expected string and the run must catch it
# in step 6 and nowhere else. Needs Docker, the AWS CLI and a populated
# .corpus; runs on its own port (4697) so it can run beside `just demo`.
demo-corpus-salesforce-api:
    bash live/e2e/corpus-salesforce-api/run.sh

# Issue #274's crossing, one of the three smallest untouched real corpus
# estates picked smallest-first to establish the method rather than to
# maximise instance count in one slot:
# .corpus/govuk-aws/terraform/projects/infra-cyber-cloudwatch-to-splunk, one
# aws_cloudwatch_log_subscription_filter. It cost two of #274's four
# onboarding-delta classes despite the single instance: `backend "s3" {}`
# to remove (#268) and `version = "~> 3.25"`, old enough to resolve to a
# release with no list resources at all (#269's shape). The type has no
# tags argument, so its identity - log_group_name and filter name, joined
# the way the provider's own import syntax joins them - re-derives from the
# declaration and needs no marker carrier. Applied, state file deleted,
# replanned empty twice, the rendered identity checked against CloudWatch
# Logs' own answer. BREAK=1 corrupts the expected identity and the run must
# catch it in step 5 and nowhere else. Needs Docker, the AWS CLI and a
# populated .corpus; runs on its own port (4698) so it can run beside
# `just demo`.
demo-corpus-cloudwatch-splunk:
    bash live/e2e/corpus-cloudwatch-splunk/run.sh

# Issue #274's crossing, another of the three smallest untouched real corpus
# estates: .corpus/iam/examples/iam-read-only-policy, a terraform-aws-modules
# EXAMPLE using a DIFFERENT iam module than demo-corpus-iam-policy - one that
# builds its policy from a generated allowed_services matrix rather than a
# literal document, instantiated three times with only the first
# contributing a resource (the other two use create_policy = false and
# create = false respectively). The module's own use_name_prefix defaults to
# true, so the policy's name is server-assigned (the NAME_PREFIX discovery
# shape, same as demo-corpus-oidc-provider's role) rather than statically
# derivable, and the assertion reads the ARN IAM actually minted. No
# backend, no version pin needed - `version = ">= 6.28"` resolves straight
# to 6.60.0 clean, the same absence-is-a-finding result demo-corpus-iam-policy
# found. Applied, state file deleted, replanned empty twice, the rendered
# identity checked against IAM's own answer. BREAK=1 corrupts the expected
# identity and the run must catch it in step 5 and nowhere else. Needs
# Docker, the AWS CLI and a populated .corpus; runs on its own port (4699)
# so it can run beside `just demo`.
demo-corpus-iam-read-only-policy:
    bash live/e2e/corpus-iam-read-only-policy/run.sh

# Issue #274's crossing, smallest-by-instance-count of a second batch:
# .corpus/mastino/prod-eu-west/services/raw-resolution-logs, one
# aws_s3_bucket. Needs no version override - its client-supplied bucket name
# never calls ListBuckets, so the release #269 flags for having no list
# resources never bites. A REAL FINDING, not hidden: the estate's deprecated
# `acl = "private"` argument never round-trips through the provider's Read,
# so live-plan never reaches a fully empty second plan - confirmed
# reproducing byte-for-byte under plain, unmodified `terraform import` +
# `terraform plan` against the same floci, with zero choudoufu code in the
# path. Steps 5-6 assert this explicitly: the update is bounded to exactly
# the known acl/force_destroy attributes, never a create, a destroy, or
# anything else, and the rendered identity (the literal bucket name) is
# checked against S3's own answer both times. BREAK=1 corrupts the expected
# identity and the run must catch it in step 5 and nowhere else. Needs
# Docker, the AWS CLI and a populated .corpus; runs on its own port (4700)
# so it can run beside `just demo`.
demo-corpus-raw-resolution-logs:
    bash live/e2e/corpus-raw-resolution-logs/run.sh

# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/crossref-agent,
# four resources (a CloudWatch event rule and target driving a Lambda
# function, with its EventBridge invoke permission) - the first Lambda-based
# estate this campaign crosses, and could not even bootstrap until #297 (a
# fresh apply's aws_lambda_permission existence check hit floci's
# not-found-shaped GetPolicy response before the function existed, which
# surfaced as a hard "Cannot import for projection" failure). All four types
# are client-named and literal, so no version override is needed even though
# `version = "~> 5"` resolves to the release #269 flags. Needs a Lambda
# execution role, a VPC, two subnets and a security group seeded through the
# AWS CLI over the estate's own (untouched) data-source reads. The estate's
# own deprecated `runtime = "nodejs14.x"` is applied as written; floci does
# not enforce AWS's since-added rejection of new functions on that runtime.
# `record_store "local" {}` (#275) is added to the live block: filename,
# source_code_hash and publish are pure inputs the Lambda API never returns,
# and without the record store a cold live-plan would propose the identical
# update on aws_lambda_function forever - the first real corpus estate to
# confirm #275's fix generalizes beyond its own live/e2e/lambda-residue
# fixture. Applied, state file deleted, replanned empty twice, all 4
# rendered identities checked against the emulator's own answer. BREAK=1
# corrupts the expected identity and the run must catch it in step 5 and
# nowhere else. Needs Docker, the AWS CLI and a populated .corpus; runs on
# its own port (4701) so it can run beside `just demo`.
demo-corpus-crossref-agent:
    bash live/e2e/corpus-crossref-agent/run.sh

# Issue #274's attempt: .corpus/mastino/prod-eu-west/services/crossref-orcid-agent,
# structurally near-identical to crossref-agent (same four resource types,
# same onboarding shape) but named separately and permanently scheduled
# never to fire. Does NOT cross - BLOCKED BY FLOCI, not choudoufu, at the
# very first apply. The estate's deployment zip has one internal entry
# named crossref-agent_runner.js (a copy-paste leftover from the sibling
# service DataCite cloned this estate from), while main.tf's handler names
# crossref-orcid-agent_runner.js. floci's Lambda CreateFunction eagerly
# validates the handler file exists in the deployment package; real AWS
# Lambda does not - it only surfaces a missing/misnamed handler file at
# invoke time - so this estate would apply cleanly against real AWS and
# only misbehave if actually invoked, which its disabled schedule means it
# likely never has been. The script applies the estate byte for byte and
# pins the exact floci error rather than editing around it; it exits 0 when
# it reaches exactly that blocker. Filed as item 7 on issue #287. Needs
# Docker, the AWS CLI and a populated .corpus; runs on its own port (4703)
# so it can run beside `just demo`.
demo-corpus-crossref-orcid-agent:
    bash live/e2e/corpus-crossref-orcid-agent/run.sh

# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/datafiles-generator,
# one resource (aws_s3_bucket.datafiles) - the rest of the estate's
# ECS-based generator is commented out in the source itself, decommissioned
# but the bucket kept. Six data sources OpenTofu evaluates unconditionally
# (a VPC endpoint, an ECS cluster, two IAM roles, a security group and two
# subnets) are seeded even though five feed only the estate's
# commented-out resources. Hits the exact same class of gap
# demo-corpus-raw-resolution-logs already isolated to the provider: the
# deprecated `acl` argument, plus `force_destroy`, never round-trips through
# aws_s3_bucket's Read, so a cold live-plan proposes the identical update
# forever. Applied, state file deleted, replanned twice, bounded to exactly
# that known acl/force_destroy update and nothing else, the rendered
# identity (the literal bucket name) checked against S3's own answer both
# times. BREAK=1 corrupts the expected identity and the run must catch it in
# step 5 and nowhere else. Needs Docker, the AWS CLI and a populated
# .corpus; runs on its own port (4704) so it can run beside `just demo`.
demo-corpus-datafiles-generator:
    bash live/e2e/corpus-datafiles-generator/run.sh

# Issue #274's attempted crossing, and #298's repro:
# .corpus/mastino/prod-eu-west/services/sitemaps-generator, three resources
# (aws_s3_bucket.akita, aws_cloudwatch_log_group and aws_ecs_task_definition)
# apply cleanly and are confirmed live through the AWS CLI, then live-plan
# fails: discovery's Cloud Control fallback (needed because `version = "~> 5"`
# resolves to 5.100.0, the release #269 documented as carrying no list
# resources at all) finds the task definition correctly but hands
# ImportResourceState the literal "family:revision" string instead of the
# ARN, which this provider's importer for that type rejects. Re-pinning to
# 6.58.0 (the #269 workaround demo-corpus-ecs-taskdef uses) clears that step
# but trades it for a floci gap: aws_s3_bucket's tag read under that
# provider version calls S3 Control's ListTagsForResource, addressed via an
# account-ID-prefixed hostname floci cannot resolve. This script does not
# fake a pass - it exits non-zero at step 5, distinguishing #298's exact
# signature from any other failure. Needs Docker, the AWS CLI and a
# populated .corpus; runs on its own port (4705) so it can run beside
# `just demo`.
demo-corpus-sitemaps-generator:
    bash live/e2e/corpus-sitemaps-generator/run.sh

# Issue #274's crossing: .corpus/govuk-aws/terraform/projects/infra-root-dns-zones,
# two aws_route53_zone instances (internal + external) from GDS's Terraform
# 0.12-era module. Its provider pin - `version = "2.46.0"` as a bare
# provider-block argument, no required_providers at all - has no darwin_arm64
# package for this machine, so it is replaced with a real required_providers
# block pinned to 6.59.0, the same #269-shape fix as demo-corpus-cloudwatch-splunk.
# The estate's own `data "terraform_remote_state"` read (an S3-backed state
# file from another team's VPC module) is kept as written; a real S3 object
# holding a minimal, hand-written state file is seeded to answer it. Applied,
# state file deleted, replanned with no resource change proposed twice (the
# estate declares root outputs, so - like demo-corpus-iam-read-only-policy -
# a permanent "Changes to Outputs" section is expected and the assertion
# checks for the absence of a resource action header rather than for
# "Plan:"). Both rendered identities (the real zone IDs, since
# aws_route53_zone is ServerAssigned) checked against Route 53's own answer.
# BREAK=1 corrupts the expected identity and the run must catch it in step 5
# and nowhere else. Needs Docker, the AWS CLI and a populated .corpus; runs
# on its own port (4702) so it can run beside `just demo`.
demo-corpus-root-dns-zones:
    bash live/e2e/corpus-root-dns-zones/run.sh

# Issue #274's step 6, on GOV.UK's own mobile-backend deployment:
# .corpus/govuk-infrastructure/terraform/deployments/mobile-backend, twelve
# instances behind a KMS signing key, an IAM role with two inline policies,
# and an S3 bucket from a shared library module (../../shared-modules/s3,
# copied out alongside it so its relative module source resolves unedited).
# A second provider (fastly/fastly) makes one real outbound HTTPS read to
# Fastly's own public IP-range API - left alone rather than stubbed, since
# it is not AWS and no emulator could answer it. Two pre-existing account
# objects this estate reads but never creates - a GitHub OIDC provider and
# an S3 logging-target bucket - are seeded with the AWS CLI before apply and
# asserted to stay unmarked afterwards. Applied, state file deleted,
# replanned empty twice; twelve instances render six distinct identity
# strings (six S3 sub-resources import by the bucket's own name, AWS's own
# convention), each checked against IAM/KMS/S3's own answer. BREAK=1
# corrupts the KMS key identity and the run must catch it in step 5 and
# nowhere else. Needs Docker, the AWS CLI, outbound HTTPS to Fastly, and a
# populated .corpus; runs on its own port (4706) so it can run beside
# `just demo`.
demo-corpus-mobile-backend:
    bash live/e2e/corpus-mobile-backend/run.sh

# Issue #274's step 6, on GOV.UK's own smallest estate:
# .corpus/govuk-infrastructure/terraform/deployments/service-linked-roles,
# one aws_iam_service_linked_role and nothing else - no data sources, no
# modules. IAM decides the role's name, not this configuration, so the only
# way a second run finds the one it already owns is to enumerate the
# account and read a marker off what comes back. variables-common.tf is the
# same real symlink demo-corpus-mobile-backend's own comment documents,
# shared across every govuk-infrastructure deployment, and it declares
# seven variables this estate never reads - all seven get a tfvars value
# because OpenTofu requires one regardless. Applied, state file deleted,
# replanned empty twice, and the one rendered identity checked as a string
# against IAM's own answer. BREAK=1 corrupts the expected identity and the
# run must catch it in step 5 and nowhere else. Needs Docker, the AWS CLI
# and a populated .corpus; runs on its own port (4707) so it can run beside
# `just demo`.
demo-corpus-service-linked-roles:
    bash live/e2e/corpus-service-linked-roles/run.sh

# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/store-crawler-results,
# four resources - aws_cloudwatch_event_rule, aws_cloudwatch_event_target,
# aws_lambda_function and aws_lambda_permission - DataCite's own periodic
# job, structurally identical to demo-corpus-crossref-agent's four types and
# named as one of #274's "nine more genuinely unattempted" in its comment
# thread. The same four seeded reads (a Lambda role, a VPC, two subnets and
# a security group) and the same record_store delta for the Lambda's
# filename/source_code_hash/publish (#275) apply unchanged; unlike
# crossref-orcid-agent's sibling estate, the deployment zip's handler file
# actually matches what main.tf names, so this one does not hit #287 item
# 7's floci gap. Applied, state file deleted, replanned empty twice, all 4
# rendered identities checked against the emulator's own answer. BREAK=1
# corrupts the expected identity and the run must catch it in step 5 and
# nowhere else. Needs Docker, the AWS CLI and a populated .corpus; runs on
# its own port (4708) so it can run beside `just demo`.
demo-corpus-store-crawler-results:
    bash live/e2e/corpus-store-crawler-results/run.sh

# Issue #274's attempt: .corpus/k8s-io/infra/aws/terraform/cncf-k8s-infra-aws-capa-ami,
# Kubernetes SIG cluster-lifecycle's own IAM setup for CAPA's AMI-building
# pipeline. refusal-probe reports this entry "clean, 1 instance" but never
# resolves its two terraform-aws-modules calls; resolved, it is four
# resources. Does NOT cross - BLOCKED BY CHOUDOUFU, not floci, at the very
# first apply: `policies = { ImageBuilder = aws_iam_policy.imagebuilder.arn }`
# passed into the iam-github-oidc-role module's for_each, then attached via
# a bare `each.value`, refuses with "Dynamic value in static context" - a
# whole-value reference to a SIBLING resource's server-assigned ARN, reached
# across a module-call boundary. The for_each KEY SET is statically known
# (this is not #187/#284's already-fixed ACM shape), and the gap is flagged
# in the source itself: resolve.go's `expansion.keyOnly` doc comment calls
# resolving a bare each.value here "a further extension this fix does not
# make." The script proves it is a genuine parity defect, not an
# unavoidable one: the identical config, same binary, with the live block
# removed, applies cleanly (4 resources) - stock OpenTofu only requires a
# for_each's own key set known at plan time, and "attach the policy I just
# created to the role I just created" is one of the most common patterns in
# Terraform/OpenTofu. Two further deltas get the estate to init at all: a
# module version pin (`~> 5.0`) for an unrelated upstream subdirectory
# rename, and a #269-shape provider version pin (`= 6.58.0`). Filed as
# issue #301. Needs Docker, the AWS CLI and a populated .corpus; runs on
# its own port (4709) so it can run beside `just demo`.
demo-corpus-cncf-k8s-infra-aws-capa-ami:
    bash live/e2e/corpus-cncf-k8s-infra-aws-capa-ami/run.sh

# Issue #274's attempt: .corpus/govuk-infrastructure/terraform/deployments/root-dns
# (NOT the already-crossed demo-corpus-root-dns-zones, a different repo -
# govuk-aws - and a different estate). Three Route53 zones. Does NOT cross -
# blocked before choudoufu's live-marker mechanism is ever reached: remote.tf
# reads `data "tfe_outputs" "vpc"`, which is not AWS at all - it is the
# hashicorp/tfe provider reading GDS's own real, live "govuk" HCP Terraform
# organization over HTTPS, and fails at the first plan/apply with "required
# token could not be found" before any AWS call is made. Neither a choudoufu
# defect nor a floci gap: there is no floci endpoint a `tfe` provider block
# could ever point at, and this campaign has no business authenticating
# against GDS's production HCP Terraform org even if it could. Also proves
# live/corpus-refusals.json's "clean, 0 sites, 3 instances" verdict wrong in
# the way #274 itself opened with - it has never touched a cloud, and cannot.
# Needs Docker, the AWS CLI and a populated .corpus; runs on its own port
# (4710) so it can run beside `just demo`.
demo-corpus-govuk-root-dns:
    bash live/e2e/corpus-govuk-root-dns/run.sh

# Issue #274's crossing: .corpus/mastino/prod-eu-west/services/crossref-related-agent,
# structurally identical to demo-corpus-crossref-agent's four types
# (aws_cloudwatch_event_rule, aws_cloudwatch_event_target,
# aws_lambda_function, aws_lambda_permission) and the sibling
# demo-corpus-crossref-orcid-agent was explicitly flagged as untested for.
# It does NOT share crossref-orcid-agent's floci Lambda-handler bug (#287
# item 7): the deployment zip's one internal entry is named
# crossref-related-agent_runner.js, matching what main.tf's handler names,
# unlike crossref-orcid-agent's copy-paste leftover. The same four seeded
# reads (a Lambda role, a VPC, two subnets and a security group) and the
# same record_store delta for the Lambda's filename/source_code_hash/
# publish (#275) apply unchanged. Applied, state file deleted, replanned
# empty twice, all 4 rendered identities checked against the emulator's own
# answer. BREAK=1 corrupts the expected identity and the run must catch it
# in step 5 and nowhere else. Needs Docker, the AWS CLI and a populated
# .corpus; runs on its own port (4711) so it can run beside `just demo`.
demo-corpus-crossref-related-agent:
    bash live/e2e/corpus-crossref-related-agent/run.sh

# The reference project: VPC, subnet, internet gateway, security group, EC2
# instance - the plainest "getting started" AWS shape, checked both
# directions. GREENFIELD: write it with a live block from the start, apply,
# every object's markers read back through the AWS CLI directly (not
# choudoufu's own report), plan empty, plan empty again with the local
# record_store deleted entirely. ADOPTION: the identical shapes applied
# first with plain stock terraform (real state, zero markers, confirmed via
# the AWS CLI), then migrated with "choudoufu live-import -approve" and
# replanned empty. Not from a corpus - hand-written, no version pins beyond
# the ordinary #269 gap every other estate needs. Needs Docker and the AWS
# CLI; runs on two ports (4712, 4713) so it can run beside `just demo`.
demo-reference-ec2-vpc:
    bash live/e2e/reference-ec2-vpc/run.sh

# .corpus/rds/examples/complete-postgres, from terraform-aws-modules/
# terraform-aws-rds - the de facto standard way people provision RDS
# Postgres, and the first RDS estate ever crossed against a cloud (#102 only
# ever used it for a static, offline measurement). Follows the five-stage
# shape (cold deploy / migrate / test plan / test apply / drift-reconverge),
# and is a genuine partial pass, reported honestly rather than routed
# around: stage 1 stands up 39 real resources with plain terraform, no
# choudoufu involved. Stage 2 (choudoufu live-import) migrates 23 of 39 for
# real (18 VERIFIED + 5 DRIFTED; 16 skipped, 13 untaggable by design and 3
# blocked by #305) - live-import's own root-module-only restriction (#59
# was closed elsewhere but never lifted here) has since been fixed. Stage 3
# (choudoufu live-plan against the really-migrated estate) refuses on
# exactly two known, open, itemized grounds - terraform-aws-modules/
# security-group's ingress_with_cidr_blocks builds an identity argument
# through a lookup()-keyed index the static walker cannot trace (7
# count-index-in-tag sites, #304), and the VPC module's default_* adopters
# (aws_default_network_acl/route_table/security_group, all three actually
# created here) are unadmitted types (3 sites, #305). Two more real,
# unrelated floci gaps (cross-region automated-backups-replication has no
# matching RDS action; SecretsManager RotateSecret wrongly requires a
# Lambda ARN for an RDS-managed secret's Lambda-less hosted rotation) are
# worked around with documented deltas so stage 1 can stand the estate up
# at all - see the script's header for the full account, including the
# exact code locations and the two floci issues filed for them. BREAK=1
# corrupts the stage-3 site-count assertion, proving it is load-bearing.
# Needs Docker, the AWS CLI, real terraform (stage 1 is deliberately not
# choudoufu) and a
# populated .corpus (`just corpus-fetch`); runs on its own port (4720) so it
# can run beside `just demo`.
demo-corpus-rds-complete-postgres:
    bash live/e2e/corpus-rds-complete-postgres/run.sh

# The five-stage real-estate crossing pipeline (cold deploy -> migrate ->
# test plan -> test apply -> drift and reconverge) against
# .corpus/s3-bucket/examples/complete, terraform-aws-modules/terraform-aws-
# s3-bucket's flagship example: 32 instances across 5 module calls and 15
# aws_s3_bucket_* types. Found and fixed on the way: a floci routing bug
# (PUT /{bucket}?accelerate falling through to bucket creation, PR #53) and
# two admission gaps (aws_s3_bucket_accelerate_configuration and
# _request_payment_configuration, ratified from row-gen's own proposal).
# Needs Docker, the AWS CLI, terraform on PATH, and a populated .corpus;
# runs on its own port (4715).
demo-corpus-s3-bucket-complete:
    bash live/e2e/corpus-s3-bucket-complete/run.sh

# Issue #280's crossing: .corpus/simpleinfra/terraform/dns calls one local
# module seven times, and every one of the seven hosted zones used to come
# back carrying module.rustconf_com.aws_route53_zone.zone - one identity on
# seven real objects, after an apply that reported success. The seven
# markers are read off the zones with the AWS CLI rather than out of the
# plan, because the plan showed the right values while the cloud got the
# wrong ones. Point TOFU_BIN at a binary built before
# internal/live/stamp/sharedbody.go and step 4 fails with all seven
# collapsed.
#
# It also crosses: 35 instances applied, the state file deleted, replanned
# empty twice, and all 35 rendered import identities checked as strings
# against Route 53's own answer. The estate is applied EXACTLY as the Rust
# project wrote it - the four trailing dots in impl/main.tf are left on,
# because #281 is fixed and the workaround that used to strip them is gone.
# Needs Docker, the AWS CLI and a populated .corpus; runs on its own port
# (4606) so it can run beside `just demo`.
demo-repeated-module:
    bash live/e2e/repeated-module/run.sh

# The five-stage real-estate crossing pipeline (cold deploy, migrate, test
# plan, test apply, drift and reconverge - live/corpus-crossing-manifest.json)
# for .corpus/lambda/examples/simple, terraform-aws-modules/terraform-aws-
# lambda's minimal entry point - Lambda is one of the most commonly deployed
# AWS services via Terraform, and calling a published module the way this
# example does is how essentially every real Terraform root module uses one.
# Stage 1 (plain terraform, no live block) and stage 2 (choudoufu live-import
# -approve, all three module-nested AWS resources verified by reading their
# tags with the AWS CLI, never through choudoufu's own report) both pass for
# real. Stage 3 currently fails with the real, unmodified choudoufu error:
# type admission runs per declared resource block rather than per resolved
# instance, so aws_lambda_function_url.this and
# aws_lambda_function_recursion_config.this - both count = 0 in this
# example - still refuse the whole plan. See the script's own header for the
# fix stage 2 needed (a module-scope bug in live-import, fixed on this
# branch) and the separate one stage 3 still needs. BREAK=1 corrupts one
# expected tofu-address before stage 2's AWS CLI checks; that step must be
# the only one that fails. Needs Docker, the AWS CLI, python3 and a populated
# .corpus; runs on its own port (4714) so it can run beside `just demo`.
demo-corpus-lambda-simple:
    bash live/e2e/corpus-lambda-simple/run.sh

# A module call expanded with count, crossed against a real emulator. Stamping
# read only a module call's for_each, so every resource under a count'd call
# was marked with the UNKEYED module path - an address identity resolution
# never computes for it. Reads the tofu-address off three real VPCs with the
# AWS CLI, then deletes the state file and rebinds to all three from the tags
# alone. Point TOFU_BIN at a binary built before internal/live/stamp's
# childExpansion and step 4 fails with module.one.aws_vpc.main. Needs Docker
# and the AWS CLI, no corpus; runs on its own port (4607).
demo-counted-module:
    bash live/e2e/counted-module/run.sh

# Issue #193's managed-argument projection end to end: a data source whose
# argument reads an attribute the resource's own block sets, read against a
# real emulator, with the parameter's live value moved out from under the
# configuration first so a static shortcut cannot pass. Needs Docker and the
# AWS CLI; runs on its own port (4599) so it can run beside `just demo`.
demo-dataread:
    bash live/e2e/dataread-projection/run.sh

# terraform-aws-modules/terraform-aws-vpc's flagship "complete" example
# (issue #274's real-estate crossing pipeline), all five stages: cold deploy
# with plain terraform, choudoufu live-import adoption, an empty replan with
# the state file deleted and three rendered identities checked against the
# AWS CLI's own answer, a genuine no-op apply, and drift on one object
# reconverging without touching any other. Needs Docker, the AWS CLI, and a
# real `terraform` (or tofu; see TF_COLD_BIN) on PATH; runs on its own port
# (4713) so it can run beside `just demo`.
demo-corpus-vpc-complete:
    bash live/e2e/corpus-vpc-complete/run.sh

# terraform-aws-modules/terraform-aws-eks's own "basic" example (v9.0.0,
# .corpus/eks/examples/basic), crossed live end to end: cold plain apply,
# choudoufu live-import, live-plan. Stages 1-2 pass in full (54/54 resources
# cold-deployed, 3/4 root-module resources adopted); stage 3 refuses
# outright on real, itemized gaps - 50 resources inside module.vpc and
# module.eks are out of live-import v1's root-module-only scope (issue #59),
# plus unadmitted default_*/VPN-gateway types and undeclared-record-store
# logical resources - so stages 4-5 are unreachable. See the script's own
# header for the full breakdown and the two floci gaps (EKS worker AMI
# discovery, SuspendProcesses/ResumeProcesses) found and fixed along the
# way. Needs Docker (with a socket floci can reach - EKS real mode spawns a
# k3s sibling container) and the AWS CLI; runs on its own port (4718).
demo-corpus-eks-basic:
    bash live/e2e/corpus-eks-basic/run.sh

# terraform-aws-modules/terraform-aws-security-group's flagship "complete"
# example (v6.0.0, .corpus/security-group/examples/complete), crossed live
# end to end: 67 real resources across the root module, its postgresql and
# consul preset submodules, a standalone SG and prefix list, and two nested
# terraform-aws-vpc calls. This module is a common dependency of other
# terraform-aws-modules (rds, eks, ...) - a prior crossing found and filed
# #304 through it as a side effect; this is the first direct crossing of
# the module's own example. v6.0.0 rewrote the module onto per-rule
# aws_vpc_security_group_ingress_rule/egress_rule/rules_exclusive
# resources, so #304's old count.index-in-lookup() pattern does not appear
# here at all. Stages 1-2 pass in full (67/67 cold-deployed via a
# documented DELTA for lex00/floci#57 - EC2 AssociateSecurityGroupVpc has
# no floci handler -, 52/67 resource instances adopted and verified
# independently through the AWS CLI); stage 3 refuses outright on two real,
# itemized gaps - #305's default_*-adopter trio (6 sites, same as other vpc-
# module crossings) and a new one: aws_vpc_security_group_rules_exclusive
# (3 sites) has no resource identity schema in the pinned provider release
# and no admission-table row, even though its own import docs name
# security_group_id as its whole identity. See the script's own header for
# the full breakdown. Needs Docker, the AWS CLI, terraform on PATH, and
# network access for `terraform init` to resolve terraform-aws-modules/vpc
# from the registry (same as demo-corpus-vpc-complete); runs on its own
# port (4721).
demo-corpus-security-group-complete:
    bash live/e2e/corpus-security-group-complete/run.sh

# Build the docs site into site/public/. Wipes the directory first, so a
# page removed from the generator stops being served instead of lingering.
#
# Build the docs site into site/public/.
site:
    rm -rf site/public
    cd site && go run . -out public/

# Build the docs site and open it. `just site-serve 8001` picks another port,
# which is what you want when a second checkout or worktree is already serving.
#
# Build the docs site and serve it locally.
site-serve port="8000": site
    @echo "choudoufu docs: http://127.0.0.1:{{port}}/  (serving $(pwd)/site/public)"
    @if lsof -nP -iTCP:{{port}} -sTCP:LISTEN >/dev/null 2>&1; then \
        echo "port {{port}} is already in use - run: just site-serve 8001" >&2; exit 1; \
    fi
    @( sleep 1; command -v open >/dev/null 2>&1 && open "http://127.0.0.1:{{port}}/" ) &
    python3 -m http.server {{port}} --bind 127.0.0.1 --directory site/public

# Lint exactly as upstream CI would (golangci-lint, both GOOS passes)
lint:
    make golangci-lint

# The estate work plan: which estate to onboard next, and what blocks it.
#
# This is the assignment rule. Work is picked per ESTATE, fewest blockers
# first - never per refusal class. A day spent clearing classes moved 1570
# sites and zero estates, because the median blocked estate carries about two
# blockers and clearing one of them leaves it blocked.
estate-plan sweep="/tmp/choudoufu-sweep.json":
    go run ./tools/refusal-probe -schemas -out {{sweep}}
    go run ./tools/estate-plan -in {{sweep}} -schemas

# Re-plan from a sweep you already have (instant, vs ~2.5min to re-measure).
estate-plan-from sweep:
    go run ./tools/estate-plan -in {{sweep}}

# How much of the wall is the estate not having been onboarded.
#
# Everything else here measures the ADOPTION question - can a stranger's
# published configuration be taken over exactly as it stands - because every
# corpus entry is somebody else's published configuration and not one of the
# 250 declares a live block or a record_store. The primary goal is the other
# thing: someone writes ordinary Terraform, adds a live block, applies, and
# the fork manages it with no state file.
#
# This measures both forms of every entry in one sweep. internal/live/onboard
# computes the edit - a live sidecar declaring record_store "local", and the
# backend or cloud block removed - in memory, so nothing is written into
# .corpus, which is shared by every worktree.
#
# It is offline: check.Analyze over edited text, and nothing more. An estate
# reading "cleared by onboarding" has cleared the offline gate, not the real
# one; live/e2e is where "applies, loses its state file, replans empty" is
# still proved one estate at a time.
#
# ~3 min warm. -schemas is not optional: identity.LocatedType fails closed
# without them, so markerless-type reads as surviving onboarding when a
# record_store answers it.
#
# Both forms of every corpus entry: what onboarding clears, and what it does not.
onboarding-gap sweep="/tmp/choudoufu-onboarded.json":
    go run ./tools/refusal-probe -schemas -onboarded -quiet -out {{sweep}}

# Fetch the third-party corpus pinned in live/corpus-manifest.json into .corpus/
# (gitignored), and install each entry's registry modules into its own
# .terraform/modules. Needs network; run once.
#
# The module half is #254. internal/live/check.Load resolves a non-local module
# source through .terraform/modules, exactly as a real user's directory has it
# after init, and nothing here ever created that directory - so 58 of the 250
# entries were measured with a hole where their modules should be, and two
# refusal classes read as zero because the code that trips them was never
# loaded. Registry versions are frozen in live/corpus-module-pins.json so the
# corpus does not float with whatever a module author published today; commit
# that file whenever this run changes it. Go-getter sources (github.com/org/repo)
# are installed and then dropped, because 133 of the corpus's 134 such calls
# carry no ref and there is nothing to pin them to; -remote-modules keeps them
# and gives up reproducibility to do it.
corpus-fetch:
    go run ./tools/corpus-fetch

# The config-language scoreboard (#102): rank which refusals fire across the corpus, into live/corpus-refusals.json. Run corpus-fetch first. No cloud.
#
# The schema flags are not optional decoration. Without them every resource
# type absent from the generated admission table reads as refused, so
# unadmitted-type tops the ranking for a reason belonging to the run rather
# than to the corpus - the single outcome #102 exists to prevent. This recipe
# omitted them, so `just corpus` produced a worse artifact than the one
# committed, which is how a regeneration command silently stops reproducing
# its own output. Provider install needs network once; after that the plugin
# cache serves it.
#
# Rank which refusals fire across the corpus -> live/corpus-refusals.json.
corpus init_bin="terraform":
    go run ./tools/corpus-gen -init-bin {{init_bin}}

# ---------------------------------------------------------------------------
# The generation pipeline (#133). Stages in dependency order; each recipe's
# comment ends with the one-line summary `just --list` shows.
#
# Two rules these recipes exist to encode:
#   - Never pipe a generator into `head`. SIGPIPE kills it before it writes,
#     and the run looks exactly like one that produced no change.
#   - A regenerated artifact IS the measurement. Regenerate, then read the
#     diff; do not reason about what should have moved.
#
# `just tables` on a clean tree must produce no diff. If it does, either a
# recipe is wrong or an artifact was already stale - both worth finding.
#
# `tables` runs the DERIVED stages only. The six source stages - registry,
# importdocs, tagverbs, survey, logical-schemas and wo-sweep - fetch from
# upstream or need a running provider, so re-running one is a deliberate act
# with a pin bump behind it,
# not something a routine regeneration should trigger as a side effect.
# estate-gen is out for the same reason plus its own: it regenerates committed
# fixtures whose acceptance verdicts are a ratchet, and it carries a separate
# provider pin (#137).
# ---------------------------------------------------------------------------

# Regenerate every derived artifact, in dependency order (#133). No network.
tables: mapping row-emit convergence identity-sources survey-render limits harness
    @git status --porcelain || true

# CloudFormation Registry schemas -> live/registry.json + its embedded copy. Network on a cold cache.
registry:
    env -u PWD go run ./tools/registry-gen
    cp live/registry.json internal/live/registry/registry.json

# registry.json + overlay.json + overlay.d/*.json -> live/mapping.json + its embedded copy.
mapping:
    env -u PWD go run ./tools/mapping-gen
    cp live/mapping.json internal/live/registry/mapping.json

# Provider doc pages -> live/import-grammar.json. Offline: the doc cache is complete.
importdocs:
    env -u PWD go run ./tools/importdocs-gen

# AWS Service Authorization Reference -> live/iam-reference.json (#152). Network on a cold cache.
iamref:
    env -u PWD go run ./tools/iamref-gen

# botocore -> live/tag-verbs.json and reference.md's tagging-verb span.
tagverbs:
    env -u PWD go run ./tools/tagverbs-gen

# The record-store effects providers' own schemas -> live/logical-schemas.json,
# the evidence every RecordBacked row is derived from (see
# tools/row-gen/logicalschemas.go). A source stage like `survey`, not a derived
# one: it launches five providers, so it is out of `tables` for the same reason
# `survey` is. All five are small and cache like any other provider.
logical-schemas init_bin="terraform":
    env -u PWD go run ./tools/row-gen -logical-schemas -init-bin {{init_bin}}

# mapping + registry + import-grammar + logical-schemas + the ratified rows -> the two generated tables (a fixed point; see emit.go).
row-emit:
    env -u PWD go run ./tools/row-gen -emit

# hashicorp/aws's own schema, walked for WriteOnly and Sensitive+settable
# arguments -> live/wo-sweep.json, tools/limits-gen's source for the
# "Attribute-level residue" section's figures (#126). A source stage like
# `survey`: it launches the provider, so it is out of `tables`. The plugin
# cache serves it after the first run.
wo-sweep init_bin="terraform":
    env -u PWD go run ./tools/wo-sweep -init-bin {{init_bin}} > live/wo-sweep.json

# Measure the classifier against the shipped table -> rowgen-convergence.json. NOT a coverage metric.
convergence:
    env -u PWD go run ./tools/row-gen -convergence

# Provider schemas -> live/survey.json and live/survey-full.json. Needs the provider.
survey init_bin="terraform":
    env -u PWD go run ./tools/survey-gen -all -init-bin {{init_bin}}

# The committed surveys -> the rendered spans in SURVEY.md, LIMITATIONS.md, MARKERS.md and COVERAGE.md. No provider, no network.
survey-render:
    env -u PWD go run ./tools/survey-gen -render

# Where the sources describing each type's identity disagree (#106), into
# live/identity-sources.json. No provider, no network.
#
# Compare the sources describing each type's identity -> live/identity-sources.json.
identity-sources:
    go run ./tools/row-gen -sources

# live/LIMITATIONS.md's per-refusal content (#110), from the three refusal
# registries, the corpus artifact above, and live/wo-sweep.json's residue
# figures (#126). No provider, no network: all three inputs are committed.
#
# Render live/LIMITATIONS.md's per-refusal spans from the refusal registries.
limits:
    go run ./tools/limits-gen

# live/HARNESS.md's two registries: what the fork is driving down, and what it
# believes while it does. Runs every measurement and every assumption check, so
# a successful run is also a run in which the whole harness held. Last in
# `tables` because it reads what the other stages write. No provider, no
# network, well under a second.
#
# Render the burndown and assumptions registries -> live/HARNESS.md.
harness:
    go run ./tools/harness-gen

# Will this configuration work under live markers? (#114) DIR defaults to "."
live-check dir=".":
    go run ./cmd/choudoufu live-check {{dir}}
