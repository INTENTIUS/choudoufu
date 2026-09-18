/**
 * `estates-plan`: the read-only half, on a pull request. Same two roots, same
 * order, nothing that writes.
 *
 * `choudoufuLivePlan` rather than `terraformPlan`: on a live root a plan
 * answers three questions off one read, because ownership is a pair of tags
 * on the resources rather than an entry in a state file - drift, resources
 * sitting at a declared identity with no marker, and the subset of those an
 * exact content match makes adoptable.
 *
 * The order matters here for the same reason it matters in `estates-apply`,
 * and it is where a reader can watch the dependency behave. Plan the service
 * root against an account where the network estate has never been applied and
 * the run fails on `data "aws_vpc" "network"` - no matching VPC - rather than
 * quietly planning a subnet with an empty `vpc_id`. There is no cached value
 * to fall back on, which is the property this whole example exists to show.
 *
 * No `findingMode`, so both jobs generate at chant's default `"report"`: this
 * Op prints its plan and posts nothing. Posting a plan comment on the pull
 * request is examples/ci-pipelines' `live-plan`, and adding it here would
 * bring that project's whole per-forge finding-mode table along with it
 * without teaching anything new about cross-estate ordering.
 */

import { Op, phase } from "@intentius/chant/op";
import { choudoufuLivePlan } from "@intentius/chant-lexicon-terraform";

const networkPlan = choudoufuLivePlan("network", { id: "network-live-plan" });
networkPlan.outcomeAttribute = { name: "NetworkDrift", from: "drift" };

const servicePlan = choudoufuLivePlan("service", { id: "service-live-plan" });
servicePlan.outcomeAttribute = { name: "ServiceDrift", from: "drift" };

export default Op({
  name: "estates-plan",
  overview: "Live-plan the network estate, then the service estate that reads its VPC",
  labels: {
    Watch: "true",
    TerraformRoots: "network,service",
  },
  phases: [phase("Network", [networkPlan]), phase("Service", [servicePlan])],
});
