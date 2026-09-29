---
title: "Claim 41: The estate answers in the present tense"
claim: the-estate-answers-in-the-present-tense
---

# Claim 41: The estate answers in the present tense

A tool that answers questions about an estate from a stored copy answers as
of the last time it touched the estate. This estate's answer comes from the
platform. Its resources carry the `tofu-estate` tag, so the tagging API
lists them, and a describe says what they are now. The claim asks one
question two ways after a change made out of band. The live way gives the
new answer and the state cache gives the old one. The cache is not being
faulted here. The point is that nothing the estate depends on for the
answer is a stored copy.

This is about the estate's own resources. It is not account-wide gap
analysis, which HANDOFF puts outside the promise.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. If Go is not installed,
export CHOUDOUFU_VERSION=<latest tag from
https://github.com/INTENTIUS/choudoufu/releases>. From the repo root run:

  just smoke the-estate-answers-in-the-present-tense

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke the-estate-answers-in-the-present-tense and report
the "caught" line: it leaves the world unmoved, and the two answers must
then agree.
```

The question is "which of this estate's security groups are attached to
nothing".

1. `stand the estate up and ask the question`. A VPC, a subnet, three
   security groups (`web`, `db`, `spare`) and one instance using `web`.
   Live: the tagging API for groups tagged `tofu-estate=smoke-present`,
   then a describe of every instance and network interface in the
   account, with no choudoufu in the loop. Stored: the state cache the
   apply wrote, read as a file. Both answer `db spare`. They must agree
   here, or the two queries are asking different questions.
2. `move reality out of band`. `aws ec2 modify-instance-attribute
   --groups` swaps the instance from `web` to `db`. No choudoufu runs.
3. `ask again, both ways`. Live answers `spare web`. The cache still
   answers `db spare`, the answer as of the apply.
4. `the next plan reads the present too`. `choudoufu plan` reads the
   instance live and proposes the one in-place update that puts `web`
   back.
5. `teardown`. Six resources destroyed.

Under `BREAK=1` step 2 is skipped. Asked again, both ways must still agree,
and the run fails if it reports a divergence. That shows the divergence in
the main arm comes from the world moving and not from two queries that
disagree by construction. The control was also run with the move left in
under `BREAK=1`, and it failed on the divergence as it should.

The attachment is an instance and not a standalone network interface
because the pinned emulator does not implement `CreateNetworkInterface`.
The live query counts network interfaces as well, so the answer is the
same on AWS.
