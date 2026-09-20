---
title: "Will my configuration work?"
weight: 1
---

# Will my configuration work?

Run `choudoufu live-check` in your configuration's directory. It reads the
configuration, touches nothing, and names every block it would refuse and
why. [Check a configuration]({{< relref "/docs/use/check-a-config" >}}) has
the command. This page is the short version of what it checks.

One rule is behind most refusals: a resource's marker has to be known before
the resource exists, so whatever names the resource has to be computable from
your configuration alone. [Identity]({{< relref "/docs/model/identity" >}})
states it in full.

## Your provider

AWS is the platform everything is built for. Kubernetes works as a platform
of its own, with one label as the marker
([Kubernetes]({{< relref "/kubernetes" >}})). A resource from another
provider is admitted when that provider publishes an identity that resolves
from configuration, and refused as `unadmitted-type` when it publishes none,
which today includes `github_*` and `fastly_*`.

## Readiness tiers

Every AWS resource type is in one of four tiers, by what recovers its identity.

{{< readiness "tiers" >}}

[Resource tier lookup]({{< relref "/docs/use/resource-tiers" >}}) lists every
type.

## How your configuration is written

| You write | What happens |
|---|---|
| `count` and `for_each` | Work. They are expanded before anything is read |
| A `for_each` key with unusual characters | Works. Every printable character is allowed except six, and the rest are escaped into the marker |
| A name built from variables, locals, functions, module outputs or data sources | Works |
| A name that refers to another managed resource, directly or inside a string template | Works |
| A name computed from another resource's attribute inside a function or arithmetic | Refused, because the value does not exist yet |
| A name from `uuid()`, `timestamp()` or `bcrypt()` | Refused. It changes on every evaluation |
| A name from a `sensitive` variable | Refused, because names appear in plan output. Wrap it in `nonsensitive(...)` if it is not secret |
| `bucket_prefix` and other generated names, with no fixed name set | Refused. The name is not known before the create |
| `module` calls, nested to any depth | Work |
| `lifecycle { ignore_changes = [tags] }` | Refused. It would discard the markers |
| Provisioners, `null_resource`, `random_*`, `time_*`, `tls_*` | Work, through the record store |
| Several `provider "aws"` blocks for accounts and regions | Work. Resources that need marker discovery must share one provider configuration |

## How you run it

A `backend` or `cloud` block beside a `live` block is refused, because it
would make a state file a second record of ownership. Workspaces other than
`default` are refused. `force-unlock` is refused, since there is no lock.

## Editors and linters

Put the estate name in `estate.chdf.hcl`. Stock tools never read that
extension, so `terraform validate`, `tflint` and editors keep passing. The
same content as a `live` block inside `terraform` works too, and stock tools
reject it with `Unsupported block type`.

## The full list

[`live/COMPATIBILITY.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/COMPATIBILITY.md)
has every construct with its issue and its measurement, and
[`live/LIMITATIONS.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/LIMITATIONS.md)
has every refusal with the rule that makes it and the fix.
