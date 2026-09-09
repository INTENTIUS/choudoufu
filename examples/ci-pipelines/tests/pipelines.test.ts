/**
 * What the checked-in workflows say, read back field by field.
 *
 * These assertions are written from what the generator and the forges
 * promise, not from the bytes that came out: each one names a property a
 * reviewer of this pipeline would want to be true (this job holds no
 * credentials; that one's install is pinned by checksum; the apply's gate
 * does not paint main red), so a change that quietly loses one fails here.
 *
 * `tests/currency.test.ts` is the other half: it proves these files are what
 * `generate.ts` emits today, so asserting over the committed files is
 * asserting over the generator.
 */

import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it } from "node:test";
import { parseYAML } from "@intentius/chant/yaml";

const projectDir = dirname(fileURLToPath(import.meta.url), );
const exampleDir = join(projectDir, "..");

const FORGE_DIR = {
  github: join(exampleDir, "github", ".github", "workflows"),
  forgejo: join(exampleDir, "forgejo", ".forgejo", "workflows"),
} as const;

type Forge = keyof typeof FORGE_DIR;

/**
 * GitLab is not in `FORGE_DIR`: its trigger is job-scoped rather than
 * workflow-scoped (see generate-op-pipeline.ts's module doc in the gitlab
 * lexicon), so `generateOpsPipeline` returns one combined file with every
 * job as a top-level key, not one file per Op with `on:`/`jobs:`/`env:`
 * structure the way GitHub and Forgejo do. Its own `describe` block below
 * parses that shape directly rather than forcing it through `Workflow`.
 */
const GITLAB_FILE = join(exampleDir, "gitlab", "scheduled-ops.gitlab-ci.yml");

/** The five Ops, which are also the five job names. */
const OPS = ["live-adopt", "live-apply", "live-check", "live-discover", "live-plan"] as const;

interface Step {
  id?: string;
  uses?: string;
  run?: string;
  env?: Record<string, string>;
}
interface Job {
  needs?: string;
  if?: string;
  "runs-on"?: string;
  container?: string;
  permissions?: Record<string, string>;
  outputs?: Record<string, string>;
  steps?: Step[];
}
interface Workflow {
  on: Record<string, unknown>;
  env?: Record<string, string>;
  concurrency?: Record<string, unknown>;
  permissions?: Record<string, string>;
  jobs: Record<string, Job>;
}

function text(forge: Forge, op: string): string {
  return readFileSync(join(FORGE_DIR[forge], `${op}.yml`), "utf8");
}

function workflow(forge: Forge, op: string): Workflow {
  return parseYAML(text(forge, op)) as unknown as Workflow;
}

function steps(forge: Forge, op: string): Step[] {
  return workflow(forge, op).jobs[op].steps ?? [];
}

/** The one line that installs choudoufu, wherever the generator put it. */
function installStep(forge: Forge, op: string): Step {
  const step = steps(forge, op).find((s) => s.run?.includes("choudoufu_v"));
  assert.ok(step, `${forge}/${op}: no choudoufu install step`);
  return step;
}

describe("both forges get one workflow per Op, and nothing else", () => {
  for (const forge of Object.keys(FORGE_DIR) as Forge[]) {
    it(`${forge}`, () => {
      assert.deepEqual(
        readdirSync(FORGE_DIR[forge]).sort(),
        OPS.map((op) => `${op}.yml`),
      );
    });
  }
});

describe("the forge a file was generated for is the forge its run rebuilds for", () => {
  // The load-bearing invariant of this project: an Op's finding mode is baked
  // in at build time, so the generated workflow has to hand the runner the
  // same CHANT_FORGE the generator read. If these ever disagree, the
  // permissions the workflow grants describe an Op the runner is not
  // building.
  for (const forge of Object.keys(FORGE_DIR) as Forge[]) {
    for (const op of OPS) {
      it(`${forge}/${op}`, () => {
        assert.equal(workflow(forge, op).env?.CHANT_FORGE, forge);
      });
    }
  }
});

describe("every job installs a pinned choudoufu before it runs an Op", () => {
  for (const forge of Object.keys(FORGE_DIR) as Forge[]) {
    for (const op of OPS) {
      it(`${forge}/${op}`, () => {
        const install = installStep(forge, op);
        // Pinned by version AND by the checksum the release publishes: a tag
        // can be moved and an asset can be replaced, and an unattended run
        // holding a cloud role would never notice.
        assert.match(install.run!, /releases\/download\/v0\.16\.0\/choudoufu_v0\.16\.0_linux_amd64\.tar\.gz/);
        assert.match(install.run!, /sha256sum -c -/);
        assert.match(install.run!, /[0-9a-f]{64}/);
        // chant's lexicon refuses a choudoufu older than v0.14.0.
        assert.ok(!install.run!.includes("latest"), "the install must not float");

        const body = text(forge, op);
        assert.ok(
          body.indexOf("choudoufu_v0.16.0") < body.indexOf(`chant run ${op}`),
          "the install has to precede the invocation, or the Op's first step is a missing binary",
        );
      });
    }
  }
});

describe("live-check reaches for no credential of its own", () => {
  // Its whole point: `choudoufu live-check` makes no cloud calls, reads no
  // state and needs no estate, so it is the cheapest thing a pull request can
  // fail on. A credential appearing here is a regression in what the job is.
  for (const forge of Object.keys(FORGE_DIR) as Forge[]) {
    it(`${forge}/live-check`, () => {
      const body = text(forge, "live-check");
      assert.ok(!body.includes("aws-actions/"), "no role to assume");
      assert.ok(!body.includes("id-token"), "no OIDC token");
      assert.ok(!body.includes("ROLE_ARN"), "no role arn");
    });
  }

  it("github: and sees none either, because OIDC credentials are minted per job", () => {
    // AWS_REGION is here, and is not a credential: it is the one repository
    // variable the provider (`TF_VAR_aws_region`) and choudoufu's own
    // tagging-API sweep (`AWS_REGION`) both read, so the two cannot disagree.
    const env = workflow("github", "live-check").env ?? {};
    assert.deepEqual(Object.keys(env).sort(), ["AWS_REGION", "CHANT_FORGE", "TF_VAR_aws_region"]);
    for (const key of ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"]) {
      assert.ok(!(key in env), `${key} must not reach a job that makes no cloud call`);
    }
  });

  it("forgejo: but does see the estate's static key, because `variables` are workflow-scoped", () => {
    // Not a defect in this example and not something it can dodge: the
    // generator's `variables` become the workflow's top-level `env:`, and a
    // Forgejo job has no OIDC to mint per-job credentials instead. So the
    // no-credentials property this job has on GitHub is a property GitHub
    // provides, not one the pipeline shape provides. Asserted rather than
    // hidden, and named in the README.
    assert.equal(
      workflow("forgejo", "live-check").env?.AWS_ACCESS_KEY_ID,
      "${{ secrets.AWS_ACCESS_KEY_ID }}",
    );
  });

  it("and it runs init first, which is what makes the answer accurate", () => {
    // choudoufu's own help: provider schemas admit types the built-in
    // admission table does not carry, and without them those types read as
    // refused. `init` reaches the registry, not AWS.
    const opSource = readFileSync(join(exampleDir, "src", "live-check.op.ts"), "utf8");
    assert.match(opSource, /phase\("Init", \[terraformInit\(ROOT\)\]\)/);
  });
});

describe("github: the pull-request half observes and posts, and can only read", () => {
  it("live-plan triggers on a pull request against main", () => {
    assert.deepEqual(workflow("github", "live-plan").on, { pull_request: { branches: ["main"] } });
  });

  it("live-plan's permissions are the comment mode's, plus OIDC and nothing else", () => {
    assert.deepEqual(workflow("github", "live-plan").permissions, {
      contents: "read",
      "pull-requests": "write",
      "id-token": "write",
    });
  });

  it("live-plan assumes a plan role, pinned to a release ref rather than a branch", () => {
    const aws = steps("github", "live-plan").find((s) => s.uses?.startsWith("aws-actions/"));
    assert.ok(aws, "the plan job assumes a role");
    assert.match(aws.uses!, /^aws-actions\/configure-aws-credentials@v\d/);
    assert.equal(aws.with?.["role-to-assume"], "${{ vars.CHOUDOUFU_PLAN_ROLE_ARN }}");
  });

  it("live-plan installs gh, because the container image has none and the comment mode shells to it", () => {
    assert.ok(steps("github", "live-plan").some((s) => s.run?.includes("cli/cli/releases/download")));
  });

  it("the two roles that may write are not the role the pull request assumes", () => {
    const roleOf = (op: string) =>
      steps("github", op).find((s) => s.uses?.startsWith("aws-actions/"))?.with?.["role-to-assume"];
    assert.equal(roleOf("live-apply"), "${{ vars.CHOUDOUFU_APPLY_ROLE_ARN }}");
    assert.equal(roleOf("live-adopt"), "${{ vars.CHOUDOUFU_ADOPT_ROLE_ARN }}");
    assert.notEqual(roleOf("live-apply"), roleOf("live-plan"));
    assert.notEqual(roleOf("live-adopt"), roleOf("live-plan"));
  });
});

describe("github: the push half applies behind a gate without painting main red", () => {
  it("live-apply triggers on a push to main", () => {
    assert.deepEqual(workflow("github", "live-apply").on, { push: { branches: ["main"] } });
  });

  it("live-adopt triggers on a push to staging, which is the reconcile position on the dial", () => {
    assert.deepEqual(workflow("github", "live-adopt").on, { push: { branches: ["staging"] } });
  });

  for (const op of ["live-apply", "live-adopt"]) {
    it(`${op} maps only the gated outcome to success`, () => {
      const run = steps("github", op).find((s) => s.run?.includes(`chant run ${op}`));
      assert.ok(run, "the job runs the Op");
      assert.match(run.run!, /--gated-exit 0/);
      // Without pipefail a failing run piped into tee comes back as tee's
      // zero, which would turn a broken apply green - the one thing this
      // mapping must not do.
      assert.match(run.run!, /set -o pipefail/);
    });

    it(`${op} publishes what it stopped on, and a follow-up job says where`, () => {
      const wf = workflow("github", op);
      assert.equal(wf.jobs[op].outputs?.gated, "${{ steps.chant-run.outputs.gated }}");
      const notice = wf.jobs[`${op}-gate-notice`];
      assert.ok(notice, "the pending gate leaves the log");
      assert.equal(notice.needs, op);
      assert.equal(notice.if, `needs.${op}.outputs.gated == 'true'`);
      // Its own permissions, replacing the workflow's for this job alone.
      assert.deepEqual(notice.permissions, {
        contents: "read",
        issues: "write",
        "pull-requests": "write",
      });
    });

    it(`${op} itself may not touch the forge`, () => {
      assert.deepEqual(workflow("github", op).permissions, { contents: "read", "id-token": "write" });
    });
  }
});

describe("github: the sweep runs on the cron the Op declares", () => {
  it("live-discover carries the Op's own schedule, plus a manual dispatch", () => {
    // The cron is on the Op (`schedule: { cron }`), not in generate.ts:
    // `generateOpsPipeline` copies a discovered Op's own cadence onto its
    // spec, so there is one place to change it.
    assert.deepEqual(workflow("github", "live-discover").on, {
      schedule: [{ cron: "0 6 * * *" }],
      workflow_dispatch: {},
    });
  });

  it("and reports as an issue, which is the whole write access it gets", () => {
    assert.deepEqual(workflow("github", "live-discover").permissions, {
      contents: "read",
      issues: "write",
      "id-token": "write",
    });
  });
});

describe("no workflow grants a blanket permission", () => {
  for (const forge of Object.keys(FORGE_DIR) as Forge[]) {
    for (const op of OPS) {
      it(`${forge}/${op}`, () => {
        const body = text(forge, op);
        assert.ok(!body.includes("write-all"), "no blanket write");
        assert.ok(!body.includes("read-all"), "no blanket read");
        assert.ok(!body.includes("contents: write"), "nothing here pushes a branch");
      });
    }
  }
});

describe("forgejo gets the same pipeline minus what its runner cannot do", () => {
  it("carries no permissions: block, because the Forgejo runner ignores one", () => {
    for (const op of OPS) {
      assert.equal(workflow("forgejo", op).permissions, undefined, op);
      assert.ok(!text("forgejo", op).includes("id-token"), `${op}: no OIDC token off Forgejo`);
    }
  });

  it("authenticates with a static secret, stated rather than implied", () => {
    // No OIDC path from Forgejo to AWS is verified anywhere in this
    // organization, so the credentials are a repository secret. They sit in
    // the workflow's top-level env because that is where the generator's
    // `variables` land - workflow-scoped, so even live-check sees them, which
    // is one more reason the OIDC form is better where it exists.
    assert.equal(workflow("forgejo", "live-apply").env?.AWS_ACCESS_KEY_ID, "${{ secrets.AWS_ACCESS_KEY_ID }}");
    assert.equal(
      workflow("forgejo", "live-apply").env?.AWS_SECRET_ACCESS_KEY,
      "${{ secrets.AWS_SECRET_ACCESS_KEY }}",
    );
  });

  it("posts nothing, because every posting mode chant has shells to gh", () => {
    for (const op of OPS) {
      const body = text("forgejo", op);
      assert.ok(!body.includes("cli/cli/releases"), `${op}: no gh install`);
      assert.ok(!body.includes("GH_TOKEN"), `${op}: no gh token`);
    }
  });

  it("drops the gated-apply notice job, and keeps the half that needs no forge API", () => {
    const wf = workflow("forgejo", "live-apply");
    assert.deepEqual(Object.keys(wf.jobs), ["live-apply"]);
    const run = wf.jobs["live-apply"].steps?.find((s) => s.run?.includes("chant run live-apply"));
    assert.match(run!.run!, /--gated-exit 0/);
  });

  it("uses the Forgejo action namespace and its own runner label", () => {
    const wf = workflow("forgejo", "live-check");
    assert.equal(wf.jobs["live-check"]["runs-on"], "docker");
    assert.ok(
      wf.jobs["live-check"].steps?.some((s) => s.uses === "https://code.forgejo.org/actions/checkout@v4"),
    );
  });

  it("still triggers on pull requests and pushes, because the generator reuses github's builder", () => {
    assert.deepEqual(workflow("forgejo", "live-plan").on, { pull_request: { branches: ["main"] } });
    assert.deepEqual(workflow("forgejo", "live-apply").on, { push: { branches: ["main"] } });
    assert.deepEqual(workflow("forgejo", "live-discover").on, {
      schedule: [{ cron: "0 6 * * *" }],
      workflow_dispatch: {},
    });
  });
});

// ---------------------------------------------------------------------------
// GitLab: one combined file, one job per Op, no on:/jobs:/permissions: at all.
// ---------------------------------------------------------------------------

interface GitlabJob {
  stage?: string;
  image?: string;
  resource_group?: string;
  id_tokens?: Record<string, { aud: string }>;
  environment?: { name: string; url?: string };
  variables?: Record<string, string>;
  rules?: { if: string }[];
  script?: string[];
  artifacts?: { when: string; paths: string[]; expire_in: string };
}

function gitlabDoc(): Record<string, unknown> {
  return parseYAML(readFileSync(GITLAB_FILE, "utf8")) as unknown as Record<string, unknown>;
}

function gitlabJob(op: string): GitlabJob {
  const job = gitlabDoc()[op];
  assert.ok(job, `gitlab: no job named ${op}`);
  return job as GitlabJob;
}

describe("gitlab: one job per Op, in the one file the generator emits", () => {
  it("is exactly the five Ops, plus stages: and variables:", () => {
    const doc = gitlabDoc();
    const jobs = Object.keys(doc).filter((key) => !["stages", "variables"].includes(key));
    assert.deepEqual(jobs.sort(), [...OPS].sort());
  });

  it("builds every job's Ops with CHANT_FORGE: gitlab", () => {
    assert.equal((gitlabDoc().variables as Record<string, string>).CHANT_FORGE, "gitlab");
  });

  for (const op of OPS) {
    it(`${op}: installs a pinned choudoufu before it runs the Op`, () => {
      const script = gitlabJob(op).script ?? [];
      const install = script.find((line) => line.includes("choudoufu_v"));
      assert.ok(install, `${op}: no choudoufu install line`);
      assert.match(install, /releases\/download\/v0\.16\.0\/choudoufu_v0\.16\.0_linux_amd64\.tar\.gz/);
      assert.match(install, /sha256sum -c -/);
      assert.match(install, /[0-9a-f]{64}/);
      assert.ok(!install.includes("latest"), "the install must not float");
      assert.ok(
        script.some((line) => line.includes(`npx chant run ${op}`)),
        `${op}: does not run its own Op`,
      );
    });
  }
});

describe("gitlab: live-check reaches for no credential of its own", () => {
  it("no id_tokens, no role variable, no AWS_ROLE_ARN export", () => {
    const job = gitlabJob("live-check");
    assert.equal(job.id_tokens, undefined, "no OIDC token");
    const script = (job.script ?? []).join("\n");
    assert.ok(!script.includes("ROLE_ARN"), "no role arn");
    assert.ok(!script.includes("AWS_WEB_IDENTITY_TOKEN_FILE"), "no web-identity exchange");
  });

  it("and runs init first, which is what makes the answer accurate", () => {
    const opSource = readFileSync(join(exampleDir, "src", "live-check.op.ts"), "utf8");
    assert.match(opSource, /phase\("Init", \[terraformInit\(ROOT\)\]\)/);
  });
});

describe("gitlab: live-plan posts a merge-request note, over OIDC, with no gh install", () => {
  it("triggers on a merge request targeting main", () => {
    assert.deepEqual(gitlabJob("live-plan").rules, [
      { if: '$CI_PIPELINE_SOURCE == "merge_request_event" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == "main"' },
    ]);
  });

  it("declares an id_tokens: entry for the plan role's OIDC exchange", () => {
    assert.deepEqual(gitlabJob("live-plan").id_tokens, { CHANT_ID_TOKEN: { aud: "$CI_SERVER_URL" } });
  });

  it("assumes the plan role by writing the JWT to a file and exporting AWS_ROLE_ARN", () => {
    const script = gitlabJob("live-plan").script!.join("\n");
    assert.match(script, /AWS_WEB_IDENTITY_TOKEN_FILE="\$CI_PROJECT_DIR\/\.chant-id-token\.jwt"/);
    assert.match(script, /AWS_ROLE_ARN="\$CHOUDOUFU_PLAN_ROLE_ARN"/);
  });

  it("installs no gh: chant's GitLab comment mode is a plain REST call, not a shell-out", () => {
    const script = gitlabJob("live-plan").script!.join("\n");
    assert.ok(!script.includes("cli/cli/releases"), "no gh install");
  });

  it("the two write roles are not the role the merge-request job assumes", () => {
    const roleOf = (op: string) => {
      const m = gitlabJob(op).script!.join("\n").match(/AWS_ROLE_ARN="\$(\w+)"/);
      return m?.[1];
    };
    assert.equal(roleOf("live-apply"), "CHOUDOUFU_APPLY_ROLE_ARN");
    assert.equal(roleOf("live-adopt"), "CHOUDOUFU_ADOPT_ROLE_ARN");
    assert.notEqual(roleOf("live-apply"), roleOf("live-plan"));
    assert.notEqual(roleOf("live-adopt"), roleOf("live-plan"));
  });
});

describe("gitlab: the push half applies behind a gate without painting the pipeline red", () => {
  it("live-apply triggers on a push to main; live-adopt on a push to staging", () => {
    assert.deepEqual(gitlabJob("live-apply").rules, [
      { if: '$CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH == "main"' },
    ]);
    assert.deepEqual(gitlabJob("live-adopt").rules, [
      { if: '$CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH == "staging"' },
    ]);
  });

  it("live-apply is bound to the production environment", () => {
    assert.deepEqual(gitlabJob("live-apply").environment, { name: "production" });
  });

  it("no other job declares an environment: GitLab protects one job, not the pipeline", () => {
    for (const op of OPS) {
      if (op === "live-apply") continue;
      assert.equal(gitlabJob(op).environment, undefined, op);
    }
  });

  for (const op of ["live-apply", "live-adopt"]) {
    it(`${op} maps only the gated outcome to success, and writes the pending gate to an artifact`, () => {
      const job = gitlabJob(op);
      const run = job.script!.find((line) => line.includes(`chant run ${op}`));
      assert.ok(run, "the job runs the Op");
      assert.match(run, /--gated-exit 0/);
      assert.equal(job.variables?.CHANT_GATE_SUMMARY, `chant-gate-${op}.md`);
      assert.deepEqual(job.artifacts, {
        when: "always",
        paths: [`chant-gate-${op}.md`],
        expire_in: "30 days",
      });
    });
  }
});

describe("gitlab: the sweep runs on the cron the Op declares, and only reports", () => {
  it("live-discover is gated to a Pipeline Schedule, not to a branch or merge-request event", () => {
    assert.deepEqual(gitlabJob("live-discover").rules, [
      { if: '$CI_PIPELINE_SOURCE == "schedule" && $CHANT_SCHEDULED_OP == "live-discover"' },
    ]);
  });

  it("carries no findingMode-comment machinery: report needs no merge request to post on", () => {
    // Asserted the same way GitHub's live-discover is asserted to be
    // "issue"-mode by its own permissions in the section above: here it is
    // the absence of a merge-request rule and the absence of a gh install,
    // since a cron job's finding mode is "report" wherever it is not GitHub.
    const script = gitlabJob("live-discover").script!.join("\n");
    assert.ok(!script.includes("cli/cli/releases"), "no gh install");
  });
});

describe("gitlab: no job's setup step is a GitHub Actions marketplace action", () => {
  it("every script line is a shell command, never a uses:", () => {
    for (const op of OPS) {
      assert.ok(!("uses" in gitlabJob(op)), `${op}: gitlab has no uses: step shape`);
    }
  });
});
