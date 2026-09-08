/**
 * `live-discover`: the scheduled sweep. What does the account actually hold
 * under this estate, and what of it is claimable.
 *
 * This is the job that catches a resource somebody created out of band and
 * nobody ever tagged. Nothing else in the pipeline is looking: `live-plan`
 * runs on a pull request, so it only ever sees an estate somebody is already
 * editing.
 *
 * Three reads, in the order that makes each one worth its cost:
 *
 *  - **Sweep** is `choudoufu live-ls -json -consistent`, straight off the
 *    Resource Groups Tagging API. It reads no configuration, no state and no
 *    record store: it is the account's own answer to "what carries this
 *    estate's marker". `-consistent` polls past the tagging API's
 *    eventual-consistency window, which a nightly job can afford and a pull
 *    request cannot. Its `gaps` list is the other half - a declared address
 *    the sweep could not answer for, with the rung and the reason.
 *  - **Ledger** is `choudoufu live-plan` in adoption-only mode, which is where
 *    the adoptable count and the paste-ready tagging commands come from. The
 *    sweep says what is out there; the ledger says what this estate could
 *    claim.
 *  - **Report** posts the ledger, and only the human render of it.
 *
 * The cron lives on the Op itself, as `schedule`. It is runtime-neutral data:
 * `chant operator` reads it as this Op's tick cadence, and
 * `generateOpsPipeline` renders it as the workflow's `on: schedule` cron -
 * which is why `generate.ts` passes no cron of its own for this Op.
 *
 * Nothing here writes a marker. `live-adopt` is what claims what this job
 * found, and it gates first.
 */

import { Op, phase, type ActivityStep } from "@intentius/chant/op";
import { choudoufuLiveLs, choudoufuLivePlan, terraformInit } from "@intentius/chant-lexicon-terraform";
import { discoverFindingMode } from "./forge";

const ROOT = "estate";

const sweep = choudoufuLiveLs(ROOT, { consistent: true, id: "sweep" });
sweep.outcomeAttribute = { name: "Estate", from: "estate" };

const ledger = choudoufuLivePlan(ROOT, { adoptionOnly: true, id: "ledger" });
ledger.outcomeAttribute = [
  { name: "Drift", from: "drift" },
  { name: "Unowned", from: "unowned" },
  { name: "Adoptable", from: "adoptable" },
];

const phases = [
  phase("Init", [terraformInit(ROOT)]),
  phase("Sweep", [sweep]),
  phase("Ledger", [ledger]),
];

if (discoverFindingMode !== "report") {
  // The same `reconcilePr` call the watch composite makes, spelled out here
  // because this Op's Plan phase is not the composite's. `entries: []` and
  // `body` together mean the activity opens what it is handed rather than
  // running a lifecycle plan of its own to find out.
  //
  // `ledger.out.finding` is the plan text with the adoption ledger under it.
  // It is one reference to one string field on purpose: there is no
  // arrangement of these args in which the `-json` document reaches a body.
  const report: ActivityStep = {
    kind: "activity",
    fn: "reconcilePr",
    args: {
      env: ROOT,
      mode: discoverFindingMode,
      entries: [],
      title: 'Adoptable resources in the "ci-pipelines-example" estate',
      body: ledger.out.finding,
    },
    outcomeAttribute: { name: "Issue", from: "issueUrl" },
  };
  phases.push(phase("Report", [report]));
}

export default Op({
  name: "live-discover",
  overview: 'Sweep the account for what the "ci-pipelines-example" estate holds, and report what is adoptable',
  labels: { TerraformRoot: ROOT, TerraformMode: "live", Watch: "true" },
  schedule: { cron: "0 6 * * *", overlap: "skip" },
  phases,
});
