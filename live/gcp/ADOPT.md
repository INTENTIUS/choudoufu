# Adopt

## Today

A labelled `google_*` resource is yours once it carries this estate's
`tofu-estate` label and the `tofu-address` label that encodes its address
(`live/MARKERS.md`, "GCP: labels"). Both go into the resource's own
`labels` map in the create call hashicorp/google already makes, so a
created resource carries them, and the plan shows them like any label you
declared. A `count` set whose members the server names carries `tofu-slot`
too, as on AWS.

The network skeleton (networks, subnetworks, firewalls, routes, routers,
service accounts, custom roles) has no `labels` map. It binds by the
`name`, `project` and location the configuration declares, recomputed on
every plan, with nothing written on the resource. It needs no adoption: a
declared skeleton object binds on the first plan.

## From a stock state file

```
choudoufu live-import -state=stock.tfstate -estate=my-estate
```

Without `-approve` it previews. With it, it reads the state file once,
reads each resource back, and writes the markers through a labels-only plan
and apply on each labelled resource, judged the way the AWS tags-only write
is judged:

- a plan that would replace the resource or change anything outside its
  labels is refused, and nothing is written;
- a resource already labelled for another estate is refused;
- a resource whose `tofu-address` label encodes another address is refused,
  because rewriting it would be a rename;
- an estate name that is not a legal label value (over 63 characters) is
  refused.

Only the configuration's own `labels` map is rewritten. Labels another
client set stay where they are, visible in `effective_labels`. The state
file is never modified.

## The marker

Three labels, all legal GCP label values: the estate name, a 53-character
encoding of the address (two SHA-256 prefixes, block and instance), and
the slot where there is one. The address label is matched against the
configuration and never decoded, so it carries no escaping and no
continuation labels whatever the address.

hashicorp/google reads labels non-authoritatively, keeping only the keys
its prior state named. A plan with no cache reads the markers from
`effective_labels`, where every label on the resource appears, and mirrors
them back, so an imported resource replans empty.

## Refused, not guessed

`live/gcp/COMPATIBILITY.md`, "Refused", has the table: a marker written by
hand into an instance template's labels, an estate name over 63
characters, an unlabelled type whose identity the server mints, and
`live-mv` until its label rewrite lands. Each is refused by name with
nothing written.

## Leaving

Remove the `live` block, keep the cache file, and stock OpenTofu with the
same provider carries on. The labels stay on the resources, where stock
ignores them, or `gcloud` removes them.
