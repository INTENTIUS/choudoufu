---
title: "Claim 49: A parent's marker never proves its child exists"
claim: a-parent-proves-no-child
---

# Claim 49: A parent's marker never proves its child exists

A child whose identity is derived from its parent is read for itself on
every plan. A parent that is present and marked says nothing about
whether its children still exist, so a child deleted out of band is
proposed for creation again while its parent stays as it was. It is D2
of [#2119](https://github.com/INTENTIUS/choudoufu/issues/2119).

Each scenario runs from the repository root and ends on a `PASS` line; its
`BREAK=1` run breaks the thing the proof rests on and must print a
`caught` line. [The README](README.md) says what each needs installed.

## On AWS

No scenario is written yet, so the cell is open.

## On Kubernetes

n/a: no Kubernetes kind is derivable from a parent and unmarkable as a
whole object, so there is no child to infer.

## On Azure

### az-a-parent-proves-no-child

    just smoke az-a-parent-proves-no-child
    BREAK=1 just smoke az-a-parent-proves-no-child

Two parents tagged for the estate, each with a child that takes no tags:
an `azurerm_subnet` in a VNet, and an `azurerm_network_security_rule` in a
network security group. Both children are deleted with ARM DELETEs. The
next plan must propose exactly the two creates while both parents stand
and keep `tofu-estate`, and the apply puts both back.

`BREAK=1` runs the same deletes and holds the plan to what a plan that
inferred the children from their parents would read, `No changes.`; that
check must fail on the real plan, which proposes the two creates.
Floci-az runs the sweep's Resource Graph query, `union`, `mv-expand` and
`bag_keys` included, since dfa33ca (lex00/floci-az#11), not yet run ([live/FLOCI.md](../../FLOCI.md#azure-floci-az)). It has no dated run yet, so the cell reads unproven.
