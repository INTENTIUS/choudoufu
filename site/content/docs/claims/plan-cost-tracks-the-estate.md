---
title: "Claim 14: A plan costs its estate, not its account"
weight: 14
claim: plan-cost-tracks-the-estate
---

# Claim 14: A plan costs its estate, not its account

A bound state file makes a terralith's plan pay for the whole account:
every resource anyone owns sits in the one file every plan reads end to
end. Here ownership is a tag, not a file, so a plan of one estate reads
only that estate's resources - and stays that cheap no matter how large
the rest of the account grows around it.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info) and the AWS CLI is installed. From the repo root run:

  just smoke plan-cost-tracks-the-estate

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke plan-cost-tracks-the-estate and report the "caught"
line: it replans the same estate account-wide instead of scoped to its
own tag, and the cost must jump to the account-wide shape.
```

The steps as they print:

1. `stand up one estate, and plan it alone` - a four-resource network
   estate (a VPC, two subnets, a security group) applies, then plans.
   Its request count is recorded.
2. `grow the account with another estate, and replan the first` - an
   eight-resource estate joins the account under a different tag. The
   first estate replans to the same request count as step 1, whether or
   not the second estate exists.
3. `what reading the whole terralith would cost` - an account-wide,
   adoption-only scan of the same account costs measurably more than
   the estate-scoped plan - the shape a bound state file would force on
   every plan, regardless of which estate you actually meant to touch.
4. `teardown` - both estates destroyed.

The `BREAK=1` run makes the same request the account-wide scan in step 3
made, against the same estate step 1 and 2 scoped for free. If the cost
did not climb to that account-wide shape - more than triple what scoping
cost, the threshold the scenario checks - something other than the
estate scoping was keeping the plan cheap, and the claim would prove
nothing.
