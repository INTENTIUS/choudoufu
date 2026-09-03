"""Tips for whoever is at the keyboard, in two registers.

Every phase of the workbench carries a drawer with two tabs. The beginner
tab says what the phase means in plain terms and what to watch on the map.
The OpenTofu-hand tab answers the questions an expert asks on first sight:
where the state went, why a plan lists before it reads, why -refresh=false
still makes a call per type, how a fresh config adopts without import, what
live-mv writes and refuses. Pure data, so the page and the README can both
draw on it and a test can read it.
"""
from __future__ import annotations

# The facts an OpenTofu hand needs once, referenced from several phases.
STATE_WENT = (
    "Where the state went: identity is two tags on the resource (tofu-estate, tofu-address); values the "
    "cloud cannot hand back (a random suffix, a null_resource's run) live in a record store beside the "
    "module (.tofu-records); the file under .terraform is a stock-format cache and never the authority. "
    "Lose every local file and a plan reads its estate back from the account by tag."
)

TIPS: dict[str, dict[str, str]] = {
    "seed": {
        "beginner": "Seeding is how resources come to carry the two identity tags, tofu-estate and tofu-address. The demo seed applies a config that was written for choudoufu, so the tags arrive with the apply. Adopting your own config does the same for resources that already exist: verify first, which reads every resource your state names and refuses anything it cannot match, and only then adopt, which writes the two tags and nothing else. Your state file is read, never changed.",
        "expert": "Adopt runs live-import: without -approve it is verify-only (no writes, exit non-zero on any unmatched address); with -approve it writes tofu-estate and tofu-address on each taggable resource and leaves untaggable ones to follow their parent. Nothing else about the resource changes and the tfstate you pointed at is not rewritten. The estate name you give here becomes the value every plan filters on, so pick the name you will govern by (an IAM condition on aws:ResourceTag/tofu-estate).",
    },
    "plan": {
        "beginner": "The plan is a table: one row per resource, with the estate it is in and the estate it should go to. Rules fill the destination column in bulk (everything under one module, everything of one type, every address with a name in it) and any single row can be overridden by hand. Rows that stay put are not moves. Saving writes the plan beside the run; nothing in the cloud changes until the move phase.",
        "expert": "Later rules win over earlier ones and a per-row edit wins over rules. Only taggable resources are rows; untaggable children (inline policies, attachments) follow their parent's tag and are shown as a count. The file is runs/<id>/carve.json: from, estates, moves[{address,to}], rules. The executor iterates moves and runs live-mv per address; the rules are recorded so the plan explains itself later.",
    },
    "preview": {
        "beginner": "Preview runs every planned move as a dry run and reports what each would do: which tags it would write and which children would follow, or why it refuses. The map on the right is the estate as it would stand afterwards, drawn from those reports. It is safe to run as often as you like, and it is the thing to show before anyone agrees to a move.",
        "expert": "Each row is one `live-mv -dry-run -from-estate=<src> <addr> <addr>` in the destination's directory: the refusals are live-mv's own (address not found under the source tag, destination already claims it, an untaggable type named directly), so what preview refuses, move refuses. The projection is the page's arithmetic over the passed rows, not a choudoufu feature; the plan-cost bars per destination estate come from the verify phase, not from here.",
    },
    "preflight": {
        "beginner": "Nothing touches the cloud yet. The run reads which account your credentials resolve to and which "
                    "choudoufu binary will run, and refuses to continue if either is not what the run was set up for.",
        "expert": "Two reads: sts get-caller-identity and `choudoufu version`. The demo seed pins one account and one "
                  "release, because its numbers were measured there; a user seed asserts the account its own profile "
                  "resolves to and the estates its plan names. `choudoufu` is a fork of OpenTofu that keeps stock "
                  "behaviour and adds a live backend; with no `live` block it is stock.",
    },
    "setup": {
        "beginner": "The demo seed applies one configuration: three teams' worth of roles, policies and log groups, all "
                    "in one estate. Watch the map fill: every box is a resource, its colour is the estate that owns it.",
        "expert": "The config carries `terraform { live { estate = \"...\" } }` instead of a backend block. The apply "
                  "stamps two tags on every taggable resource (tofu-estate, tofu-address) through the provider's own "
                  "tag attributes; untaggable types (an inline role policy, an attachment) take identity from the "
                  "taggable parent they belong to, drawn here as dotted boxes tied to it. " + STATE_WENT,
    },
    "slow-plan": {
        "beginner": "A plan of the whole monolith, changing nothing. The number to watch is how many requests it "
                    "costs, because every plan of a big estate pays it.",
        "expert": "With no state file, a plan first discovers its estate: one listing per resource type (the tagging "
                  "API where the type is taggable, the service's own list call where it is not), then a read per "
                  "resource. Stock's refresh would be about one read per resource; the listing is the price of "
                  "statelessness, and the comparison the workbench draws is monolith against estate under the same "
                  "tool, not choudoufu against stock.",
    },
    "decompose": {
        "beginner": "Three new configurations, one per team, each applied on its own. Nothing is rebuilt: the map "
                    "recolours as each team's resources change owner. Where there was one boundary there are three.",
        "expert": "A declared address binds to the live resource carrying that address tag, so a fresh config adopts "
                  "what it declares without `import`; the apply's only change is the tofu-estate tag. The monolith's "
                  "config loses the blocks that moved, or it would plan to recreate them. Two configs declaring the "
                  "same address is refused, not resolved by preference.",
    },
    "fast-plan": {
        "beginner": "One team's plan, from its own cache, next to the monolith's number from a minute ago. A plan "
                    "should cost what its estate costs, not what the whole account costs.",
        "expert": "`-refresh=false` here still makes one listing per type: the sweep vouches that each cached resource "
                  "is still live under the estate's tag before its cached values are served, so a deleted resource is "
                  "never served stale. The requests you see are those listings; the reads are what the cache saved. "
                  "The tagging index lags a write by a minute, so the workbench waits for it before measuring.",
    },
    "carve": {
        "beginner": "One team dissolves into another: its resources change owner with one tag write each, and the "
                    "role's inline policy and attachment follow it without a write of their own. No state was edited.",
        "expert": "`choudoufu live-mv -from-estate=<source> <address> <address>`, run in the destination's directory: "
                  "a tags-only apply through the provider that rewrites tofu-estate (and tofu-address when the "
                  "address changes). It refuses when the destination does not declare the address, when the "
                  "destination already carries it, when a third estate owns it, or when the plan would touch anything "
                  "but tags. `-dry-run` makes every check and stops. Untaggable children follow the parent's live "
                  "tag: a parent tagged for another estate never anchors a read in this one.",
    },
    "guard": {
        "beginner": "Four reads and no writes: the role's tag, its children, then one plan per estate. Both plans "
                    "must be clean at the same moment, or the move left something behind.",
        "expert": "The source plan must show no destroy and no 'owned and undeclared' line; the destination plan no "
                  "create and no 'unowned' line. Terraform's equivalent, state mv or rm-and-import, has a window "
                  "where one side wants to destroy and the other to create; here both read only what carries their "
                  "tag, so both are clean at once or the verdict says which is not.",
    },
    "receipt": {
        "beginner": "Every ownership move was a tag write, and the account logs tag writes. This reads them back: "
                    "who wrote which tag on what, and when.",
        "expert": "CloudTrail lookup-events by event name (TagRole, TagPolicy, TagResource and kin) since the run "
                  "began, filtered to the run's own resources, in the region where IAM's global events land. A state "
                  "edit has no equivalent record. The governed refusals, an IAM condition on the ownership tag saying "
                  "no, are claim 13's smoke on the emulator and its own CloudTrail receipt.",
    },
    "teardown": {
        "beginner": "Only for the demo seed: every estate it made is destroyed through its own configuration, then the "
                    "account is listed to prove nothing with the run's prefix remains.",
        "expert": "Destroys run from each estate's manifest entry, newest first; the sweep lists roles, policies and log "
                  "groups by prefix and refuses to call the run clean while anything remains. A user seed has no "
                  "teardown anywhere on the page.",
    },
}


def tip(phase: str, register: str) -> str:
    """The tip for one phase in one register, or an empty string."""
    return TIPS.get(phase, {}).get(register, "")
