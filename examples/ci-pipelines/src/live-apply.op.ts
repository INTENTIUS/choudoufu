/**
 * `live-apply`: the push half, and the only Op in this project that is
 * allowed to change the estate. Init, `plan -out=chant.tfplan`, Gate on the
 * `show` of that file, `apply chant.tfplan`.
 *
 * `gate: "always"` rather than the default `"on-destroy"`: this is the
 * authoritative position on the lifecycle dial, so every merge stops for an
 * approval rather than only the ones that would destroy something.
 *
 * ## Two gates, and which one is the approval of record
 *
 * chant's gate is a fact on a ledger, not a wait. The push run reaches it,
 * finds no resolution, records the pending fact and ends - it does not hold a
 * runner open. `chant approve live-apply approve-live-apply --approver you`
 * writes the resolution as a commit on the `chant/lifecycle` branch, and
 * re-running the workflow walks through the gate and applies. That is the
 * approval of record, and it is the one a reviewer can audit after the fact.
 *
 * A forge-side environment reviewer stacks on top of it, and now generates:
 * `generate.ts`'s `live-apply` spec carries `environment: { name: "production" }`
 * (chant #2264), naming the same environment
 * `examples/pipeline-governance/github/governance.yml` declares a reviewer
 * on - see that policy and its README for what the reviewer adds on top of
 * the chant gate below. GitLab's generator maps the same option to its own
 * `environment:` key; Forgejo Actions has no environments at all, so its
 * dialect drops the key and says so in a header comment on the generated
 * file rather than silently dropping the gate the option exists for.
 *
 * ## What the approval covers
 *
 * The plan file crosses the gate, and `plan.out.planFile` is a reference to
 * the Plan step's own output rather than a literal path, so the file the
 * approver read is the file the Apply step names (chant's TF101 fails the
 * build if that pairing is ever spelled out by hand).
 *
 * On a live root, `apply <planfile>` does not replay the file. Prior state is
 * rebuilt from the live system every run, so the apply re-plans, then compares
 * its fresh plan with the approved one - same addresses, same actions, same
 * live objects, same planned values - and refuses at exit status 3 when they
 * disagree ("The approved plan no longer matches the live system", or "The
 * approved plan belongs to a different estate"). chant maps that status to a
 * result rather than a thrown error, so the run comes back as refused instead
 * of broken.
 */

import { TerraformApplyOp } from "@intentius/chant-lexicon-terraform";

const { op } = TerraformApplyOp({
  name: "live-apply",
  root: "estate",
  gate: "always",
});

export default op;
