# Planning one resource

GitHub issue #2245. `choudoufu live-plan -resource=ADDR` plans one managed
resource and its upstream cone, and prints the cone's drift as NDJSON in
chant's drift record v1 shape (INTENTIUS/chant#4450). It reads the record
store by key, never lists it, and runs no estate-wide marker sweep. The last
line counts what the run read, so a reader can check that.

```
choudoufu live-plan -resource=aws_s3_bucket.data
choudoufu live-plan -estate=prod -resource='module.net.aws_subnet.private[0]'
```

It runs on both of live-plan's routes: a configuration with a live block or
an `estate.chdf.hcl` sidecar, and the `-estate` form. ADDR is a managed
resource or one of its instances, in any module. A data source or a module
is refused.

## What is in the cone

The cone is the plan graph's answer to `-target=ADDR`: the resource, every
block it reads, transitively, and the data sources among them. Its identity
is resolved as any plan resolves it, including the data reads an identity,
a count or a for_each needs and the provider-configuration reads.

The records are for the cone's managed instances. When ADDR names one
instance, that block's other instances are left out, since the plan does not
reach them.

## What it reads

- The record store is opened keyed. The provisioning handshake's List is
  replaced by one Get of the sentinel, and a store with no sentinel is
  refused, for the reason the handshake exists (#693): a store that reads
  as empty would report every record-backed resource as missing. A full
  plan provisions the sentinel. The bulk load of the namespace is off, so
  each record the cone needs is one Get.
- The projection reads the cone's instances and nothing else. It does not
  list the namespace for record-backed resources whose block was deleted.
- Marker discovery runs only when an instance in the cone has a
  server-assigned identity, and lists only that instance's type, filtered
  to the estate's marker where the provider can filter. There is no sweep
  leg, no estate-wide tag-index call (the Resource Groups Tagging API call
  a full plan makes once) and no unclaimed inventory. The Cloud Control
  fallback and the per-service tag reader stay, since they answer for one
  type.
- The policy's account reconciliation, the foreign classification, the
  guided-sweep hint, root outputs, the lookalike guard and the Kubernetes
  dry run do not run. None of them is about one resource's drift.

A -target or -cone plan keeps the sweep estate-wide
([PLAN-CONE.md](PLAN-CONE.md)). This path does not, and that is the
difference between them.

The run writes nothing. The record store wrapper refuses every write, and
`-out` is refused, so the plan behind the records cannot be applied. That is
what makes leaving out the removal legs safe: a removal outside the cone is
not this run's question.

## The stream

Three kinds of line, each with a `type`:

```
{"type":"drift-start","drift":"v1","member":"live-unit","engine":"choudoufu","read":"plan","resource":"aws_vpc.main","expected":["aws_vpc.main"]}
{"type":"drift-record","member":"live-unit","engine":"choudoufu","address":"aws_vpc.main","node":null,"observedAt":"2026-10-25T09:00:00Z","read":"plan","status":"in-sync","ownership":{"verdict":"owned","by":"marker"},"evidence":{"tool":"choudoufu","version":"...","command":"live-plan -resource=aws_vpc.main","engineCode":"noop"}}
{"type":"drift-settle","drift":"v1","member":"live-unit","engine":"choudoufu","read":"plan","counts":{"in-sync":1},"reads":{"recordsRead":3,"recordKeys":2,"recordListCalls":0,"sweepCalls":0,"scanTypes":["aws_vpc"],"scanListCalls":1}}
```

A record line carries the `DriftRecord` fields under the names chant's
design note gives them. `type` is the stream's envelope: the design note
names the three line kinds and leaves the discriminator open. `member` is
the estate name, since choudoufu does not know the workspace member's name.
`node` is always null; it is chant's block-level graph id.

Statuses, following the design note's table for choudoufu live+plan:

| What the run found | status | read |
|---|---|---|
| planned no-op | in-sync | plan |
| planned update or replace | drifted, with the top-level attributes the plan writes | plan |
| ABSENT omission, or a create with no omission | pending | plan |
| UNOWNED omission | unclaimed, ownership adoptable or foreign | ownership |
| any other omission | not-observed, reason from the omission (`no-binding` for NEEDS_DISCOVERY, `read-failed` otherwise) | plan |
| expected and not in the plan | not-observed, `not-reached` | plan |

`evidence.engineCode` keeps the source token: the omission reason, or the
planned action. Drifted attributes carry names only, never values, and
`sensitive` where the planner or the schema marks the attribute.

## The read count

`reads` on the settle line is the run's own account:

| Field | Counts |
|---|---|
| `recordsRead` | Gets asked of the record store |
| `recordKeys` | distinct keys among them |
| `recordListCalls` | Lists asked of the record store. Always 0 |
| `sweepCalls` | types the estate-wide sweep listed. Always 0 |
| `scanTypes` | the types the cone's own discovery listed |
| `scanListCalls` | how many type listings that was |

A List reaching the store is refused, and a non-zero `recordListCalls` or
`sweepCalls` fails the run with an error naming the count, so a regression
cannot pass as a clean read.

`TestLivePlanResource_readsNoListAndSweepsNothing` holds this. It runs a
whole-estate plan over a fake store and a fake sweep leg first, to show both
fakes see a List and a sweep, then runs `-resource` over the same fakes and
asserts neither was reached.

## What it does not do

- It does not find what nothing declares: no orphan, no foreign object, no
  record of a deleted block.
- A type whose list call drops tags relies on the tag index to read its
  marker, and that index is not read here. Such a needs-discovery instance
  reports `not-observed` with `no-binding` where a full plan would bind it.
- Azure, GCP and Kubernetes find a server-assigned identity through their
  sweep legs, which this path does not build. A needs-discovery instance of
  those families is looked for through the provider's own list protocol, and
  is `not-observed` when the provider cannot list it. Client-named instances
  of every family are read as usual.
- The records stream at the end of the plan, not one by one as each read
  returns.

Options it cannot honour are refused: `-target`, `-exclude`, `-destroy`,
`-refresh-only`, `-replace`, `-out`, `-cone`, `-adoption-only` and `-filter`.
`-detailed-exitcode` exits 2 when a record is drifted, pending or missing.

What the run costs against a real estate has not been measured. The
maintainer runs the estate check.
