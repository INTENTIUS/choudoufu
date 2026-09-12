---
title: "Claim 13: The tag is the boundary"
weight: 13
claim: the-tag-is-the-boundary
---

# Claim 13: The tag is the boundary

In stock Terraform and OpenTofu, who owns a resource is a line in a state
file. Changing that line is state surgery: no IAM policy can gate it,
because the cloud never sees it, and nothing in the account records it.
Under choudoufu ownership is a tag on the resource, and a tag write is an
API call the cloud's own policy engine evaluates per resource. A role can
be fenced to half an estate by a condition on the ownership tag, with the
grant `live/MARKERS.md` publishes under "Granting an estate". That fence
binds the credential, not the binary: the same condition governs a plain
AWS CLI call with no choudoufu anywhere in the process, exactly as it
governs choudoufu's own writes, and what it lets through is not hidden
from the tool either - the next plan reads live tags, not a log of who
wrote them. A carve, one half moving into an estate of its own, is then a
governed write the platform can refuse. The scenario turns the emulator's
IAM enforcement on for its run; the harness's own key stays privileged,
and only the two roles the scenario creates and assumes are governed.

The boundary this claim proves is narrow, and it is worth stating exactly
that way. The grant fences three actions by name -
`ec2:CreateTags`, `ec2:DeleteTags` and `ec2:TerminateInstances` - on
resources carrying the ownership tag's value for the caller's half. It
says nothing about any other action, and nothing about a resource this
estate does not own. Read it as "this condition governs the actions it
names, on the resources that carry the tag it names," never as a claim
that IAM fences every write a tool-less actor could make.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke the-tag-is-the-boundary

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke the-tag-is-the-boundary and report both "caught"
lines: the first drops the conditions from Bob's grant, and Bob's write
on Alice's half through choudoufu must go through, which proves the
condition and not the credentials was the boundary; the second repeats
that with no choudoufu in the call at all, a plain AWS CLI write, and it
must go through too.
```

The steps as they print:

1. `the platform stands one estate up, two halves in it` - two instances
   in estate app, one under module.net and one under module.data, with
   markers stamped by the account.
2. `two roles, two halves, one grant shape` - Alice may act on
   module.data.* and create into data; Bob may act on module.net.* and
   create into net. The evidence line prints the conditions.
3. `Alice converges her half` - a tag change on the database, applied
   under Alice's session.
4. `Alice is denied on Bob's half - by AWS, not by this tool` - the same
   kind of change on the gateway. The provider's CreateTags comes back
   403 and the gateway is untouched.
5. `Bob converges the same change` - his session, his half.
6. `Bob, tool-less, is refused on Alice's half - by AWS, with no
   choudoufu in the call path` - under Bob's session, with nothing of
   this tool anywhere in the process, a plain `aws ec2 create-tags` and a
   plain `aws ec2 terminate-instances` against the database both come
   back refused. The same condition that governs choudoufu's own writes
   governs a script's.
7. `Bob's own half, tool-less, and the platform lets it through - the
   next plan sees it` - the identical plain CLI call against the
   gateway, Bob's own half, lands with no choudoufu involved, and the
   next `choudoufu plan` names the drift and proposes reconciling it -
   nothing the fence permits is invisible to the tool. Bob then
   reconciles it with an ordinary apply.
8. `the carve begins with a git move, and Bob's attempt at the retag is
   denied` - the data module moves to a new root, and Bob's
   `live-mv -from-estate=app` is refused by the platform before anything
   moves.
9. `Alice completes the carve: one governed tag write` - the same
   command under Alice's session, and tofu-estate becomes data.
10. `both estates plan clean, each under its own role` - No changes in
   data under Alice and in app under Bob.
11. `teardown - each estate by its own destroy`.

The `BREAK=1` run replaces Bob's grant with the same reach and no
conditions, then has Bob change a tag on Alice's half. The write must go
through. If the platform still refused, something other than the
condition was the boundary and the claim would prove nothing. It then
repeats the write with no choudoufu at all - a plain `aws ec2 create-tags`
under Bob's session - and that must go through too, or step 6's refusal
above would have measured a check this tool runs before calling the API
rather than the condition itself.

One emulator note. Real EC2 refuses with `UnauthorizedOperation`; the
emulator refuses with a 403 whose body the EC2 SDK cannot parse, so the
provider prints `api error UnknownError`. The scenario matches both, and
the gap is filed as lex00/floci#189.

On the real account, the same carve ran in us-east-2 on 2026-09-03 with
the roles assumed through STS. Every governed write was in the account's
own CloudTrail event history within a minute. The two refusals
carry the code real EC2 uses, and each record names the session that was
refused:

```text
04:39:31Z  alice  OK                            Name=database-v2           i-01e1006285c2b37b3
04:39:47Z  alice  Client.UnauthorizedOperation  Name=gateway-v2            i-0d3d2031d0b946a23
04:40:02Z  bob    OK                            Name=gateway-v2            i-0d3d2031d0b946a23
04:40:32Z  bob    Client.UnauthorizedOperation  tofu-estate=boundary-data  i-01e1006285c2b37b3
04:40:40Z  alice  OK                            tofu-estate=boundary-data  i-01e1006285c2b37b3
```

Each line is one `ec2:CreateTags` event, and
`live/smoke/evidence/the-tag-is-the-boundary.cloudtrail.json` holds the
five with their event IDs and the lookup that returned them. No state
file could have produced that record, because a state edit is not an API
call. The estate was torn down afterwards and the account listed back to
baseline.
