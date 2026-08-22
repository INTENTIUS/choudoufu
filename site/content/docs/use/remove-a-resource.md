---
title: "How to stop managing or destroy a resource"
weight: 7
---

# How to stop managing or destroy a resource

Deleting a resource block leaves its marker on the live object, and the sweep
destroys a marked, undeclared, taggable resource on the next plan. That matches
upstream without a `removed` block.

To stop managing something without destroying it, change what happens to a
resource you no longer declare.

```hcl
# estate.chdf.hcl
estate = "my-estate"

policy {
  undeclared_tagged = "untag"
}
```

`untag` removes this estate's marker and leaves the resource running. `keep`
leaves both alone. [The ownership policy matrix]({{< relref "/docs/use/ownership-policy" >}})
has the rest of the verbs.

## Two cases where removal leaves an orphan

Standing limits rather than races, and the plan names them every time.

**Types carrying no tags.** `aws_route`, `aws_route_table_association`,
`aws_s3_bucket_policy`, `aws_s3_bucket_versioning`, `aws_iam_role_policy`,
`aws_kms_alias`, `aws_route53_record` and others have nowhere to put a marker,
so deleting the block removes the only record of which live resource it was.
Destroy the resource before removing its block, or delete it out of band. The
set is determined at runtime from each type's provider schema rather than
fixed.

**Types outside the admission table.** A live resource carrying this estate's
markers at an unadmitted type is invisible to the removal sweep, which is
defined over the admission table.

Both appear in every plan under "Not swept for removal". Types a provider
cannot list or tag are reported by count, since that holds every run. Pass
`-verbose` to itemise them. A list call that actually failed is itemised every
time.

## The sweep can be a run behind

Finding resources you own but no longer declare may go through AWS's Resource
Groups Tagging API, which is eventually consistent. A resource whose tags have
not propagated is not returned, so an orphan can be reported one run late.

That is the only direction this bites. Binding the resources you *do* declare
reads each type through its own service API rather than the tag index, so a
freshly tagged resource is never mistaken for a missing one and no plan
proposes a duplicate because of it.
