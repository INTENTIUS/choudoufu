---
title: "Use it"
weight: 5
bookCollapseSection: true
---

# Use it

The pages a reader needs once they are past the model and the policies: what
a real config runs into, how to bring an existing estate in or start a new
one, and day-to-day operation. Where things are stored and the fork's own
surface are here too.

| Page | Question it answers |
|------|---------------------|
| [Compatibility reference]({{< relref "compatibility" >}}) | What in a real configuration this fork accepts or refuses |
| [Resource tier lookup]({{< relref "resource-tiers" >}}) | Your resource types, one by one: tier, status, and why for anything not admitted yet |
| [How to check a configuration before migrating]({{< relref "check-a-config" >}}) | Running `choudoufu live-check` against your own code |
| [What you set up by hand]({{< relref "setup" >}}) | What must exist before the first run, versus what the tool creates |
| [Migrate an existing estate]({{< relref "migrate" >}}) | How resources you already manage bind to their markers |
| [Day-2 operations]({{< relref "day2" >}}) | Renaming, removing, recording effects and working with other people, indexed |
| [Recover an estate]({{< relref "recover-an-estate" >}}) | What to do, in order, when the cache or the record store is gone - and what does not come back |
| [Start a new estate]({{< relref "start" >}}) | The `live` block, from a first apply |
| [Questions]({{< relref "faq" >}}) | Short answers to the questions that come up first |
| [How to write markers inside a for_each'd module]({{< relref "keyed-modules" >}}) | Threading `each.key` through a wrapped module by hand |
| [Where things are stored]({{< relref "storage" >}}) | Markers, records and receipts, what lives where, and how the record store bucket is laid out |
| [The three things a record store bucket must have]({{< relref "bucket-contract" >}}) | Versioning, a lifecycle rule and public-access block: what each protects against, when it is checked, and what waiving one costs |
| [Encryption at rest for the record store]({{< relref "encryption" >}}) | Why it is not asserted, that every SSE flavour works, and the customer managed key arrangement |
| [IAM for the record store bucket]({{< relref "iam" >}}) | The policy for one estate's role, from the script that renders it |
| [Secrets in the record store]({{< relref "secrets" >}}) | Who can read an estate's recorded values, and the ways out |
| [Reading a value from another estate]({{< relref "cross-estate" >}}) | A data source on the live resource, and why no run reads another estate's objects |
| [How the compatibility numbers are measured]({{< relref "measurement" >}}) | Where the corpus ranking comes from, and what not to read into it |
| [How the pinned AWS provider gets bumped]({{< relref "provider-bump" >}}) | What a provider upgrade can change, and how it is reviewed |
| [Running an estate from CI]({{< relref "cicd" >}}) | The five jobs a choudoufu pipeline runs, per forge, and what the governance policies lock |
| [Reference]({{< relref "reference" >}}) | The fork's call surface, per stage |
