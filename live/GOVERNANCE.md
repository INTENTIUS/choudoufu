# Governance with markers

How to scope IAM roles with the `tofu-estate` and `tofu-address` markers: one
estate, part of one, one policy for every team, a staging role kept out of
production, and nothing created unowned.

Tag-based IAM scoping is a feature AWS already has. What it needs is tags
that are reliably there, on everything, correct. That is what markers are.

Every resource this fork creates whose type takes tags carries `tofu-estate`
and `tofu-address`, derived from its configuration address. For most types
the marker rides in the create call; a few cannot take tags at creation and
are tagged straight afterwards. That is not a convention someone has to
remember or a `default_tags` block that drifts. Its edges, which a policy
inherits, are in [IAM-REFERENCE.md](IAM-REFERENCE.md), under where AWS
honours the condition.

So scoping a role is ordinary tag conditions.
[Scope a role to an estate](#scope-a-role-to-an-estate) has the policies
themselves. They are written from AWS's documentation and not run by this
project, except one shape:
[claim 13](smoke/claims/the-tag-is-the-boundary.md) fences
`ec2:CreateTags`, `ec2:DeleteTags` and `ec2:TerminateInstances` between two
roles under emulator IAM enforcement, with a real-account CloudTrail record.
Test the rest against your own account.

## Three things a file cannot do

The grants above have rough equivalents in splitting a configuration. These
do not. They work because the permission unit is a cloud resource, so every IAM
feature that applies to resources now applies to your infrastructure, and IAM
has features a file has never had.

- [Stop a staging role reaching production](#stop-a-staging-role-reaching-production).
  A staging role is denied on anything belonging to another estate, so the
  mistake fails at the cloud rather than at review.
- [Cover every team with one policy](#cover-every-team-with-one-policy).
  ABAC over the estate tag, so onboarding a team costs a session tag rather
  than a policy.
- [Deny creating anything unowned](#deny-creating-anything-unowned). Deny
  creates carrying no estate tag, and ownership becomes a precondition of
  existing.

## One configuration, many owners

This one has an equivalent today, and the equivalent is the problem. Two
teams sharing a state file both need write on it, so the usual answer is two
root modules with `remote_state` wired between them. Your repository
layout ends up determined by your blast radius decisions and stays that way.

Here the configuration stays one thing and the boundary is a policy. Each
team's role is scoped to its own addresses, so an apply touches only what that
team changed. Moving the boundary is an edit to a condition rather than a
restructuring.

Two things to hold. Reads stay open across the estate, because discovery has
to see everything or a plan proposes duplicates. And if a team plans a change
to something it does not own, the apply fails at that resource, which is the
right answer but is worth knowing before it happens in front of someone.

## Protecting the markers

The grants above rest on tags, so a stripped tag is a real hazard. An AWS
Organizations tag policy cannot block a tag's removal at all. An SCP can
deny the untagging call, but only in member accounts and only where the
condition key is honored, so the last line of defense is the plan: a create
that matches an unowned live resource of its type gains a
`[POSSIBLE DUPLICATE]` warning above the plan diff, naming that resource and
the command that adopts it.
[MARKERS.md](MARKERS.md#protecting-the-markers) has the SCP, including the
continuation keys `aws:TagKeys` needs, and the gaps it leaves.

## Scope a role to an estate

### The whole estate

Creating and mutating are conditioned by different keys, and tagging is
both.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "MutateOnlyThisEstate",
      "Effect": "Allow",
      "Action": ["ec2:CreateTags", "ec2:DeleteTags", "ec2:TerminateInstances"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:ResourceTag/tofu-estate": "prod-networking"},
        "StringEqualsIfExists": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "CreateOnlyIntoThisEstate",
      "Effect": "Allow",
      "Action": ["ec2:RunInstances"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {"aws:RequestTag/tofu-estate": "prod-networking"}
      }
    },
    {
      "Sid": "TagOnlyAsPartOfThatCreate",
      "Effect": "Allow",
      "Action": ["ec2:CreateTags"],
      "Resource": "*",
      "Condition": {
        "StringEquals": {
          "aws:RequestTag/tofu-estate": "prod-networking",
          "ec2:CreateAction": "RunInstances"
        }
      }
    }
  ]
}
```

`aws:ResourceTag` governs what the estate already owns; a create has no
resource yet, so the second statement conditions on `aws:RequestTag`. The
first statement's `aws:RequestTag` line stops a retag into another estate,
and the third's `ec2:CreateAction` stops the role stamping its estate onto
resources it did not create.
[MARKERS.md](MARKERS.md#granting-an-estate) has the reasoning, and the grant
for a type tagged after its create and for a marker removed by `DeleteTags`.

The actions are illustrative and all EC2. The tagging action differs by
service; [IAM-REFERENCE.md](IAM-REFERENCE.md) has the generated per-service
table, and the services on which a run cannot stamp a marker at all.

### Part of an estate

One substitution. Condition on `aws:ResourceTag/tofu-address`
instead, and the grant covers named addresses rather than the whole estate.

```json
"Condition": {
  "StringLike": {"aws:ResourceTag/tofu-address": ["module.app.*", "module.app:*"]}
}
```

`aws:RequestTag/tofu-address` is the matching create grant, giving
a principal the right to create one declared address and nothing else.

[Claim 13](smoke/claims/the-tag-is-the-boundary.md) runs the change half: in
one estate the role changes `module.app` and AWS refuses it on `module.db`.
`module.app:*` covers a keyed module call;
[MARKERS.md](MARKERS.md#what-this-grant-cannot-reach) has the limits.

### Across estates

A role holding parts of several estates is one policy with several
statements, or one statement whose condition names several values. No state
file is shared, and no estate is split to make it possible.

```json
"Condition": {
  "StringEquals": {
    "aws:ResourceTag/tofu-estate": ["prod-networking", "prod-data"]
  }
}
```

### Read without write

Listing what an estate contains is a tagging API call, so an auditor or an
incident responder needs no `choudoufu` binary and no write access.

```sh
aws resourcegroupstaggingapi get-resources \
  --tag-filters Key=tofu-estate,Values=prod-networking
```

Grant `tag:GetResources` plus the read actions for the services
involved. The estate is legible to them and unchangeable by them.

### Handover and splitting

Handover is attaching the policy to the receiving role and detaching it from
the sending one: two IAM changes, no tag writes. Splitting is a rewrite of
`tofu-estate` on the resources leaving, plus a copy of the policy under the
new name. `choudoufu live-mv -from-estate=<old> <address> <address>`, run in
the new estate's configuration after the block moves there, makes that tag
write, with its refusals in
[How to rename a resource](https://intentius.io/choudoufu/docs/use/rename-a-resource/#moving-a-resource-to-another-estate);
its role needs both estate names in its `aws:RequestTag` condition.
[MARKERS.md](MARKERS.md#granting-an-estate) has both in full.

## Stop a staging role reaching production

Pointing a staging configuration at a production account is a familiar outage.
Nothing structurally prevents it today, because it is the same principal making
the same API calls either way. IAM has nothing to tell the two runs apart.
Markers give it something to tell them apart by.

### The policy

Attach this to the role your staging runs use.

```json
{
  "Sid": "NeverMutateAnotherEstate",
  "Effect": "Deny",
  "Action": [
    "ec2:TerminateInstances",
    "ec2:DeleteSubnet",
    "ec2:DeleteVpc",
    "rds:DeleteDBInstance"
  ],
  "Resource": "*",
  "Condition": {
    "StringNotEquals": {"aws:ResourceTag/tofu-estate": "staging"}
  }
}
```

A mistake now fails at the cloud rather than at review. The guardrail sits
below the tool, so it holds whatever the tool was pointed at and whatever the
plan said, from whatever directory someone was standing in.

### Keep reads out of the Deny

Discovery lists across the account to bind declared resources to live ones. A
run that cannot read reports its own resources as absent and proposes creating
them again, which is the duplicate hazard rather than a safety improvement.

Deny mutating actions. Leave `Describe`, `List` and `Get` alone.

### Untagged resources are covered too

A resource carrying no `tofu-estate` tag has no value for the condition to
compare against, which makes `StringNotEquals` true and extends the Deny to it.

That is the safe direction, and it is usually what you want, since a resource
outside every estate is not something a staging run should be deleting. Confirm
the behaviour against your own policy before relying on it.

### Where it stops

The condition is only evaluated where AWS evaluates it, and a resource can only
carry a marker if its type takes tags. [IAM-REFERENCE.md](IAM-REFERENCE.md)
has both bounds.

This is a guardrail, not a substitute for separate accounts. It narrows what a
mistake can reach inside an account. Production in its own account remains the
stronger boundary.

## Cover every team with one policy

This is attribute-based access control, ABAC, applied to the resources your
configuration manages.

A policy per team is a policy set that grows with the org chart and drifts
between its members. ABAC exists to avoid that, and it needs an attribute on
the resource to match against. Markers are one, written on every resource the
estate manages rather than applied by a convention someone has to remember.

### The ABAC policy

```json
{
  "Sid": "MutateYourOwnTeamsEstates",
  "Effect": "Allow",
  "Action": ["ec2:CreateTags", "ec2:DeleteTags", "ec2:TerminateInstances"],
  "Resource": "*",
  "Condition": {
    "StringEquals": {
      "aws:ResourceTag/tofu-estate": "${aws:PrincipalTag/team}"
    },
    "StringEqualsIfExists": {
      "aws:RequestTag/tofu-estate": "${aws:PrincipalTag/team}"
    }
  }
}
```

Without the `aws:RequestTag` line a team could retag its own resource into
another team's estate.

One policy covers every team. Onboarding a team is a session tag rather than a
new policy. There is no per-team document to review, drift, or forget to
revoke.

The principal tag can come from your identity provider through
`AssumeRoleWithSAML` or `AssumeRoleWithWebIdentity`, so team membership is
asserted where it is already managed rather than copied into AWS by hand.

### Pick the naming convention deliberately

The match is a string comparison, so estate names and team tags have to agree.
Decide which direction is authoritative before you have many of either. An
estate named for a system rather than a team will not match, and a team that
owns several estates needs either several tags or a prefix match with
`StringLike`.

### The same shape takes any condition key

Once the resource carries an attribute, every IAM condition applies to it.

| Key | What it gives you |
|---|---|
| `aws:CurrentTime` | A change window. The estate is mutable between named hours. |
| `aws:MultiFactorAuthPresent` | MFA required to touch production. |
| `aws:SourceIp` | Changes only from a known network. |
| `aws:PrincipalOrgID` | Only principals inside your organization. |

A session that expires expires the access with it. None of these can be
expressed against a state file, because a file has no attributes for a
condition to read.

### Where ABAC stops

The condition is only evaluated where AWS evaluates it, and a resource can only
carry a marker if its type takes tags. [IAM-REFERENCE.md](IAM-REFERENCE.md)
has both bounds.

## Deny creating anything unowned

Tag compliance is normally retroactive. A scanner finds untagged resources, a
ticket asks someone to explain them, and the backlog never empties because new
ones arrive faster than old ones are resolved.

Conditioning creation inverts it. Ownership stops being something a resource
acquires later and becomes a precondition of existing.

### The SCP

As a service control policy, applied to the accounts that hold estates.

```json
{
  "Sid": "NothingIsCreatedUnowned",
  "Effect": "Deny",
  "Action": ["ec2:RunInstances", "ec2:CreateVolume"],
  "Resource": "*",
  "Condition": {
    "Null": {"aws:RequestTag/tofu-estate": "true"}
  }
}
```

`Null` with `true` matches when the key is absent, so a create that supplies no
estate tag is denied. An account under this policy cannot accumulate resources
nobody can account for.

### Check which types can tag on create first

This is the bound that decides whether the policy is usable, and it is worth
establishing before you write it.

The stamp pass writes `tofu-estate` and `tofu-address` into the resource's own
`tags` argument. For most types the AWS provider carries tags on the create
call, which is what puts `aws:RequestTag` in the request for this condition to
read. Where a type cannot take tags on create (`aws_route53_zone` is one;
[#1084](https://github.com/INTENTIUS/choudoufu/issues/1084)), choudoufu tags
immediately afterwards; the key is then absent from the create call, and this
Deny stops a legitimate one.

Name the actions you have confirmed. Do not reach for a wildcard and find out
in production.

### It governs more than choudoufu

The policy conditions the create call rather than the tool, so it applies to
anything with credentials: the console, the CLI, another pipeline. That is
the point. A resource created by hand either carries an estate tag or does not
get created.

It also means the tag is worth something as evidence. A resource carrying
`tofu-estate` under this policy was claimed at birth rather than labelled
afterwards by whoever ran the scanner.

### Where the SCP stops

`aws:RequestTag` is a different key from `aws:ResourceTag` and services support
them independently, so confirm this one specifically.
[IAM-REFERENCE.md](IAM-REFERENCE.md) covers the reach of both.

A service control policy has no effect on the organization's management
account, or on any principal outside the organization.
