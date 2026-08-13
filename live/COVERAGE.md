# AWS Provider Coverage

choudoufu supports one provider today: AWS. This page is the ledger of
how much of the AWS provider live markers cover, layer by layer: what is
proven now, what is generated and waiting on ratification, what needs one
small decision per type, and the small residue that will never map, each
entry with its reason.

The usage-weighted summary comes first, because raw percentages
undersell it. The services estates are actually made of — EC2/VPC, S3,
IAM, Lambda, RDS, DynamoDB, SQS/SNS, EKS/ECS, ELB, Route53, KMS,
CloudWatch — are all either live-proven, in the generated set awaiting
ratification, or in an alias family being converted right now. The tail
that will never map is disproportionately dead or exotic services, and
every type in it gets a named, one-sentence answer in
`live/LIMITATIONS.md` and in the lint refusal itself.

## The layers at a glance

| Layer                                  | Types | What stands between it and support            |
| -------------------------------------- | ----- | --------------------------------------------- |
| Live-proven on `main`                   | 37    | Nothing. Exercised end to end.                 |
| Generated, ratifiable today             | 551   | A ratification batch: paste, fixture, test.    |
| Behind small, known hand-work           | 240   | One one-line decision per type.                |
| Alias families (conversion in flight)   | ~576  | A service-alias line per family.               |
| Genuine residue                         | —     | Will not map; named reason per type.           |

## Live-proven: 37 types

These are exercised end to end by the demo estate on `main`: VPC, subnet,
and security-group networking, S3 and its children, the IAM role trio,
DynamoDB, KMS, Route53, the ALB stack, SNS, ACM, and so on. The Lambda
pilot batch in flight adds the first ratified cohort on top.

## Generated and ratifiable today: 551 types

The generation approach produced 551 further types: 501 with
server-assigned identifiers and 50 client-named, each printed with its
registry evidence. They wait only on ratification batches (paste,
fixture, test). This is real generated coverage; it has not been stamped
through the human gate yet, by design. Nothing here ships without a
fixture and a test.

## Behind small, known hand-work: 240 types

- 114 composite types each need a one-character import separator chosen.
- 126 need an identity-argument name confirmed.

Bounded, per-type, one-line decisions.

## The "unmapped" 900: mostly a naming problem

Of the 900 types the v1 generator left unmapped, measured composition
shows 576 sit in services CloudFormation absolutely covers. The join
failed on naming, because Terraform files things under `vpc_`,
`cloudwatch_`, and `db_` while CloudFormation files them under `EC2`,
`Logs`, and `RDS`.

The v1 generator shipped with exactly 31 aliases, the minimum the curated
68 types needed, because the alternative — fuzzy cross-service matching —
produces confidently wrong garbage. A prototype demonstrated it by
mapping `aws_appsync_type` to `AWS::Cassandra::Type`. Wrong mappings here
touch live infrastructure, so v1 chose "unmapped" over "wrong."

Unmapped is cheap to fix in a way wrong is not. One service-alias line
("`vpc_` means `EC2`") converts an entire 38-type family at once. That
work is in flight now: service-scoped matching with a service-alias table
and a false-positive guard, expected to move the mapped count from 791
toward roughly 1,200–1,350. That puts the generated-proposal set on track
for about 70–80% of the provider.

## The residue that will never map, and why that is fine

After aliases, the genuine residue is:

- 76 registry types in dead services (Pinpoint, Greengrass V1, WAF
  classic, and their kin).
- Terraform-only property-children and waiters, which need no mapping of
  their own because their identity is their parent's.
- Types CloudFormation does not model, such as `aws_s3_object`.
- 51 registry-laggard types, where the provider's own schemas are the
  fallback.

Every one of these gets a named, one-sentence answer in
`live/LIMITATIONS.md` and in the lint refusal itself. "Not covered"
always arrives with its reason, never as a silent gap.

## Other providers

AWS is the only provider today. Azure and Google Cloud are coming, and
appear greyed out in the docs navigation until they land.
