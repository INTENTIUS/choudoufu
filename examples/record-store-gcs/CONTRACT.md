# What a record store GCS bucket must have

choudoufu asserts four settings about the bucket behind `record_store "gcs"`
and refuses a bucket that fails one, and warns about a fifth. The first three
are [the S3 bucket's three](../record-store-bucket/CONTRACT.md) in GCS terms,
for the same reasons. Each has its own name so a waiver says which store it is
about.

| Setting | What is asserted | What it protects against |
|---|---|---|
| `object_versioning` | Object Versioning is enabled | A record can be the only copy of what it says. Without versioning an overwrite or a delete is final |
| `noncurrent_lifecycle` | A `Delete` rule removes **noncurrent** versions under every prefix an estate writes, and no `Delete` rule can match a **live** object there | With versioning on, every apply adds versions. Without the rule the bucket keeps all of them, and nobody has chosen how long a deleted record stays recoverable |
| `public_access_prevention` | Public access prevention is `enforced` on the bucket | Records hold secret material. A grant to `allUsers` or `allAuthenticatedUsers` would publish them |
| `uniform_bucket_level_access` | Uniform bucket-level access is enabled | Without it, object ACLs grant access the bucket's IAM policy does not show, and GCS refuses IAM conditions on the bucket, so [the grant](iam/render-grant.sh) cannot be applied |
| `list_isolation` | No other estate's keys are in the bucket. A warning, never a refusal | GCS cannot fence a listing by prefix, so every identity that may list one estate's keys lists all of them. See [One bucket per estate](../../live/STORAGE.md#one-bucket-per-estate) |

The first four come from one read, `storage.buckets.get`. An identity without
it gets all four reported unreadable, which refuses the same as a failure.
`list_isolation` comes from one listing of the bucket's sentinels, by name
wherever an estate's `key_prefix` put them; an identity that may not list
gets `not_checked`.

Encryption at rest is not asserted, for the S3 contract's reason: every GCS
object is encrypted, so the check could not fail. Soft delete is not asserted
either. It is a second recovery window GCS keeps by itself, and the store
does not depend on it.

## What counts as a noncurrent rule

GCS has no separate noncurrent action. A rule is a `Delete` action with
conditions, and four conditions match only a noncurrent version:
`isLive: false`, `daysSinceNoncurrentTime`, `numNewerVersions` and
`noncurrentTimeBefore`. A `Delete` rule carrying one of them counts.

- It needs a window. `isLive: false` alone deletes a version the moment it
  stops being live, which leaves nothing to recover, and does not count.
  Add `daysSinceNoncurrentTime` or `age`.
- `matchesPrefix` counts for the prefixes under it. One rule with no prefix
  covers every estate sharing the bucket.
- A rule filtered by suffix, storage class, custom time or creation date
  does not count.

A `Delete` rule with none of the noncurrent conditions can match a live
object, so it deletes records on a timer. That refuses the bucket under the
headline `The record store bucket's lifecycle deletes records`, and
`allow_insecure` does not waive it. A rule whose `matchesPrefix` lies outside
every prefix the estate writes is not about the estate and is left alone.

A rule that works:

```json
{"rule": [{"action": {"type": "Delete"}, "condition": {"daysSinceNoncurrentTime": 30}}]}
```

The thirty is a recovery window. Choose it as the answer to "how long until
we would notice", as the S3 page says.

## When it is checked

When the S3 bucket's are: an estate's first contact with the bucket, before
every run that writes a record, and on demand. `choudoufu live-bucket` in a
directory whose live block declares `record_store "gcs"` reports the five. A
`list_isolation` warning leaves its verdict correct. Its `-bucket` flag names
S3 buckets only.

## Waiving a setting

```hcl
record_store "gcs" {
  bucket         = "prod-records"
  allow_insecure = ["noncurrent_lifecycle"]
}
```

| Waived | The cost |
|---|---|
| `object_versioning` | An overwritten or deleted record cannot be brought back, and a record can be the only copy of what it says |
| `noncurrent_lifecycle` | Nothing is known to delete noncurrent versions: the bucket may keep every version of every record forever, and nobody has chosen how long a record destroyed by mistake stays recoverable. It does not waive a rule that deletes records |
| `public_access_prevention` | Nothing is known to stop an IAM grant to `allUsers` or `allAuthenticatedUsers` from publishing the records |
| `uniform_bucket_level_access` | Object ACLs may grant access the bucket's IAM policy does not show, and the per-estate grant's IAM conditions cannot be set on the bucket |
| `list_isolation` | This estate's identity may list every other estate's keys in the bucket, and nothing says so on the run |

Every run that opens the store repeats the warning. An S3 name such as
`versioning` on a gcs store is a configuration error.

## A correct bucket

```
gcloud storage buckets create gs://prod-records --location=<region> \
  --uniform-bucket-level-access --public-access-prevention
gcloud storage buckets update gs://prod-records --versioning
gcloud storage buckets update gs://prod-records --lifecycle-file=lifecycle.json
```

with `lifecycle.json` holding the rule above. One bucket per estate is the
default.

## The grant

`iam/render-grant.sh <estate> <bucket> <member>` prints the bindings for one
estate's identity, and `iam/example-prod.json` is its output for `prod`.
`live/STORAGE.md` says what the grant fences and what it does not.

Nothing on this page has been run against real GCS or an emulator yet. The
store and its checks are tested against a fake of the JSON API.
