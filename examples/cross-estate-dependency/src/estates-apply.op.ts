/**
 * `estates-apply`: the producer, then the consumer, in one Op.
 *
 * ## Why one Op and not two
 *
 * Applying the producer before the consumer is the entire operational content
 * of a dependency between two estates, and choudoufu has no config-level edge
 * that says so. `terraformRootSchema` is `{dir, workspace, varFiles,
 * backendConfig, delete}`; there is no `dependsOn` anywhere in the terraform
 * lexicon, and `TerraformApplyOp` takes a single root positionally, so two
 * `TerraformApplyOp`s would be two Ops with nothing between them but a habit.
 *
 * Ordering in chant is phase order inside one Op: phases run in sequence, and
 * a phase that fails ends the run. So one Op takes both roots - `Network`
 * first, `Service` second - and the edge is a fact about this file rather
 * than a convention in a runbook. `chant.config.ts` names the two roots;
 * this file is the only place that says which comes first.
 *
 * ## What happens if the first one fails
 *
 * The `Service` phase never starts. That is the half of the question the
 * phase order answers on its own, and it is the right answer: the consumer's
 * `data "aws_vpc" "network"` resolves the producer's VPC by its marker tags
 * at plan time, so a service plan against a producer that did not apply
 * fails on the read rather than building something against a stale id.
 *
 * The other half is what is left behind, and `onFailure` says it plainly
 * rather than pretending to undo it. Terraform has no rollback: a partial
 * apply is undone by planning and applying the inverse, which is a decision
 * about the estate and not something a generated phase can make. What this
 * project can say for certain is that both roots are idempotent under
 * re-run - prior state on a live root is rebuilt from the live system every
 * time, so a re-run re-reads what actually exists rather than replaying a
 * stale plan - so the remedy is to fix the cause and run the Op again.
 *
 * ## Why each phase still plans to a file and applies that file
 *
 * `apply <planFile>` on a live root does not replay the file. The apply
 * re-plans against the live system, compares its fresh plan with the approved
 * one - same addresses, same actions, same live objects, same planned
 * values - and refuses at exit status 3 when they disagree. Keeping the plan
 * file is therefore what makes the second phase's apply auditable against
 * what the first phase's apply actually did, and `plan.out.planFile` is a
 * reference to the Plan step's own output rather than a path written twice
 * (chant's TF101 fails the build if that pairing is ever spelled by hand).
 */

import { Op, phase, activity } from "@intentius/chant/op";
import { terraformInit, terraformPlan, terraformApply } from "@intentius/chant-lexicon-terraform";

/** The producer's plan, named so its own apply can reference the file it wrote. */
const networkPlan = terraformPlan("network", { id: "network-plan", planFile: "chant.tfplan" });

/** The consumer's plan. Runs only if every step of the Network phase succeeded. */
const servicePlan = terraformPlan("service", { id: "service-plan", planFile: "chant.tfplan" });

export default Op({
  name: "estates-apply",
  overview: "Apply the network estate, then the service estate that reads its VPC",
  labels: {
    Apply: "true",
    // Both roots, in the order they run. One Op, two roots: the label is the
    // only place a reader listing Ops can see that this one is not the
    // single-root shape TerraformApplyOp builds.
    TerraformRoots: "network,service",
  },
  phases: [
    phase("Network", [
      terraformInit("network"),
      networkPlan,
      terraformApply("network", { planFile: networkPlan.out.planFile }),
    ]),
    phase("Service", [
      terraformInit("service"),
      servicePlan,
      terraformApply("service", { planFile: servicePlan.out.planFile }),
    ]),
  ],
  onFailure: [
    phase("Explain", [
      activity("shellCmd", {
        cmd: [
          'echo "estates-apply failed. Nothing was rolled back, and nothing was destroyed."',
          'echo "If the Network phase failed, the Service phase never started: the service root reads the"',
          'echo "producer VPC by its tofu-estate/tofu-address tags at plan time, so it would have failed on"',
          'echo "the read rather than building against a stale id."',
          'echo "If the Service phase failed, the network estate is applied and the service estate is not."',
          'echo "Both roots are idempotent: a live root rebuilds prior state from the live system every run,"',
          'echo "so re-running this Op after fixing the cause re-reads what exists rather than replaying a"',
          'echo "stale plan. Terraform has no automatic rollback; undoing a partial apply means planning and"',
          'echo "applying the inverse, which is a decision about the estate and not one this phase can make."',
        ].join(" && "),
      }),
    ]),
  ],
});
