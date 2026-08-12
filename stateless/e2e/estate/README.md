# P0.1 estate fixture

A Terraform/OpenTofu project against floci, standing in for a real estate
that later phases plan and apply without a state file. Every taggable
resource carries the marker tags this whole branch depends on:
`tofu-estate = "stateless-e2e"` and `tofu-address = "<its own address>"`.
No modules, no remote state (no `backend` block — the local statefile this
fixture's own `terraform apply` writes is scaffolding for verification, not
a feature under test), no provisioners, no logical resources.

## Coverage map

| Coverage row | Resource block(s) | Why it lands there |
|---|---|---|
| Marker path (server IDs) | `aws_vpc.main`, `aws_subnet.this`, `aws_security_group.main`, `aws_kms_key.main` | Identity is a server-assigned ID (`vpc-…`, `subnet-…`, `sg-…`, a KMS key UUID) not derivable from config; recovered only by the `tofu-address` tag. |
| Marker path, non-`id` identity attribute | `aws_route53_zone.main`, `aws_lb.main`, `aws_lb_target_group.app`, `aws_lb_listener.app` | Same path, but the identity is not the `id` attribute: the zone's identity schema names `zone_id`, and every ELBv2 object's names `arn`. The identity table has to say which attribute carries the import ID, and a list result only ever carries that one. |
| Client-named path | `aws_s3_bucket.data`, `aws_iam_role.app`, `aws_cloudwatch_log_group.app`, `aws_dynamodb_table.events` | Identity is a name already in config (bucket name, role name, log group name, table name) — nothing to recover. |
| Client-named path, ARN-shaped id | `aws_ecs_cluster.app` | The documented import ID is the cluster name, already in config, but the provider sets `id` to the cluster ARN — so only the `name` attribute may hand out the identity, the same care `aws_route`'s synthesized id gets. |
| Named singleton child | `aws_s3_bucket_policy.data` | Exactly one policy per bucket; its own identity is the parent bucket's name, so it needs no marker of its own (and the type carries no `tags` argument to put one on). |
| Parent-derived | `aws_route.internet_gateway`, `aws_route_table_association.this`, `aws_lb_target_group_attachment.app` | Identity is a composite of admitted parents: a route is (route table, destination CIDR); an association is (subnet, route table); an attachment is (target group ARN, target, port). None of the three accepts a `tags` argument. |
| Fungible count | `aws_eip.pool` (`count = 3`) | Three interchangeable elastic IPs; no identity-bearing property distinguishes one slot from another, which is the shape phase 3's slot-marker matcher is built for. |
| Conditional idiom | `aws_cloudwatch_log_group.optional` (`count = var.enabled ? 1 : 0`) | The `enabled`-gated single-instance idiom the roadmap calls out as surviving unchanged. |
| for_each stable keys | `aws_subnet.this` (`for_each = local.subnets`, keyed `"a"`, `"b"`) | Same block as the marker-path row above — its `tofu-address` carries the full keyed address (`aws_subnet.this:a (escaped per stateless/MARKERS.md)`), which is what phase 3's exact for_each binding keys off. |
| Attachment composite | `aws_iam_role_policy_attachment.app` | Identity is the (role name, policy ARN) pair, both already client-named in config; the type carries no `tags` argument. |
| Account-derived | `aws_sqs_queue.jobs`, `aws_sns_topic.alerts` | The name is in config and the provider imports by a URL or an ARN built around it, so the identity needs the run's account and region as well (`stateless/SURVEY.md` flags F1 and F2). With no `CloudContext` supplied — which is every run today — both fall back to the marker path, which is what this fixture exercises live. |
| Receipt, existence flavor (effect memory, default) | `aws_ssm_parameter.demo_existence` | A plain client-named resource whose value is the constant `"done"` — existence is the entire bit for a run-once effect. Proves the receipts pattern's default recommendation (`stateless/RECEIPTS.md`, "Two flavors; prefer the simpler"): deleting the parameter out of band and watching the next plan re-propose exactly its create is the whole demonstration. |
| Receipt, hash flavor (effect memory, run-on-change) | `aws_ssm_parameter.demo_effect` | A plain client-named resource whose value is `sha256()` over a local of declared inputs — never the inputs themselves. Proves the receipts pattern for effects that must re-run when their inputs change. Both receipts share the leaf rule: no receipt-specific tooling, nothing else in the estate depends on either. |

## Supporting, not coverage

`aws_route_table.main` and `aws_internet_gateway.main` exist only so
`aws_route.internet_gateway` and `aws_route_table_association.this` have a
parent and a target to point at. Both are themselves marker-path-shaped
(server-assigned IDs, tagged the same way as the VPC/subnet/security group)
but neither is named in the coverage table, so they aren't claimed as a row
here — they'd be admitted the same way `aws_vpc` is, incidentally.

## Untaggable types

Five types in the table above carry no `tags` argument in the AWS provider:
`aws_s3_bucket_policy`, `aws_route`, `aws_route_table_association`,
`aws_iam_role_policy_attachment`, `aws_lb_target_group_attachment`. Each is
commented in its resource block.
This is expected, not a gap — their identity comes from the client-named or
parent-derived path, not from a marker, so admission doesn't need a tag.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks. Endpoint, access key, secret key and region all come from `AWS_ENDPOINT_URL` / `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_REGION` (the recent AWS provider honors `AWS_ENDPOINT_URL` as a global override); the provider block carries only the flags with no env-var form: `skip_credentials_validation`, `skip_metadata_api_check`, `s3_use_path_style`. Provider pinned to `hashicorp/aws = 6.58.0`. |
| `variables.tf` | `enabled` (bool, default `true`). |
| `locals.tf` | `estate_tag` (the marker's estate value) and the `subnets` map the for_each keys off. |
| `network.tf` | VPC, subnets, security group, route table, internet gateway, route, route table associations. |
| `storage.tf` | S3 bucket and bucket policy. |
| `iam.tf` | IAM role and its policy attachment. |
| `logs.tf` | The two CloudWatch log groups (client-named, conditional). |
| `compute.tf` | The three-EIP fungible-count pool. |
| `database.tf` | The DynamoDB table (client-named, #19's first slice). |
| `containers.tf` | The ECS cluster (client-named with an ARN-shaped `id`, same slice). |
| `keys.tf` | The KMS key (marker path, #20's first slice). |
| `dns.tf` | The Route 53 hosted zone (marker path with a `zone_id` identity attribute, same slice). |
| `balancers.tf` | The ELBv2 chain: load balancer, target group and listener (marker path, #20's second slice), plus the target group attachment (parent-derived, #21). |
| `messaging.tf` | The SQS queue and the SNS topic (account-derived, #21). |
| `receipts.tf` | The receipt demo, both flavors (`aws_ssm_parameter.demo_existence`, `aws_ssm_parameter.demo_effect`). See `stateless/RECEIPTS.md`. |

## Verifying by hand

```
docker run -d --rm -p 4602:4566 --name tofu-stateless-p01-verify floci/floci:latest
export AWS_ENDPOINT_URL=http://localhost:4602 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

aws --endpoint-url "$AWS_ENDPOINT_URL" ec2 describe-vpcs \
  --filters Name=tag:tofu-address,Values=aws_vpc.main

terraform destroy -auto-approve
docker rm -f tofu-stateless-p01-verify
```
