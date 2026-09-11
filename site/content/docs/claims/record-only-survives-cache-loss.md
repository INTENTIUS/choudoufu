---
title: "Claim 17: A record-only composite identity survives cache loss without a duplicate create"
weight: 17
claim: record-only-survives-cache-loss
---

# Claim 17: A record-only composite identity survives cache loss without a duplicate create

This claim covers one specific, narrow class of resource: **untaggable,
unlistable, composite-identity** types, where the record store this fork
writes on every apply is the *only* place an instance's identity is ever
held - no tag to carry it, no listing that could rediscover it. For that
class, "the record is a cache" is not true the way it is for a
tag-governable resource: lose the record and there is nothing left to
recover from, so the honest answer is a named duplicate create, never a
silent bind and never a silent "no changes."

`aws_iam_group_policy` is this fork's worked example. It carries no
`tags` argument, and left to the ordinary default - the `name` argument
absent, the provider assigning one - its two-part identity (the IAM
group, the assigned policy name) has nowhere else to come from: the
policy name is not a literal in configuration and not a reference to any
other resource's own argument, because the provider invents it at create
time. Once the object exists, the record this apply writes is the only
copy of that pairing.

This is a different resource than the one issue #746 and PR #851
measured. That PR's own re-measurement, against a real hashicorp/aws
6.59.0 provider schema, named 27 admitted types that are markerless,
unlistable and carry a *wire-identity* composite (a provider-native
`identity_schema`, several attributes, no documented separator) - and of
those, only three actually reach the located-fallback bind PR #851 fixed
(`aws_datazone_glossary_term`, `aws_opensearchserverless_security_config`,
`aws_redshift_namespace_registration`); the other 24 are already diverted
to the record-located class earlier, through a different door. None of
the three is implemented by the pinned floci image (probed directly:
`datazone`, `opensearchserverless` and `redshift-serverless` are absent
from its service list, and `redshift register-namespace` itself answers
`UnknownOperationException`). The other 24 all carry a ratified,
hand-written identity-table row whose components are literals or
references this fork's own static evaluator can fold from configuration
alone - measured directly against this same pinned image, every one of
them survives losing its record exactly because configuration alone
rebuilds the same identity, record or no record. Neither population,
today, can demonstrate this claim on the emulator.

`aws_iam_group_policy` reaches the identical recovery mechanism
(`identity.ClassRecordLocated`, `projection.materializeLocated`) through
its own ratified table row, whose policy-name component is marked
server-assigned-if-absent rather than through a wire `identity_schema` -
an older, independently-arrived-at admission for the same class of
problem. It is markerless and unlistable in exactly the sense PR #851's
population is, and IAM is a service the pinned floci image fully
implements, so it is the nearest floci-servable member of this claim's
real population: untaggable, unlistable, composite-identity resources
whose identity has at least one component the provider - not this run's
own configuration - assigns.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. From the repo root run:

  just smoke record-only-survives-cache-loss

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke record-only-survives-cache-loss and report the
"caught" line: with the identity record deleted, the plan must propose
one create, named, instead of quietly reporting no changes.
```

The steps as they print:

1. `stand the estate up, and read the record` - an IAM group and one
   inline policy on it apply, the policy's `name` left for the provider
   to assign. The record this apply writes is read back and its group
   and policy name are printed by value - no tag, no listing, holds
   either one.
2. `lose the disposable cache` - the cache and the whole `.terraform`
   directory are deleted, the same disaster claim 5 recovers from. The
   re-plan reports `No changes.`, and the scenario checks the debug log's
   own `GetGroupPolicy` read: the group and policy name it actually used
   are compared, by value, against the ones the record printed in step
   1 - not a passing count alone.
3. `lose the record too` - the honest answer is a duplicate, by name.
   With `BREAK=1`, the identity record itself is deleted before this
   same re-plan (cache and `.terraform` gone as well): nothing anywhere
   can say which live object the policy owns, and the plan must propose
   exactly one create, naming `aws_iam_group_policy.app`. Without
   `BREAK=1`, the same cache-loss recovery runs a second time with the
   record intact, for contrast: still `No changes.`
4. `teardown` - the group and its policy destroyed (or, under
   `BREAK=1`, cleaned up by hand, since the proposed duplicate was never
   applied).
