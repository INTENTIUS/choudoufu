# SSM Record Store Parameter Type Ruling

Issue: https://github.com/INTENTIUS/choudoufu/issues/600

#548 stood an estate up from empty against the pinned emulator and read a
`random_password`'s generated value back out of the `ssm` record store with a
plain `ssm:GetParameter`. The parameter was `Type: String` with a null `KeyId`.
That is the default and it was not documented anywhere an operator would look,
which #600 splits into three parts: correct the docs, add gitignore guidance
for the `local` store, and decide whether `ssm` should be writing
`SecureString` instead.

The first two are done in the same change as this document
(`site/content/docs/use/storage.md`, `site/content/docs/use/start.md`). This
document settles the third.

## The ruling

**The `ssm` record store keeps `Type: String` as its default.** Encryption at
rest for the record store, if it is added at all, arrives as an opt-in
`record_store "ssm"` argument that names a KMS key, and defaulting that
argument is a separate decision from adding it.

Nothing about the default changes in the unit that writes this. What changes is
that the docs now say what the default means, per backend, on the page an
operator reads before choosing one.

## What the current default actually is

Read off the code rather than inferred:

- `internal/live/staterecord/ssm.go:160` (`PutIfAbsent`) and `:218`
  (`PutIfVersion`) both set `Type: types.ParameterTypeString`. Neither sets
  `KeyId`, and neither sets `Tier`.
- `SSMStore.Get` (`ssm.go:134`) calls `GetParameter` with `Name` only. There is
  no `WithDecryption`. Neither does the `GetParametersByPath` call in
  `SSMStore.List` (`ssm.go:289`).
- The value is base64-encoded before the write (`ssm.go:159`) and decoded on
  read (`ssm.go:144`). That is a transport convenience for an opaque payload,
  not a protection.
- The `record_store` block's schema (`internal/configs/live.go`, attributes at
  lines 485-488) accepts `path`, `bucket`, `key_prefix` and `region`. There is
  no argument that would let one estate opt into encryption and another out.

So `SecureString` today would be unconditional for every `ssm` estate, and it
would arrive as a behaviour change to configurations that never asked for one.

## Why not flip the default

**It is not a one-line change, and the one line is the dangerous version.**
Changing only the two `Type:` fields leaves `Get` asking for a `SecureString`
without `WithDecryption`, so Parameter Store returns the KMS ciphertext, the
base64 decode at `ssm.go:144` gets something that is not the payload, and every
read of every newly written record fails or corrupts. A correct flip is a write
change, a read change on two call sites, and an IAM change, at minimum.

**It adds a billed dependency to the data path that the operator did not ask
for.** Every record write becomes a KMS `GenerateDataKey` and every record read
a KMS `Decrypt`. KMS bills per request, so the cost scales with records times
runs rather than with estate size, and it lands on an account whose owner
agreed to Parameter Store and nothing else. A KMS request is also a new way for
a plan to fail: a key policy that does not cover the run's principal, a
throttle, or a regional KMS problem all become plan failures on an estate that
was working.

**It changes the permissions a working estate needs.** The documented
permission row for this backend is `ssm:GetParameter`, `ssm:PutParameter`,
`ssm:DeleteParameter`, `ssm:GetParametersByPath`
(`site/content/docs/use/reference.md:368`). `SecureString` adds `kms:Decrypt`
on read and `kms:GenerateDataKey` on write. Any role that was scoped to exactly
the documented list breaks on upgrade.

**The protection it buys is narrower than it sounds.** With the account's
default `aws/ssm` key, the reader it stops is a principal holding
`ssm:GetParameter` on the path but not `kms:Decrypt`. That is a real
separation and it is worth having for some accounts. It is not the separation
most readers assume, which is that the value stops being recoverable from
Parameter Store. Nothing here has measured that boundary against real AWS; the
emulator authorizes everything, so it cannot.

**A stronger answer already exists and is already the documented one.**
`strict { secrets = "refuse" }` refuses the secret-generating types outright,
so the store never receives the material at all, and it can be pinned from the
environment with `CHOUDOUFU_STRICT_PIN=1` so relaxing it and approving the
relaxation cannot be the same change. An operator who must not have secrets at
rest is better served by having nothing at rest than by having something
encrypted with an account-default key.

## What a flip would owe existing estates

Recorded here because #600 asks for it explicitly, and because it is the part
that is easy to skip.

Nothing in this codebase rewrites records it was not asked to write. Records
are written per instance, when that instance's value changes
(`internal/live/projection/record.go`, `rootoutput.go`, `hint_store.go`, all
through `Store.PutIfVersion` / `PutIfAbsent`). There is no migration pass and
no sweep that would revisit a store.

So a default flip does not re-encrypt anything. Every parameter already written
stays exactly as it is, in whatever form it was written, until something
happens to rewrite that particular record, and some records are never rewritten
for the life of an estate. An estate that has been running for a year on `ssm`
would end up with a mixture, and an operator reading "the store is encrypted
now" would be wrong about most of it.

Any future change to this default therefore ships with:

1. A release note saying plainly that existing parameters are unchanged and
   that the store holds a mixture until every record is rewritten.
2. A stated path for an operator who wants the old records gone rather than
   mixed. Deleting the store's parameters and letting the estate rebuild them
   is churn (the effect re-runs, the value regenerates) and is a real answer;
   it needs to be written down as one rather than left as an exercise.
3. The permission table in `site/content/docs/use/reference.md` updated in the
   same change, since the IAM requirement moves.

## The recommendation, if this is revisited

Add `kms_key_id` to the `record_store "ssm"` block. Present means
`SecureString` with that key and `WithDecryption` on both read paths; absent
keeps `String`. That gets encryption at rest to the operators who want it,
prices it to the estates that opted in, keeps the permission story per estate
rather than global, and leaves the default alone so no running estate acquires
a KMS dependency on upgrade.

Whether the argument should later default to the account's `aws/ssm` key is a
separate question and should be asked separately, with the mixture problem
above answered first.

## What was not verified

- Anything about KMS behaviour, key policies, or `kms:Decrypt` denial. The
  pinned emulator authorizes everything, so no run here can distinguish a
  principal that holds `kms:Decrypt` from one that does not.
- KMS per-request pricing. The shape of the cost is stated above; the numbers
  are AWS's and are not quoted here.
- Whether Parameter Store permits changing an existing parameter's type on
  overwrite. It does not matter for this ruling, because no code path here
  rewrites a record it was not separately asked to write.
