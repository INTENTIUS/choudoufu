/**
 * Which forge this build targets.
 *
 * The forge is an input to the build rather than something substituted per
 * forge afterwards, and it travels two ways that always agree: `generate.ts`
 * runs once per forge in its own process, and each generated workflow sets
 * `CHANT_FORGE` in its own `env:`, so the Op a runner builds is the Op the
 * workflow was generated from. `examples/ci-pipelines/src/forge.ts` is the
 * long version of this note, including why one process cannot build the same
 * Op two ways (the ESM module cache).
 *
 * This project's two Ops carry no finding mode, so nothing here is
 * forge-conditional in the way ci-pipelines' is: both jobs report and neither
 * posts. What still differs per forge is how a job gets AWS credentials, and
 * that lives in `generate.ts` beside the specs that need it.
 */

export type Forge = "github" | "forgejo" | "gitlab";

export const FORGES: readonly Forge[] = ["github", "forgejo", "gitlab"];

function isForge(raw: string): raw is Forge {
  return (FORGES as readonly string[]).includes(raw);
}

function readForge(): Forge {
  const raw = process.env.CHANT_FORGE ?? "github";
  if (!isForge(raw)) {
    throw new Error(
      `CHANT_FORGE is "${raw}", which is not a forge this project builds for. ` +
        `Set it to ${FORGES.map((f) => `"${f}"`).join(" or ")}, or leave it unset for github.`,
    );
  }
  return raw;
}

/** The forge this build is for. Defaults to github when nothing says otherwise. */
export const forge: Forge = readForge();
