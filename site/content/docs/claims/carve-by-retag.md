---
title: "Claim 12: Carve by retag"
weight: 12
claim: carve-by-retag
---

# Claim 12: Carve by retag

In stock tooling every ownership boundary is a state file, so splitting a
monolith into team estates is state surgery. Each resource is moved
between files by hand, and for a moment it sits in two ledgers or in
none. Here the boundary is
a tag. A resource leaves one estate for another by having its
`tofu-estate` tag rewritten. The tool refuses a write that would leave
either side dirty, and afterwards each side plans clean and pays only
for what it holds.

```text
Clone https://github.com/INTENTIUS/choudoufu. Confirm Docker is running
(docker info), the AWS CLI is installed, and Go is installed (this
scenario generates its estate with go run). From the repo root run:

  just smoke carve-by-retag

Explain each step's verdict line to me as it prints. Then run
BREAK=1 just smoke carve-by-retag and report the "caught" line: it moves
the six blocks but skips the retag, and the monolith must propose
destroying the leavers while the new estate proposes building them.
```

The steps as they print:

1. `stock stands the terralith up` - the pinned stock OpenTofu applies
   the generated terralith the ordinary way. It is 79 resources. Most of
   them are IAM. A small ECS layer and a Route 53 fan-out sit beside them.
   The estate carries `count` and `for_each` expansion and one
   module-nested pod. One state file, and not a marker anywhere.
2. `one command adopts it, and the state file is deleted` - `live-import`
   reads the file once and stamps 38 resources; the other 41 are
   untaggable and compose their identity from a stamped parent. The file
   goes, and the plan is clean. The request count of that plan is kept
   for step 6.
3. `carve a team out` - six blocks move to a new root with its own live
   block, the git half any tool needs. Then `live-mv -from-estate` runs
   three times in the destination, once for each resource that carries a
   marker. The inline policy and the two attachments carry none and need
   no write. The plain CLI reads the role's new estate tag and its inline
   policy still attached.
4. `both sides plan clean` - the monolith no longer declares or owns the
   six; the team estate declares all six and owns the three. No state was
   split, nothing was rebuilt.
5. `carve across a reference` - the ECS execution role leaves for an IAM
   estate. The task definition that stays reads the same ARN through a
   data source, the cross-estate pattern the docs give, and all three
   estates plan clean.
6. `a plan costs what its estate holds` - the team estate's plan makes a
   fraction of the requests the monolith's did.
7. `teardown` - the monolith destroys its 71 resources. The IAM estate
   destroys 2 and the team estate 6. Nothing is left. Every count in this
   list is asserted by value by the scenario itself rather than typed
   here from a reading - `carve-by-retag.sh` refuses unless stock adds
   exactly 79, unless `live-import` ratifies 38 of 79, and unless the
   three teardowns account for 71, 2 and 6 - so a run is what
   re-measures them, and those assertions have stood since commit
   `e57cc5b4`.

The `BREAK=1` run does the git half of the carve and never rewrites a
tag. That is the two-ledger window stock lives in, manufactured: the
monolith must name the leavers under owned and undeclared and propose
destroying them, and the new estate must propose creating what it
declares and does not own. Either side planning clean would show the
tag write had changed nothing.

The verb this claim rests on landed with it, and so did the rule it
exposed. A move leaves the source's records for the resource behind, and
the source's next plan found the moved role's untaggable children in
those records and proposed destroying them. The live tag decides. A
parent whose marker names another estate never anchors a child for this
one, whatever a left-behind record says, and that rule is why step 4
plans clean.
