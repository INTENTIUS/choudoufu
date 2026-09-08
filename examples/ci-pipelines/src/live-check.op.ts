/**
 * `live-check`: can this configuration move under live resource markers at
 * all, and what stops it if it cannot.
 *
 * The cheapest job in the pipeline and the first one a pull request should
 * fail on. `choudoufu live-check` makes no cloud calls, reads no state and
 * needs no estate, so the generated job carries no credentials of any kind -
 * no role to assume, no `id-token: write`, no secret. Its whole cost is a
 * checkout, an install and a parse.
 *
 * Init runs first because choudoufu's own help says the answer is materially
 * more accurate with provider schemas present: a type the built-in admission
 * table does not carry is admitted anyway when the provider's identity schema
 * settles it, and without schemas every such type reads as refused. `init`
 * fetches providers from the registry, which needs the network and no AWS
 * credentials.
 *
 * ## Why the second step exists
 *
 * `choudoufu live-check` exits non-zero on a refusal precisely so it can gate
 * CI, but chant's `choudoufuLiveCheck` activity deliberately does not throw on
 * that: it carries the answer back as `refused: true` with choudoufu's own
 * report, so an Op can read a refusal rather than crash on one. That is the
 * right shape for an activity and the wrong shape for a gate, so this Op adds
 * the assertion the pull request needs, reading the first step's own output.
 *
 * The `policyCheck` profile is single-attempt: a refusal is deterministic and
 * retrying it three times only delays the red run.
 */

import { Op, activity, phase } from "@intentius/chant/op";
import { choudoufuLiveCheck, terraformInit } from "@intentius/chant-lexicon-terraform";

const ROOT = "estate";

const check = choudoufuLiveCheck(ROOT, { id: "check" });
check.outcomeAttribute = { name: "Refused", from: "refused" };

const assertAdmissible = activity(
  "shellCmd",
  {
    cmd: [
      'if [ "$CHOUDOUFU_LIVE_CHECK_REFUSED" != "true" ]; then exit 0; fi',
      'echo "choudoufu live-check refused this configuration:" >&2',
      'printf "%s\\n" "$CHOUDOUFU_LIVE_CHECK_REPORT" >&2',
      "exit 1",
    ].join("\n"),
    env: {
      CHOUDOUFU_LIVE_CHECK_REFUSED: check.out.refused,
      CHOUDOUFU_LIVE_CHECK_REPORT: check.out.text,
    },
  },
  { profile: "policyCheck" },
);

export default Op({
  name: "live-check",
  overview: 'Refuse a pull request whose configuration choudoufu cannot run under live markers',
  labels: { TerraformRoot: ROOT, TerraformMode: "live" },
  phases: [
    phase("Init", [terraformInit(ROOT)]),
    phase("Check", [check]),
    phase("Gate", [assertAdmissible]),
  ],
});
