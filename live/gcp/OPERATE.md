# Operate

## Rename

`live-mv` refuses a GCP resource by name ("live-mv cannot rewrite a GCP
resource's labels yet") and writes nothing, until its label rewrite lands.
Renaming does not need it. Rename the block, or add a `moved` block, and
plan: the records follow the address, and the plan rewrites the
`tofu-address` label as an ordinary in-place label change, the way an AWS
plan rewrites the tag. Nothing is replaced.

A skeleton type carries no label, so its rename is the configuration
change alone.

## Records

GCP records go in a Cloud Storage bucket, `record_store "gcs"`
([#2122](https://github.com/INTENTIUS/choudoufu/issues/2122);
`live/STORAGE.md` has the arguments). One bucket per estate is the
default, because GCS cannot fence a listing: a principal that may list a
shared bucket lists every estate's records in it.

## Remove

Delete the block. The record proposes the removal, and the sweep
confirms it: an undeclared labelled resource carrying this estate's label
is proposed for deletion like any other orphan. Three things are excluded
first:

- controller-held objects, a GKE node's VM and disks, a Dataproc or Batch
  VM, a Cloud Run service another product manages
  (`live/gcp/COMPATIBILITY.md`, "Refused");
- an object whose address label matches nothing declared, which is
  reported and never planned, because the label cannot be decoded into an
  address to destroy;
- a skeleton object, which the sweep cannot find at all: only its record
  proposes its removal.

`policy { undeclared_tagged = "untag" }` removes this estate's labels and
leaves the resource running, through the same labels-only plan
`live-import` uses.

## Scale

A `count` set of client-named resources binds by address. A set whose
names the server assigns carries `tofu-slot`, so scaling down removes the
highest slots and every survivor keeps its index, as on AWS.

## Plan, review, apply

As on AWS. The sweep reads an index that lags, so a resource deleted a
moment ago may still be listed; a record tombstone is how the plan tells
that from a second claimant.

## From CI

As on AWS, with the principal holding the bindings `live/gcp/estate-grant.sh`
grants.

## Two runs at once

There is no lock. Two applies to one estate meet at the record store's
conditional write (`ifGenerationMatch`), and the second is refused.
