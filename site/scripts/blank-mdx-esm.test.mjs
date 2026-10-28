// node --test scripts/blank-mdx-esm.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { blankMdxEsm } from "./blank-mdx-esm.mjs";

test("single-quoted import lines are blanked, line numbers kept", () => {
  const src = "---\ntitle: T\n---\nimport A from '../a.astro';\nimport B from '../b.astro';\n\nBody text.\n";
  const out = blankMdxEsm(src);
  assert.equal(out.split("\n").length, src.split("\n").length);
  assert.equal(out.split("\n")[3], "");
  assert.equal(out.split("\n")[4], "");
  assert.match(out, /Body text\./);
});

test("multi-line import and export are blanked through their end", () => {
  const src = "import {\n  a,\n  b,\n} from 'x';\nexport const k = {\n  v: 1,\n};\nAfter.\n";
  assert.equal(blankMdxEsm(src), "\n\n\n\n\n\n\nAfter.\n");
});

test("code fences and prose after imports are untouched", () => {
  const src = "import A from 'a';\n\n```js\nimport B from 'b';\nexport const x = 1;\n```\n\nPlain prose after the imports.\n";
  const out = blankMdxEsm(src).split("\n");
  assert.equal(out[0], "");
  assert.equal(out[3], "import B from 'b';");
  assert.equal(out[4], "export const x = 1;");
  assert.equal(out[7], "Plain prose after the imports.");
});
