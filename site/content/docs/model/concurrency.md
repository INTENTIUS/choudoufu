---
title: "Two runs at once"
weight: 5
---

# Two runs at once

Ownership lives on the resources themselves, and records settle concurrent
writes by conditional write. Two simultaneous applies against one estate
resolve one of four ways.

| Race | Outcome |
|---|---|
| Two creates of the same client-named resource | The cloud's uniqueness constraint rejects the second. The loser re-plans, binds to the winner's resource, and comes back clean. |
| Two creates of the same server-assigned resource | Both are created. The next plan reports a marker collision naming both live IDs and refuses rather than guessing. A human deletes one. |
| Divergent in-place updates | Last writer wins at the API. The next plan reads the live system and converges. |
| An update racing a destroy | The loser gets not-found, re-plans, and converges. |

No race orphans a resource silently. Each case is a clean re-plan or a named
collision.

Compare a backend whose lock fails or was never configured, where the last
state write wins and the loser's resource drops silently out of every future
plan. A crash mid-apply is the same story, lock or no lock, because a resource
created but not yet recorded is orphaned either way. Under markers the tag rode
the create call itself, so the resource is discoverable and there is nothing to
unlock or recover.

None of that argues for applying concurrently. Serialize applies in CI, where
the real mutex has always been.
