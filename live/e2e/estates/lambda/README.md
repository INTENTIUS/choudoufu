# lambda cohort

Cohort: `lambda`. Ratified by: the first registry-backed ratification batch
against #40's strategy and #44's row generator. Mechanism: #48 (see
`live/e2e/estates/example/README.md` for the proof-of-mechanism cohort this
one follows).

This is the first cohort with real resources: it exercises every type
`tools/row-gen`'s Lambda batch proposed and this batch ratified into
`internal/live/lint/admission.go` and `internal/live/identity/table.go`.

## Coverage map

| Coverage row | Resource block | Why it lands there |
|---|---|---|
| Client-named path | `aws_lambda_capacity_provider.app` | Identity is the `name` argument, already in config — confirmed against the provider's own identity schema (`live/survey-full.json`), not only the registry. |
| Marker path | `aws_lambda_code_signing_config.app` | Lambda mints the config's ARN at create time; the type has no name argument at all for a wrong guess to reach for. |
| Client-named path | `aws_lambda_function.app` | Identity is the `function_name` argument, already in config — confirmed against the provider's own identity schema, same as the capacity provider. |
| Marker path | `aws_lambda_event_source_mapping.app` | Lambda mints the mapping's UUID at create time; `event_source_arn` names what the mapping reads from, not the mapping itself. |
| Marker path, untaggable | `aws_lambda_layer_version.app` | Lambda mints the layer version's ARN, embedding a version number it assigns and increments itself; `layer_name` names the family, not one immutable version. Carries no `tags` argument — see "Untaggable types" below. |

## Rejected, and deliberately absent

`tools/row-gen`'s Lambda batch proposed eight types. Two are here:

- `aws_lambda_permission` — composite `primaryIdentifier` in the registry
  (`FunctionName`, `Id`); row-gen correctly refused to guess the join
  separator and put it in "needs hand separator". Not ratified, not
  rejected — just not this batch's to decide.

Two more were proposed as pastable server-assigned rows and rejected on
independent verification against the provider's own documented import
behavior, not against the registry:

- `aws_lambda_alias` — the registry's `AliasArn` is read-only in
  CloudFormation's model, which is what led row-gen to classify it
  server-assigned. The provider disagrees: `aws_lambda_alias` has no
  identity schema in v6.58.0, and its documented import ID is
  `function_name/alias_name`, a composite of two arguments already in
  configuration. `AliasArn` is CloudFormation's own read-only projection of
  those same two arguments, not a value this provider imports by.
- `aws_lambda_layer_version_permission` — same failure shape. The
  registry's opaque `Id` reads as server-assigned, but the provider's
  documented import ID is `layer-arn,version-number`, again a composite the
  configuration already supplies.

Both would need a hand-chosen composite separator, which is exactly what
`tools/row-gen`'s own rules refuse to guess (issue #44); this batch chooses
not to write one for either without a config-signal check first, so both
stay out of `admittedTypesV0`, `DefaultTable`, and this fixture.

## Untaggable types

One type in the table above, `aws_lambda_layer_version`, carries no `tags`
argument in the AWS provider — the same shape as the twelve untaggable
types `live/e2e/estate/README.md` names, and it is commented as such in
`lambda.tf`. It is **not** added to `live/LIMITATIONS.md`'s "Untaggable
types cannot be removed by the sweep" entry, on purpose:
`tools/survey-gen/limitations_test.go`'s `TestLimitationsDocAgainstSurvey`
derives that entry's roster from `live/survey.json`, the curated 68-type
roster, intersected with the admission table — not from
`live/survey-full.json`, the 1,691-type registry-backed roster this batch's
evidence came from. `aws_lambda_layer_version` is outside the curated 68
(only `aws_lambda_function` and `aws_lambda_permission` are in it), so it
never enters that test's `derived` set, and adding it to the doc's
untaggable paragraph would make the test fail — the reverse of what
editing it is supposed to accomplish. The behavior is real and worth
knowing (`aws_lambda_layer_version` genuinely cannot be swept), but
documenting it correctly needs the pin test extended to the full registry
roster first. Left as follow-up.

## Files

| File | Contents |
|---|---|
| `versions.tf` | `terraform`/`provider "aws"` blocks, identical in shape to `live/e2e/estate/versions.tf`. |
| `locals.tf` | `estate_tag` — `"lambda-cohort"`, distinct from the demo estate's `"stateless-e2e"`. |
| `iam.tf` | `aws_iam_role.lambda`, supporting infrastructure for the function's execution role and the capacity provider's operator role — not a coverage row; `aws_iam_role` is already covered by `live/e2e/estate/`. |
| `lambda.tf` | The five ratified types. |

## Gating

Nothing here runs against a live or emulated cloud yet. `go test
./internal/live/lint/... ./internal/live/identity/...` picks this directory
up through `internal/live/flocitest.FixtureDirs` (#48's union pin) and
checks it by static HCL parse — `TestAdmissionTableCoversEstate` and
`TestTableCoversFixtureTypes` require `admittedTypesV0` and `DefaultTable`
to cover exactly the union of `live/e2e/estate/` and every `estates/*`
cohort, this one included.

`live/e2e/run.sh` and the gated `internal/live/discovery` floci integration
test still stand up only the demo estate (confirmed by inspection: neither
mentions `estates/` or "cohort"). Wiring cohort estates into the gated
apply-against-floci tier is separate follow-on work, the same note
`estates/example/README.md` already makes.

## Verifying by hand

```
docker run -d --rm -p 4603:4566 --name tofu-lambda-cohort-verify floci/floci:latest
export AWS_ENDPOINT_URL=http://localhost:4603 AWS_ACCESS_KEY_ID=test \
       AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

terraform init
terraform validate
terraform apply -auto-approve

terraform destroy -auto-approve
docker rm -f tofu-lambda-cohort-verify
```

`terraform validate` passes against the real provider release (6.58.0) this
fixture pins — confirming every argument name above is real, not just
registry-plausible. An `apply` against `floci/floci:latest` was run by hand
during ratification (not wired into any automated tier — see "Gating"
above) and found the emulator's actual coverage narrower than its health
check advertises:

- `aws_lambda_function.app`, `aws_lambda_event_source_mapping.app` and the
  supporting `aws_iam_role.lambda` create and destroy cleanly.
- `aws_lambda_capacity_provider.app` and `aws_lambda_code_signing_config.app`
  fail to create: floci's `/_localstack/health` reports `lambda: running`,
  but `CreateCapacityProvider` and `CreateCodeSigningConfig` both return an
  HTML error body where the SDK expects JSON — the operations are not
  actually implemented, only the service's presence is. This is a floci
  gap (`choudoufu#26` territory), not evidence against either type's
  admission: identity and enumeration are properties of the provider and
  the registry, not of one emulator's completeness.
- `aws_lambda_layer_version.app` fails for an honest reason instead: its
  `s3_bucket`/`s3_key` are placeholders (see `lambda.tf`'s comment), and
  floci correctly reports that the bucket does not exist. A real
  verification run would need a real `aws_s3_object` uploaded first, which
  this fixture deliberately does not add.

## Supporting IAM resources, relocated from the hand-written iam.tf

The comment below traveled with a hand-written iam.tf that #108 criterion 4
folded into the generator (the supporting resources are now emitted by
estate-gen itself - see GENERATED.md's provenance table). Kept verbatim as
ratification evidence:

> Supporting, not coverage: aws_iam_role.lambda exists only so
> aws_lambda_function.app and aws_lambda_capacity_provider.app have a role
> to assume/operate under. It is itself client-named-shaped exactly the way
> live/e2e/estate/'s own aws_iam_role.app is, but it is not claimed as a
> coverage row here — see live/e2e/estate/README.md's own "Supporting, not
> coverage" section for the same pattern, and aws_iam_role is already
> covered there.

## Per-resource evidence comments, relocated from the pre-fold files

These comment blocks traveled with the hand-maintained .tf files before
#108 criterion 4 made this cohort generator-emitted. The regeneration
replaces them with generated provenance headers; the originals are kept
here verbatim as ratification evidence.

From `lambda.tf`:

> Coverage: the registry-ratified Lambda batch (#40's registry-backed
> admission strategy, #44's row-gen tool). Every resource below is one of
> the five types this batch ratified into admittedTypesV0
> (internal/live/lint/admission.go) and DefaultTable
> (internal/live/identity/table.go) — see table.go's "Registry-ratified"
> section comment for the per-type evidence, and for the two row-gen
> proposals (aws_lambda_alias, aws_lambda_layer_version_permission) this
> batch rejected and left out of both tables.

From `lambda.tf`:

> Coverage: client-named path (aws_lambda_capacity_provider — identity is
> the name argument, already in config; confirmed against the provider's
> own identity schema, live/survey-full.json). Literal placeholder
> subnet/security-group IDs rather than real aws_subnet/aws_security_group
> resources: this cohort exercises Lambda identity admission, not EC2
> networking, and both types are already covered by live/e2e/estate/.

From `lambda.tf`:

> Coverage: marker path (aws_lambda_code_signing_config — Lambda mints the
> config's ARN at create time; the type has no name argument for a wrong
> guess to reach for). The signing profile is a literal placeholder ARN
> rather than a real aws_signer_signing_profile resource, which is outside
> this batch's scope.

From `lambda.tf`:

> Coverage: client-named path (aws_lambda_function — identity is the
> function_name argument, already in config; confirmed against the
> provider's own identity schema). Image-packaged so the fixture needs no
> local zip artifact or S3 object.

From `lambda.tf`:

> Coverage: marker path (aws_lambda_event_source_mapping — Lambda mints
> the mapping's UUID at create time; the event_source_arn below names what
> it reads from, not the mapping itself). The source is a literal stream
> ARN rather than a real aws_dynamodb_table resource with streaming
> enabled — the same "keep the block out of the emulator's boundary"
> choice live/e2e/estate/iam.tf makes for its inline policy's bucket ARN.
> DynamoDB Streams is not what this cohort is testing.

From `lambda.tf`:

> Coverage: marker path (aws_lambda_layer_version — untaggable; carries no
> tags argument in the provider, so it is not swept for removal like the
> twelve untaggable types live/e2e/estate/README.md names — see this
> cohort's own README, "Untaggable types", for why that entry cannot land
> in live/LIMITATIONS.md yet. Lambda mints the layer version's ARN,
> embedding a version number it assigns and increments itself; layer_name
> names the family, not one immutable version of it). s3_bucket/s3_key are
> literal placeholders, the same choice the capacity provider's signing
> profile ARN makes, rather than a real aws_s3_object this batch has no
> reason to admit.

From `locals.tf`:

> The marker's estate value (live/MARKERS.md, P0.3), distinct from the
> demo estate's "stateless-e2e" so the two cohorts never collide if ever
> applied against the same account side by side.

From `versions.tf`:

> Lambda cohort — provider wiring only, identical to live/e2e/estate/'s own
> versions.tf. See that file's comment for why the provider block carries
> only the flags with no environment-variable form.
