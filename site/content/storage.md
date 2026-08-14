# Where things are stored

choudoufu writes in three places, and two of them can both end up as SSM
parameters, which is why they get confused. They do different jobs and have
different owners.

| What | Where it lives | Who reads it | Losing it costs |
|---|---|---|---|
| Ownership markers | Two tags on the resource itself | choudoufu, and you, with any cloud tool | The resource goes invisible and the next plan proposes a duplicate |
| Micro-state records | A `record_store` you declare: local dir, SSM, or S3 | choudoufu only | Churn: the effect re-runs or its value regenerates |
| Receipts | Ordinary resources *you* declare, by convention SSM parameters | You, your reviewers, your incident responder | Nothing structural. It is your data, in your configuration |

The first is the product. The second is plumbing you turn on when you need it.
The third is a pattern you write yourself, and choudoufu only lints it.

## Ownership markers

Two tags, `tofu-estate` and `tofu-address`, written onto each resource as it is
created. Every plan rebuilds prior state by reading them back off the live
system.

This is the only one of the three that is authoritative about *what you own*,
and it deliberately lives on your resources in your account rather than in
anything choudoufu keeps. `live/MARKERS.md` is the normative spec and the
surface external tooling can rely on.

Nothing else on this page is required. An estate of ordinary cloud resources
needs markers and nothing more.

## The record store

Some resources have no cloud twin to read back. A `null_resource` that ran a
script, a `time_static` that captured a timestamp, a `random_pet` that
generated a name: nothing in AWS knows these happened, so there is no marker to
recover them from.

Those persist as **micro-state**: one small record per resource. Declare a
store and they are admitted; without one they are refused.

```hcl
# estate.chdf.hcl
estate = "my-estate"

record_store "ssm" {}
```

The same block goes inside `live` if the configuration uses the in-`terraform`
form instead of the sidecar file. The label picks the backend.

| Backend | Where it writes | Arguments |
|---|---|---|
| `local` | A directory beside the module, `.tofu-records` by default | `path` |
| `ssm` | SSM Parameter Store, under a prefix derived from the estate name | `key_prefix`, `region` |
| `s3` | An S3 bucket you already own | `bucket` (required), `key_prefix`, `region` |

Three things about it are worth knowing before you turn it on.

**You are not meant to read it.** The payload is a self-describing ctyjson
envelope, readable by this fork's own code. It is not an operator-facing
artifact, and nothing about its format is a contract.

**There is no lock.** Writes are conditional: a record is written only if it
still carries the version the writer read. A losing writer gets a named
failure, not a blocking wait and not a silent overwrite.

**Losing a record is churn, not a lost estate.** The effect re-runs or its
value regenerates, and anything reading that value plans as a change. It cannot
cost you a resource, because a record-backed value is a resource attribute and
identity arguments have to be statically evaluable, so such a value can never
be the thing that names a resource.

**Secrets never go here.** `random_password`, `random_bytes` and every `tls_*`
are refused rather than recorded. Their output is material only a state file
ever remembered, and a record holding a secret is a state file with extra
steps.

## Receipts

A receipt answers a different question: not "what was this resource's last
value" but "did this external effect actually run, and with what input".

A receipt is not choudoufu storage. It is an ordinary resource you declare,
which by convention is an SSM parameter at
`/tofu-receipts/<estate>/<effect>` holding a hash. It goes through the
ordinary plan and apply cycle, and its diff appearing in a plan is the point:
that diff is what tells a reviewer or a CI gate that this apply is about to
trigger something with consequences outside the resources being managed.

choudoufu does not write receipts. It lints them, enforcing that the value is a
hash or a constant and never a `SecureString`, that nothing references a
receipt's attributes, and that inputs reference secrets by pointer rather than
by value.

`live/RECEIPTS.md` has the pattern and the reasoning behind each guard.

## Why receipts are not record-store entries

This is the distinction the rest of the page exists to set up, and it is
enforced rather than advised: a `key_prefix` whose first segment is literally
`tofu-receipts` is a configuration error, so a record can never be written into
the receipts namespace.

The reason is visibility. A receipt is deliberately AWS-shaped so its value
stays readable with a plain `aws ssm get-parameter`, by a person with read-only
IAM access and no `choudoufu` binary at all, at three in the morning. A
record-store payload is tool-internal by design. Moving a receipt onto it would
trade `aws ssm get-parameter` for "read choudoufu's internal JSON envelope",
which is strictly worse for the one artifact whose entire job is being legible
to someone who is not running the tool.

**The tempting mistake**, now that `terraform_data` is record-backed, is to use
its `triggers_replace` as a pseudo-receipt. Do not. It hides the fingerprint
inside the tool's own store instead of an ordinary declared resource, and it
collapses a receipt's semantics into "did an input change", with no existence
flavour, no hash flavour, and no naming convention for the lint rules to
recognise.

`terraform_data` is for the graph: ordering an apply, feeding
`replace_triggered_by`, standing in for a resource that does nothing. Receipts
are for external effects. Keep them apart.

## Choosing a record store backend

`local` for a single operator or a demo, where a directory beside the module is
fine and nothing else needs to read it.

`ssm` when more than one machine runs the estate. Zero infrastructure to set
up, since Parameter Store already exists in the account.

`s3` when you want the records in a bucket you already operate, with your own
versioning and lifecycle rules. You create and configure the bucket; choudoufu
only reads and writes keys in it.
