// Prose lint for the site, on the `sentences` trope ruleset (modelled on
// chant's scripts/lint-docs-sentences.mjs). It reads every page under
// src/content/docs, the string literals of src/data/rooms.ts and the repo
// README.md.
//
// A ratchet: findings are counted per (file, rule) in prose-baseline.json
// and the lint fails only when a count grows. When counts drop, rerun with
// --update-baseline and commit the smaller file.
//
//   node scripts/lint-prose.mjs                    # gate (CI)
//   node scripts/lint-prose.mjs --verbose          # every finding, file:line
//   node scripts/lint-prose.mjs --update-baseline
//
// medium and high severities gate; `reframe` ("It is not X. It is Y.")
// gates at any severity, as in chant.
import { readFileSync, writeFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { RULES } from "sentences/lint/registry";
import { runRules } from "sentences/lint/engine";
import { buildDocAnalysis } from "sentences/lint/build-doc";
import { extractProse } from "sentences/lint/markdown-prose";

const here = dirname(fileURLToPath(import.meta.url));
const REPO = join(here, "..", "..");
const DOCS_ROOT = join(here, "..", "src", "content", "docs");
const ROOMS = join(here, "..", "src", "data", "rooms.ts");
const README = join(REPO, "README.md");
const BASELINE = join(here, "prose-baseline.json");
const GATED_SEVERITIES = new Set(["medium", "high"]);
const ALWAYS_GATED_RULES = new Set(["reframe"]);

const verbose = process.argv.includes("--verbose");
const updateBaseline = process.argv.includes("--update-baseline");

function docFiles(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) out.push(...docFiles(p));
    else if (/\.(md|mdx)$/.test(entry)) out.push(p);
  }
  return out.sort();
}

// Same literal match as live/site_size_test.go's roomsStringLiteral.
const LITERAL = /'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"|`([^`]*)`/g;

// rooms.ts as prose: each string literal with a space in it becomes its own
// paragraph. lines[i] is the source line of prose line i+1.
function roomsProse(src) {
  const code = src
    .split("\n")
    .map((l) => (l.trim().startsWith("//") ? "" : l))
    .join("\n");
  const parts = [];
  const lines = [];
  for (const m of code.matchAll(LITERAL)) {
    const lit = (m[1] ?? "") + (m[2] ?? "") + (m[3] ?? "");
    if (!lit.trim().includes(" ")) continue;
    const start = code.slice(0, m.index).split("\n").length;
    lit.split("\n").forEach((_, i) => lines.push(start + i));
    lines.push(start);
    parts.push(lit);
  }
  return { prose: parts.join("\n\n"), lines };
}

function lineOf(text, offset) {
  return text.slice(0, offset).split("\n").length;
}

// Anaphora fires on markup: consecutive bullets, link lists and headings
// "open" with "-", "[", "###" or ",". An opening with no letter in it is not
// a word, so those findings are dropped.
function markupAnaphora(f) {
  if (f.ruleId !== "anaphora/repeated-opening") return false;
  const m = /“([^”]*)”/.exec(f.message);
  return m !== null && !/\p{L}/u.test(m[1]);
}

const sources = docFiles(DOCS_ROOT).map((file) => {
  const prose = extractProse(readFileSync(file, "utf8"));
  return { rel: relative(REPO, file), prose, line: (o) => lineOf(prose, o) };
});
{
  const { prose, lines } = roomsProse(readFileSync(ROOMS, "utf8"));
  sources.push({ rel: relative(REPO, ROOMS), prose, line: (o) => lines[lineOf(prose, o) - 1] });
}
{
  const prose = extractProse(readFileSync(README, "utf8"));
  sources.push({ rel: "README.md", prose, line: (o) => lineOf(prose, o) });
}

const current = {}; // file -> rule -> gated count
let total = 0;
let gated = 0;
const detail = [];

for (const { rel, prose, line } of sources) {
  const { findings, errors } = runRules(RULES, buildDocAnalysis(prose));
  for (const e of errors) detail.push(`${rel}: rule ${e.ruleId} errored: ${e.message}`);
  for (const f of findings) {
    if (markupAnaphora(f)) continue;
    total++;
    if (verbose) detail.push(`${rel}:${line(f.span.start)} [${f.severity}] ${f.ruleId}: ${f.message}`);
    if (!GATED_SEVERITIES.has(f.severity) && !ALWAYS_GATED_RULES.has(f.ruleId)) continue;
    gated++;
    const perFile = (current[rel] ??= {});
    perFile[f.ruleId] = (perFile[f.ruleId] ?? 0) + 1;
  }
}

for (const l of detail) console.log(l);

// Hedge and apology phrasing. No baseline: one hit fails. It reads the same
// prose as above (code, URLs and front matter blanked) for every page and
// for rooms.ts, minus rooms.ts's LIMITS list, which states limits by design.
// The last five came after #2130's GCP pages shipped "None has a dated run
// yet." and "report which GCP claims are open"; a sentence ending in "yet" is
// matched on its own, since "\b" cannot close after the full stop.
const HEDGE = /\b(not yet|unproven|not measured|limitations?|caveats?|unfortunately|does not support|cannot yet|is refused|we don't|still needs|stale|dated run yet|claims are open|none has a dated run|cannot reach)\b|\byet\.(?!\w)/gi;
const hedges = [];
for (const { rel, prose, line } of sources) {
  if (rel === "README.md") continue;
  let text = prose;
  if (rel.endsWith("rooms.ts")) text = blankLimits(readFileSync(ROOMS, "utf8"), prose);
  for (const m of text.matchAll(HEDGE)) hedges.push(`${rel}:${line(m.index)}: "${m[0]}"`);
}
// The front matter too: the page shows its description, and its prompt sits
// under the title. extractProse blanks both.
for (const file of docFiles(DOCS_ROOT)) {
  const src = readFileSync(file, "utf8");
  const fm = /^---\n([\s\S]*?)\n---/.exec(src);
  if (!fm) continue;
  for (const m of fm[1].matchAll(HEDGE)) hedges.push(`${relative(REPO, file)}:${lineOf(src, m.index + 4)}: "${m[0]}" (front matter)`);
}
if (hedges.length > 0) {
  console.error(`prose lint: ${hedges.length} hedge or apology phrase(s); state what the product does:`);
  for (const h of hedges) console.error(`  ${h}`);
  process.exit(1);
}

// Cross-platform pointers. Each platform page states its own content, so a
// sentence that only defers to another platform's page fails. It reads the
// raw page source, since link text and targets matter here.
const POINTER = /\b(as on \[AWS\]|as on AWS|same as AWS|as on Kubernetes)(?!\w)/gi;
const pointers = [];
for (const file of docFiles(DOCS_ROOT)) {
  const src = readFileSync(file, "utf8");
  for (const m of src.matchAll(POINTER)) pointers.push(`${relative(REPO, file)}:${lineOf(src, m.index)}: "${m[0]}"`);
}
if (pointers.length > 0) {
  console.error(`prose lint: ${pointers.length} cross-platform pointer(s); state the content on this page:`);
  for (const p of pointers) console.error(`  ${p}`);
  process.exit(1);
}

// rooms.ts prose with the LIMITS entries' literals blanked, offsets kept.
function blankLimits(src, prose) {
  const start = src.indexOf("export const LIMITS");
  if (start < 0) return prose;
  const end = src.indexOf("];", start);
  let out = prose;
  for (const m of src.slice(start, end).matchAll(LITERAL)) {
    const lit = (m[1] ?? "") + (m[2] ?? "") + (m[3] ?? "");
    if (lit.includes(" ")) out = out.replace(lit, " ".repeat(lit.length));
  }
  return out;
}

if (updateBaseline) {
  writeFileSync(BASELINE, JSON.stringify(current, null, 1) + "\n");
  console.log(`prose lint: baseline written, ${gated} gated finding(s) in ${Object.keys(current).length} file(s) (${total} incl. low tiers)`);
  process.exit(0);
}

let baseline;
try {
  baseline = JSON.parse(readFileSync(BASELINE, "utf8"));
} catch {
  console.error(`prose lint: no baseline at ${relative(process.cwd(), BASELINE)}; run with --update-baseline and commit it`);
  process.exit(1);
}

const regressions = [];
for (const [file, rules] of Object.entries(current)) {
  for (const [rule, count] of Object.entries(rules)) {
    const allowed = baseline[file]?.[rule] ?? 0;
    if (count > allowed) regressions.push(`${file}: ${rule} ${allowed} -> ${count}`);
  }
}
const baselineTotal = Object.values(baseline).reduce(
  (n, rules) => n + Object.values(rules).reduce((m, c) => m + c, 0),
  0,
);

if (regressions.length > 0) {
  console.error(`prose lint: ${regressions.length} regression(s) against the baseline:`);
  for (const r of regressions) console.error(`  ${r}`);
  console.error("Fix the prose (--verbose gives file:line), or for a deliberate choice run --update-baseline and commit.");
  process.exit(1);
}

if (gated < baselineTotal) {
  console.log(`prose lint: ${gated} gated finding(s), no regressions, ${baselineTotal - gated} below the baseline of ${baselineTotal}; tighten it with --update-baseline`);
} else {
  console.log(`prose lint: ${gated} gated finding(s), no regressions (${baselineTotal} baselined)`);
}
