---
title: One plan, one apply, across estates
description: A root that calls each estate as a module plans and applies them together.
prompt: >-
  Read https://intentius.io/choudoufu/how-it-works/across-estates/ and say
  whether our root could run as a composed root. Change no files.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`), approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

A root with no `live` block of its own that calls each estate's directory as a
module plans all of them in one plan and applies them in one apply. No flag
turns it on.

```hcl
# root/main.tf
module "net"  { source = "../net" }
module "app"  { source = "../app" }
module "logs" { source = "../logs" }
```

An estate reads another through `terraform_estate_outputs`. This is the same
file whether `app` runs alone or composed:

```hcl
# app/main.tf
data "terraform_estate_outputs" "net" {
  estate = "net"
  names  = ["subnet_id"]
}

resource "aws_instance" "web" {
  subnet_id = data.terraform_estate_outputs.net.values.subnet_id
}
```

## What the plan shows

Say `net` creates a subnet in this run. Its id does not exist yet, so in `app`
the read shows as known after apply. `net` applies first, and during the same
apply the value resolves and `app` uses it. When `net` has nothing pending, the
read happens in the plan from outputs the run already holds.

Planning one root at a time gives two choices. The downstream plan can wait for
its upstream to apply, which means two runs in order. Or it plans on a
placeholder for the value, and the real plan comes later.

## Ownership stays put

Each module counts as the root of its own directory and keeps its estate name
and record store. A record key and a `tofu-address`
marker carry the address the resource has in its own directory (`aws_vpc.main`,
not `module.net.aws_vpc.main`). Running one estate alone afterwards sees the same
ownership.

## Limits

- The root holds module calls, outputs and variables. A resource in the root is
  refused, and so is a module call with no `live` block or a non-local source.
- A module call takes no arguments, `count`, `for_each`, `enabled` or
  `providers`. An estate's inputs are its own variable defaults.
- `-target`, `-exclude` and cone plans are refused. Plan one estate by running
  its directory alone.
- A failed apply leaves every applied resource applied and recorded under its
  own estate. Nothing is rolled back, and a re-run plans only what is left.
- Roles stay per estate when each module's `assume_role` sits in that module's
  provider block. An identity taken from the environment is one identity for
  every estate.
- A sensitive output stops the run, and so does an estate reading its own name.

Three cases have not been run against a record store yet
([#2208](https://github.com/INTENTIUS/choudoufu/issues/2208)). One is an estate
run alone after a composed run. Another is a re-run after a composed apply that
failed part way. The last is a destroy of a composed root. No test has covered them.
