/**
 * `dev-converge`: the loop issue #1033 asks for. One tick observes `dev`
 * (`chant lifecycle plan dev --live --json` + `chant components status dev
 * --live --json`, chant's `convergeTick` activity), classifies the result
 * against the rule table below, and dispatches a matched `run()` up to
 * `budget` — see chant's `docs/src/content/docs/guide/converging-lifecycle.mdx`
 * for the phases and `.../guide/operator.mdx` for what ticks it.
 *
 * ## Why this table reads `createCount`/`adoptCount`, not `status`
 *
 * chant's own doc example keys its drift rule on `eq("status", "drifted")`.
 * `status` is the worst `ComponentStatusRow.reconciliation` verdict across
 * whatever `chant components status --live` reports, and that command's rows
 * come from `*.component.ts` declarations and `chant components release`
 * records (chant's `components/discover.ts`, `cli/handlers/components.ts`) —
 * a different chant feature from a plain terraform root. This project
 * declares neither, so that surface reports zero rows every tick, and
 * `worstStatus([])` is unconditionally `"reconciled"`. `status` would never
 * read `"drifted"` here, whatever happens to the estate — measured against a
 * live tick while building this example, not asserted from the docs alone.
 *
 * The signal that *is* live for a plain terraform root is the change-set
 * counts `chant lifecycle plan --live` already computes, which is what
 * `ConvergeSymptom` carries alongside `status` for exactly this reason (see
 * `@intentius/chant/lifecycle/symptoms`). Rules here key on those instead.
 *
 * ## What a live root can and cannot see as drift
 *
 * chant's terraform lexicon observes a live root through choudoufu's own
 * `live-plan -json`, and — unlike a stock root's `terraform show -json` —
 * that read is presence/ownership-only: `bound[]`/`unowned[]`/`adoptable[]`/
 * `omissions[]` (`@intentius/chant-lexicon-terraform`'s
 * `describe-resources.ts`), never a property tree. `examples/ci-pipelines`'s
 * README says the same thing under "What is not here": no
 * `observeResourcesDeep()`, so no property-level drift and no claimed-field
 * set, for a live root specifically.
 *
 * Concretely: deleting the declared log group out of band flips its
 * classification from `noop` to `create` (missing, per chant's own
 * observation-category → plan-action crosswalk), which moves `createCount` —
 * this rule table's whole live-drift signal. Editing one of its *tags* out of
 * band would not move anything this table can read: the resource is still
 * present, so it is still `noop`, whatever its tags say now. That is why this
 * example's demo script introduces drift by deleting the resource rather
 * than by editing a tag, even though the issue names both as options — both
 * were tried against a live floci while building this example, and only the
 * deletion moved a count `ConvergeSymptom` carries.
 *
 * Two more counts were tried and dropped from this table for the same
 * measured-not-assumed reason. `unobservedCount` sat at 5 on every tick this
 * run ever saw, clean or not: `terraform{}`, `provider "aws" {}`, both
 * `variable` blocks and the `estate.chdf.hcl` sidecar itself all read as one
 * `unsupported-kind` unobserved entity apiece — "only resource blocks have a
 * row in a live-plan document" — so a rule keyed on `gt("unobservedCount",
 * 0)` would report every single tick regardless of drift, on this root or any
 * other with variables in it. And `adoptCount` never moved for a foreign
 * resource created at an address this root doesn't declare: `describeResources`
 * only ever asks about the entity names the build declares, so an
 * out-of-band resource under a name this root never mentions is invisible to
 * `chant lifecycle plan` entirely (that is `live-discover`'s job — see
 * `examples/ci-pipelines`'s README — not a converge tick's). Even stripping
 * the ownership tags from the *declared* resource, so choudoufu's own
 * `live-plan` no longer recognizes it as this estate's, left it `noop`: it is
 * still present at a declared identity, and chant's plan-action crosswalk
 * only proposes `adopt` for a resource `orphan` (`declared: false`), never
 * for a declared one whose marker went missing. `adopt-report` below is kept
 * in the table anyway — a rule reading `adoptCount` and only ever
 * `report()`-ing is exactly the shape OPS014's adopt-safety check exists to
 * require, and it is typed and build-checked like every rule here — but
 * this example's own demo script does not claim to have made it fire; see
 * the README's proof list for exactly what did and did not run live.
 *
 * ## `dial: "apply"`, and what OPS014 checks about it
 *
 * `dev-apply` classifies `mutating` (see that file's doc comment), so
 * dispatching it needs `dial: "apply"` — chant's `OPS014` refuses a mutating
 * `run()` under `"observe"`/`"reconcile"` at build time, and refuses any
 * destructive dispatch under every dial including `"apply"`. Neither
 * refusal applies here; both are exercised by chant's own test suite, not
 * this project's.
 *
 * ## An open question this rule table's own dispatch runs into
 *
 * `dev-apply` is `gate: "always"`, so this table's whole live proof runs
 * through a dispatch that stops at a gate — and measuring that dispatch
 * surfaced a second finding beyond the two above. chant's own
 * `classifyDispatchFailure` (`@intentius/chant`'s
 * `src/op/activities/converge.ts`) reads a dispatched op's `--json` record
 * looking for `parsed.gate?.gate`, but a gated `TerraformApplyOp` run's own
 * record carries `gate: { name: "<gate>", since: "<iso>" }` — the field is
 * `gate.name`, and `classifyDispatchFailure`'s regex fallback
 * (`/is gated on "([^"]+)"/`) never matches either, because that phrase
 * belongs to the human render `--json` mode suppresses. So every dispatch
 * this rule table makes to a gated `TerraformApplyOp` is misclassified: the
 * gate is real (`dev-apply`'s own run genuinely stops there, exit 3, a
 * genuine pending fact lands on `_gates/dev-apply.jsonl`, and `chant
 * operator status` genuinely shows it pending), but `dev-converge`'s own
 * tick record calls the outcome `"reported"` rather than `"gated"`, with an
 * empty `reason` (`dispatch of "dev-apply" failed: `). See the README's
 * "Two upstream findings" section for the exact commands and quoted output
 * that pinned this down, and for why this example does not attempt to work
 * around it: the fix belongs in `classifyDispatchFailure` itself, not in a
 * rewrite of what a project's own dispatched Op prints.
 *
 * ## No `schedule`
 *
 * Omitting `schedule` (chant's `ConvergeOpConfig.schedule` is optional) means
 * this Op carries no cron, and `chant operator`'s own loop
 * (`op/operator.ts`'s `runOperatorRound`) ticks an Op with no cron on every
 * round — bounded only by `--interval` — rather than only on cron-firing
 * minutes. A production deployment converging on a real cadence would set
 * `schedule` to that cadence and run the operator with a matching
 * `--interval`; this demo wants ticks measured in seconds, which a 5-field
 * cron cannot express, so it leaves the cadence to `--interval` alone.
 */

import { ConvergeOp, gt, run, report, when } from "@intentius/chant/op";
import type { ConvergeSymptom } from "@intentius/chant/lifecycle/symptoms";

export const { op } = ConvergeOp({
  name: "dev-converge",
  env: "dev",
  dial: "apply",
  budget: 2,
  rules: [
    when<ConvergeSymptom>(gt("createCount", 0), run("dev-apply"), {
      id: "recreate-deleted",
      why:
        "A declared resource is missing from the live account, most likely deleted out of band; " +
        "re-apply recreates it, converging the account back to what dev-apply.op.ts's root declares.",
      // Flap-damping counts a rule's PREDICATE matching, not the outcome its
      // action reached (`consecutiveRuleFires` walks `firedRuleIds`, which is
      // recorded whether the tick ran, reported, or gated) — measured, not
      // assumed, while building this example's own demo: `dev-apply` sitting
      // at an unapproved gate for more than the default threshold (3) of
      // this Op's own ticks reads as flapping, and this rule stops
      // dispatching (`skipped-flap`) exactly as if the drift were never
      // clearing on its own, even though a human just hasn't approved yet. A
      // real environment wants the default; this demo's own approval step
      // is a scripted pause rather than a person watching a chat channel, so
      // it raises the threshold instead of racing the default one.
      flapThreshold: 20,
    }),
    when<ConvergeSymptom>(gt("adoptCount", 0), report("an undeclared resource is present in the account"), {
      id: "adopt-report",
      why:
        "An unowned live resource is reported for a human to review and adopt by hand " +
        "(choudoufu live-adopt); a converge tick never claims ownership on its own — OPS014 " +
        "refuses outright a rule that reads adoptCount and dispatches a mutating op.",
    }),
  ],
});

export default op;
