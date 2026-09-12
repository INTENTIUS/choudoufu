/**
 * The two roots, read back field by field off their own HCL.
 *
 * What can rot here is quiet. The coupling between the two estates is a pair
 * of strings - the producer's estate name and its resource address, restated
 * as filter values in the consumer - and nothing in the language checks that
 * they agree. Rename `aws_vpc.main` to `aws_vpc.core` in terraform/network
 * and every tool stays green until the day someone applies the service root
 * against a real account and the data source finds no VPC. So this file is
 * the join, the way `live/pipeline_governance_test.go` is the join between a
 * policy's job names and a generator's.
 *
 * It also holds the two absences the example exists to demonstrate, because
 * an absence is exactly the thing a later edit restores without noticing: no
 * `output` block in the producer, and no `terraform_remote_state` anywhere.
 *
 * Reads HCL through `@cdktf/hcl2json`, which is why this project declares it
 * as a devDependency rather than inheriting it: it is a devDependency of
 * chant's terraform lexicon, so a standalone project that parses HCL - or
 * runs `chant build`, which does - needs it named here.
 */

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";
import { parse } from "@cdktf/hcl2json";

const exampleDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const networkDir = join(exampleDir, "terraform", "network");
const serviceDir = join(exampleDir, "terraform", "service");

async function hcl(dir: string, file: string): Promise<any> {
  const path = join(dir, file);
  return parse(path, readFileSync(path, "utf8"));
}

/** The `estate` value out of a root's `estate.chdf.hcl` sidecar. */
async function estateOf(dir: string): Promise<string> {
  const sidecar = await hcl(dir, "estate.chdf.hcl");
  assert.ok(typeof sidecar.estate === "string", `${dir}/estate.chdf.hcl declares no estate`);
  return sidecar.estate as string;
}

/** One `filter` block's `values`, keyed by its `name`, off a data-source body. */
function filtersOf(body: any): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const filter of body.filter ?? []) out[filter.name] = filter.values;
  return out;
}

describe("two estates, not one", () => {
  it("each root declares its own estate, and the two differ", async () => {
    const network = await estateOf(networkDir);
    const service = await estateOf(serviceDir);
    assert.equal(network, "cross-estate-network");
    assert.equal(service, "cross-estate-service");
    assert.notEqual(
      network,
      service,
      "both roots claim the same estate. An estate is the unit of ownership and the sweep is " +
        "estate-scoped, so each root's plan would see the other's resources as undeclared_tagged " +
        "and propose destroying every one of them. See the README's 'Why two estates'.",
    );
  });

  it("neither root declares a backend or a cloud block", async () => {
    for (const [name, dir] of [["network", networkDir], ["service", serviceDir]] as const) {
      const main = await hcl(dir, "main.tf");
      const terraform = (main.terraform ?? [])[0] ?? {};
      assert.equal(terraform.backend, undefined, `${name} declares a backend block; TF024 refuses one on a live root`);
      assert.equal(terraform.cloud, undefined, `${name} declares a cloud block; TF024 refuses one on a live root`);
    }
  });
});

describe("the producer publishes nothing", () => {
  it("terraform/network declares no output block at all", async () => {
    const main = await hcl(networkDir, "main.tf");
    assert.equal(
      main.output,
      undefined,
      "the producer grew an output block. The whole point of live/OUTPUTS.md's decision is that it " +
        "needs none: the consumer reads the live resource, so there is nothing to publish and - since " +
        "a live root writes no state file - nothing that could read a published value back.",
    );
  });

  it("and declares exactly the one resource the consumer reads", async () => {
    const main = await hcl(networkDir, "main.tf");
    assert.deepEqual(Object.keys(main.resource ?? {}), ["aws_vpc"]);
    assert.deepEqual(Object.keys(main.resource.aws_vpc), ["main"]);
  });
});

describe("the consumer reads the live resource, not a stored copy", () => {
  it("its data source is an aws_vpc filtered on the producer's own marker pair", async () => {
    const main = await hcl(serviceDir, "main.tf");
    const vpc = main.data?.aws_vpc?.network?.[0];
    assert.ok(vpc, "terraform/service declares no data \"aws_vpc\" \"network\"");

    const filters = filtersOf(vpc);
    assert.deepEqual(
      Object.keys(filters).sort(),
      ["tag:tofu-address", "tag:tofu-estate"],
      "the data source filters on something other than the marker pair. Both tags are already written " +
        "on every managed resource in a live root; a naming convention invented on top of them is a " +
        "second source of truth.",
    );
  });

  it("and the filter values are the producer's estate and its VPC's address", async () => {
    const main = await hcl(serviceDir, "main.tf");
    const filters = filtersOf(main.data.aws_vpc.network[0]);

    // The variables carry the values; the filters carry the references. Both
    // halves are checked, because either one drifting breaks the read.
    assert.deepEqual(filters["tag:tofu-estate"], ["${var.network_estate}"]);
    assert.deepEqual(filters["tag:tofu-address"], ["${var.network_vpc_address}"]);

    const vars = main.variable ?? {};
    assert.equal(
      vars.network_estate[0].default,
      await estateOf(networkDir),
      "var.network_estate's default is not the producer's own estate name. Nothing in the language " +
        "ties the two together, so this assertion is the tie.",
    );
    assert.equal(
      vars.network_vpc_address[0].default,
      "aws_vpc.main",
      "var.network_vpc_address's default is not the producer's VPC address. tofu-address is the " +
        "configuration address choudoufu stamps on the resource, so renaming the producer's block " +
        "changes it.",
    );

    // ...and that address really is a block in the producer.
    const producer = await hcl(networkDir, "main.tf");
    const [type, name] = (vars.network_vpc_address[0].default as string).split(".");
    assert.ok(
      producer.resource?.[type]?.[name],
      `var.network_vpc_address names ${type}.${name}, which the producer does not declare`,
    );
  });

  it("and the subnet takes its vpc_id from that data source", async () => {
    const main = await hcl(serviceDir, "main.tf");
    const subnet = main.resource?.aws_subnet?.app?.[0];
    assert.ok(subnet, "terraform/service declares no aws_subnet.app");
    assert.equal(subnet.vpc_id, "${data.aws_vpc.network.id}");
  });

  it("and no root reads a state file through terraform_remote_state", async () => {
    for (const [name, dir] of [["network", networkDir], ["service", serviceDir]] as const) {
      const main = await hcl(dir, "main.tf");
      assert.equal(
        main.data?.terraform_remote_state,
        undefined,
        `${name} reads terraform_remote_state. choudoufu no longer refuses it by lint - the rule that ` +
          "did was removed under #179 stage 3 - which makes this worse, not better: a live producer " +
          "writes no state file, so a remote-state read that still resolves is reading a snapshot " +
          "frozen at migration time, with real-looking values and no marker on the file to say so.",
      );
    }
  });
});
