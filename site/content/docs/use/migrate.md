---
title: "Migrate an existing estate"
weight: 3
---

# Migrate an existing estate

You have an OpenTofu or Terraform configuration that already manages live
resources. Migrating means putting a marker on each of those resources.

{{% hint warning %}}
Turning on the live backend does not bind resources you already manage. A
resource with no marker is not yours yet, so the first plan reads it as absent
and proposes a second one beside it. Applying that plan succeeds and creates
the duplicate. Put the markers on first.
{{% /hint %}}

## Keep the state file

Do not delete `terraform.tfstate`. It is the only input `choudoufu
live-import` has. The live backend ignores the file, so keeping it costs
nothing, and it is your way back to stock.

If your state is in a remote backend, pull a copy first with `tofu state pull
> terraform.tfstate`.

## With a state file: two commands

Run these in a directory `choudoufu init` has prepared, with the same provider
configuration the state was last applied with.

```
choudoufu live-import -state=terraform.tfstate -estate=my-estate
choudoufu live-import -state=terraform.tfstate -estate=my-estate -approve
```

The first writes nothing and prints a report. The second writes a marker on
every entry the report showed as `VERIFIED` or `DRIFTED`. Entries shown as
`MISSING`, `UNTAGGABLE` or `UNADMITTED_TYPE` are never written, and the
report says why for each. The state file is read once and never changed. The
command works on AWS and on Kubernetes.

## Without a state file: adopt by hand

This path never offers a `count` or `for_each` instance, so use `live-import`
if you still have a state file.

1. Create `estate.chdf.hcl` with `estate = "my-estate"`, and remove any
   `backend` or `cloud` block.
2. Run `choudoufu plan` and read the `Adoptable` section. Each entry names a
   live resource that matches a block and has no marker, with the command
   that adopts it.
3. Run those commands as printed. They carry the region and endpoint the plan
   used.

```
aws ec2 create-tags --resources 'vpc-0123456789abcdef0' \
  --tags 'Key=tofu-estate,Value=my-estate' 'Key=tofu-address,Value=aws_vpc.main' \
  --region 'us-east-1'
```

4. Plan again. Every adopted resource reports no changes.

Any tool that can write two tags can adopt a resource. `live/MARKERS.md` is
the contract.

## Then

Keep the `live` block or the sidecar in place from here on. A plain plan
without it is stock mode, and stock mode with no state file proposes
rebuilding the estate. The run warns when it sees that.

## Getting back out

Copy `.terraform/choudoufu-cache.tfstate` to `terraform.tfstate` and remove
the `live` block. Stock OpenTofu's first plan proposes one kind of change,
removing the two marker tags, and you are back on a state file.

Run that same plan with choudoufu and it is refused with `Plan would remove
this estate's ownership markers`, so that a `live` block lost in a bad merge
cannot un-migrate an estate by accident. To leave on purpose, name the estate:

```
CHOUDOUFU_UNMIGRATE=my-estate choudoufu apply
```

[`live/MIGRATE.md`](https://github.com/INTENTIUS/choudoufu/blob/main/live/MIGRATE.md)
has the rest: what binds on its own, hand-writing a marker for an expanded
instance, the `Unowned` section, the types with no adoption path, the guard
that stops a stock run stripping markers, and the measured rate for a large
estate.
