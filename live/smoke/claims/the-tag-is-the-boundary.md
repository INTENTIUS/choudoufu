---
title: "Claim 13: The tag is the boundary"
claim: the-tag-is-the-boundary
---

# Claim 13: The tag is the boundary

In stock, who owns a resource is a line in a state file the cloud never
sees, so no policy can gate it. Here ownership is a marker on the
resource, a marker write is an API call the platform's own policy engine
judges, and every answer a plan gives is about one estate and one provider
configuration. Two estates in one account need nothing between them but
their markers.

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

### the-tag-is-the-boundary

    just smoke the-tag-is-the-boundary
    BREAK=1 just smoke the-tag-is-the-boundary

With the emulator's IAM enforcement on, Bob's role is fenced to half an
estate by a condition on the ownership tag (the grant `live/MARKERS.md`
publishes): his writes on Alice's half are refused by the platform, a
plain `aws ec2 create-tags` under his session included, and a carve is a
governed tag write. The grant fences `ec2:CreateTags`, `ec2:DeleteTags`
and `ec2:TerminateInstances` on resources carrying the tag, nothing more.
The same carve ran on real AWS on 2026-09-03; its CloudTrail record is in
`live/smoke/evidence/the-tag-is-the-boundary.cloudtrail.json`. `BREAK=1`
gives Bob the same reach with no condition, and his write must go
through.

### the-boundary-holds-across-regions (claim 16 until #1817)

    just smoke the-boundary-holds-across-regions
    BREAK=1 just smoke the-boundary-holds-across-regions

One estate spans two regions, with one client-chosen log group name in
both. A delete in one region is seen in that region and is not vouched
for by the other region's object (the defect of #745). An orphan in a
region nothing declares is still found, recovery re-runs in both regions,
and a region change is a replace, refused unless named. `BREAK=1` strips
the surviving object's markers and points the second region at the
first, and the plan must catch the dead instance being served as
unchanged.

### the-boundary-holds-across-accounts (claim 19 until #1817)

    just smoke the-boundary-holds-across-accounts
    BREAK=1 just smoke the-boundary-holds-across-accounts

The same estate across two accounts in one region: one name in each,
told apart only by the account. Kept as its own proof rather than a mode
of the regions scenario because the emulator setup differs (a second
account's credentials) and the regions scenario has two steps this one
does not. `BREAK=1` is the regions control with the account swapped for
the region.

### two-estates-at-once (claim 43 until #1817)

    just smoke two-estates-at-once
    BREAK=1 just smoke two-estates-at-once

Two roots declare the identical address and read the identical data
source, differing only in `tofu-estate`, and apply at the same moment:
both finish clean with nothing serialized. `BREAK=1` gives both the same
estate value, and the next plan must name both live roles rather than
read clean.

## On Kubernetes

### k8s-the-label-is-the-boundary

    just smoke k8s-the-label-is-the-boundary
    BREAK=1 just smoke k8s-the-label-is-the-boundary

The fence is one `ValidatingAdmissionPolicy`
(`live/kubernetes/estate-boundary.yaml`) on the estate label, installed by
a cluster admin, with the grant an ordinary ClusterRole. It judges a plain
`kubectl label` like any other write. It is write-only (admission never
sees get or list) and cluster-wide. The plan refuses a block declaring
another estate's object before any cluster is consulted. `BREAK=1`
removes the policy, and the writes it refused must go through, while the
plan-side refusal must still hold. One estate across clusters, or two
estates applying to one cluster at once, is not measured.
