# The `strict` block, toggle by toggle

The principles this fork exists for, each as a toggle whose default is what
stock OpenTofu does. A block that sets nothing and no block at all mean the
same thing, so an estate that sets none behaves like stock plus markers.
Turning a toggle on is the setup step.

The table is generated from `internal/live/strict/registry.go` by
`go run ./tools/toggles-gen`; edit the registry, never the span. The rest of
this file is the reasoning and the measured behaviour behind each toggle.

<!-- toggles-gen:begin strict-toggles -->
| Argument | Values | Default | Meaning |
|---|---|---|---|
| `marker_repair` | `"repair"`, `"never"` | `"repair"` | What a run does about an ownership marker on a live object that disagrees with the marker this configuration declares. "repair" writes the declared value over it, as the plan's ordinary in-place tags update. "never" leaves it silently, for an estate where something else owns the tags, and only once a markers "record" selection gives the resource an identity source that is not the marker. |
| `secrets` | `"store"`, `"refuse"` | `"store"` | What a run does with the secret material a configuration generates or sets. "store" keeps it the way stock OpenTofu keeps it. "refuse" is two refusals: a secret-generating type is refused outright, and a sensitive settable argument is left out of its record. It does not reach the cache file, or a terraform_data or null_resource the configuration hands a secret. |
| `no_source_create` | `"refuse"`, `"create"` | `"refuse"` | What a run does with an instance that has no record, no live marker and no identity anything can derive from configuration. "refuse" reports it, by name, and names both remedies: "choudoufu live-import" from a stock state that already holds it, or this toggle. "create" selects stock OpenTofu's own behavior for a resource with no prior state: plan a create. |
| `provider_change` | `"refuse"`, `"recreate"` | `"refuse"` | What a run does when a resource block names a different provider configuration than the one whose account or region still holds a live object carrying this estate's marker for that block's address - a region or account change. "refuse" reports the object, by name, with the provider configuration that found it and the one its address now belongs to, and names both remedies: destroying or disowning that object, or this toggle. "recreate" selects stock OpenTofu's own behavior - plan the create under the new configuration - and warns, by name, that the old one's object is abandoned and nothing will find it again. |
<!-- toggles-gen:end strict-toggles -->

None of the settings above affects a resource being created. A create is stamped
whatever the setting says: the safety rule has no converse permitting an
unmarked create, and a create writes a marker that is new rather than one
that disagrees with anything.

The table's `marker_repair` values leave out `"report"`. It is still valid
`strict { marker_repair = ... }` grammar, and this fork's decoder parses it
and refuses it with a "not implemented yet" detail, but no build gives it a
mechanism.

`"never"` on its own, with no selection, is refused for the reason in the
`strict-marker-repair` entry in
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-marker-repair).
Markers are repaired by the plan's ordinary tags diff, and suppressing that
per key is what `lifecycle { ignore_changes }` does, which is refused: a
resource whose identity is only its marker and whose marker write is
discarded can never be found again. `"never"` therefore needs a resource to
have somewhere else to hold its identity, which is the next block.

#### Pinning `secrets` and `no_source_create` from the environment

`secrets` and `no_source_create` can be pinned to their strict setting
(`"refuse"` for both) from OUTSIDE the configuration: set
`CHOUDOUFU_STRICT_PIN=1` in the environment that runs a plan or apply, and
a `strict` block that sets either of them to anything else is refused, at
the offending argument's own line, naming the environment variable and the
value it forces. An omitted argument resolves to the pinned setting
silently, with no refusal - pinning changes what "nothing here" means, it
does not require every configuration to say so out loud.

This is the mechanism a platform team uses to require a behavior a
configuration author cannot switch off in the same commit that would relax
it: the pin lives in the process that runs the plan rather than in anything a
pull request touches. Relaxing a toggle and approving that relaxation can
never be the same change. `marker_repair` is not pinnable this way - its
three settings are not a single safety axis the way the other two are (see
the table above), so there is no one setting "pinning the profile" could
force it to.

#### `secrets`

The default is `"store"`, and that is the compatibility half: a stock
OpenTofu state file holds `random_password.result` in clear, so a
configuration that generates a password runs here with a `live` block added
and nothing else. What a state file would hold, the estate's record store
holds - namespaced per estate, under IAM, written with compare-and-swap,
with the sensitivity marks travelling beside the value. Like every other
record-backed type, a secret-generating one needs no `record_store` block: an estate
that declares none gets the implied local store.

`"refuse"` is the principle, and it is two refusals rather than one:

- a **secret-generating record-backed type** (`random_password`, `tls_private_key`,
  `local_sensitive_file` and their measured siblings) is refused at lint,
  naming the setting. It is refused again at the two other layers that could
  write such a record without lint having run: identity resolution, and
  `choudoufu live-import`, which seeds records straight from a stock state
  file;
- a **sensitive settable argument** on an ordinary cloud resource is never
  recorded as residue - the argument values this fork remembers because the
  provider's own read never gives them back. Only the sensitive argument is
  left out: the same resource's ordinary arguments are still recorded
  (GitHub issue #1873).

```hcl
terraform {
  live {
    estate = "prod"
    record_store "s3" { bucket = "my-records-bucket" }

    strict {
      secrets = "refuse"
    }
  }
}
```

`"refuse"` also turns the local cache file off. `.terraform/choudoufu-cache.tfstate`
is a stock state file written unencrypted, so under `"refuse"` it is neither
written nor read, unless `CHOUDOUFU_STATE_CACHE` names a path on purpose. A
cache file left by an earlier run is warned about by name and not deleted.

One thing `"refuse"` does not cover, which a reader could easily assume it
does:

- **A record-backed resource handed a secret by configuration.**
  `terraform_data { input = var.db_password }` is admitted and recorded
  whole. The refusal is by resource type.

[Secrets in the record store](SECRETS.md) has it.

Three things neither setting reaches, and they are not the same kind of
thing:

- **Write-only attributes**, ever. The plugin protocol forbids a provider
  returning one, so a recorded value could never be checked against the
  object it describes - and stock does not keep one either, nulling them out
  before the state is written. This is not a stricter or laxer choice.
- **Effect receipt values.** A receipt is a published breadcrumb whose whole
  purpose is that other tools can read it, which is the opposite of a record
  store's IAM boundary, and stock has no equivalent of it to be compatible
  with. See `receipt-secret` in
  [`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#receipt-secret).
- **A sensitivity mark the provider's schema did not put there.** A residue
  record stores an unmarked value and the sensitivity is reconstructed from
  the schema when the record is read, which is exact for a schema mark and
  for nothing else. A value that picked up sensitivity from a
  `sensitive = true` *variable* stays out under either setting, and the
  argument is proposed for update on every plan.

A **markerless type whose schema carries credential material** is also
outside this setting's reach today, and that is a deliberate bound rather
than an omission - see
[`strict-secrets`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-secrets)
for the two measurements behind it.

#### `markers "record"` block

A nested block inside `strict`, naming the resources that hold their
identity in the estate's record store instead of in a `tofu-address` tag. No
ownership marker is written for them at all. It is the tag-budget and
tag-policy toggle: you buy a tag back and pay for it in governability, since
an `aws:ResourceTag` condition or a cost report can no longer see the
resource as this estate's, and neither can any other tool that lists by tag.

```hcl
terraform {
  live {
    estate = "prod"
    record_store "s3" { bucket = "my-records-bucket" }

    strict {
      marker_repair = "never"

      markers "record" {
        types     = ["aws_ebs_volume"]
        addresses = ["aws_instance.worker", "module.server.aws_instance.instance"]
      }
    }
  }
}
```

| Argument | Meaning |
|---|---|
| `types` | Resource types whose every instance is selected. A literal list of strings. |
| `addresses` | Individual resources, in the `-target` grammar: module-qualified or not, no wildcards. A literal list of strings. |

Both are optional and either may be given alone, but a block naming neither
is refused: it narrows nothing, and reading it as "everything" would
withhold a marker from resources nobody named.

Three things it requires, each a lint refusal when missing:

- **The identity goes to a `record_store`.** A selection with nowhere to
  put one leaves the resource with neither a marker nor a record.
- **Whole resources go in `addresses`, not instances.** `aws_instance.web[0]`
  is refused. One configuration body serves every instance a `count` or
  `for_each` expands to and the marker written into it is a template over
  the instance key, so a marker cannot be withheld from one instance and
  written for its siblings. Split the instance you mean into its own
  resource block.
- **The type's identity must be recordable.** The provider has to import
  the type back, its exported `id` has to be provably the whole of its
  import string, and the attribute the record would hold must not be one the
  provider marks sensitive. See
  [`strict-markers-unrecordable`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md#strict-markers-unrecordable);
  those three are not skippable by choosing, because each is a way to record
  a *wrong* identity, which no later run can detect.

Pairing the selection with `marker_repair = "never"` is what makes
`lifecycle { ignore_changes }` over the marker tags stop being refused - for
the selected resources only. A resource the selection does not cover still
gets its marker and still refuses `ignore_changes = [tags]`, so an
estate-wide `"never"` meets its limit loudly rather than silently.

The label is `"record"` because it names one of a family. `markers "tag"`,
the inverse selection, is grammar this leaves room for.

