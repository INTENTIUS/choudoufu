---
title: "How to cover every team with one policy"
weight: 4
---

# How to cover every team with one policy

This is attribute-based access control, ABAC, applied to the resources your
configuration manages.

A policy per team is a policy set that grows with the org chart and drifts
between its members. ABAC exists to avoid that, and it needs an attribute on
the resource to match against. Markers are one, written on every resource the
estate manages rather than applied by a convention someone has to remember.

## The policy

```json
{
  "Sid": "MutateYourOwnTeamsEstates",
  "Effect": "Allow",
  "Action": ["ec2:CreateTags", "ec2:DeleteTags", "ec2:TerminateInstances"],
  "Resource": "*",
  "Condition": {
    "StringEquals": {
      "aws:ResourceTag/tofu-estate": "${aws:PrincipalTag/team}"
    }
  }
}
```

One policy covers every team. Onboarding a team is a session tag rather than a
new policy, and there is no per-team document to review, drift, or forget to
revoke.

The principal tag can come from your identity provider through
`AssumeRoleWithSAML` or `AssumeRoleWithWebIdentity`, so team membership is
asserted where it is already managed rather than copied into AWS by hand.

## Pick the naming convention deliberately

The match is a string comparison, so estate names and team tags have to agree.
Decide which direction is authoritative before you have many of either. An
estate named for a system rather than a team will not match, and a team that
owns several estates needs either several tags or a prefix match with
`StringLike`.

## The same shape takes any condition key

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

## Where it stops

The condition is only evaluated where AWS evaluates it, and a resource can only
carry a marker if its type takes tags.
[Where AWS honours the condition]({{< relref "/docs/governance/reach" >}}) has both bounds.
