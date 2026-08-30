// Prose lint over this fork's hand-written Markdown.
//
// REPORT ONLY. This script always exits 0 on findings, and nothing in `just ci`
// calls it. It is a writing aid, not a gate. Making it a gate is a separate
// decision that needs a baseline first, which is what `-json` is for.
//
//   node prose-lint.mjs                 # every tracked page, summary per file
//   node prose-lint.mjs path/to/page.md # one file, every finding located
//   node prose-lint.mjs -json           # machine-readable, for a future ratchet
//
// The rules come from the `sentences` package, whose published tarball is the
// `dist-lint` subsystem only: a deterministic linter for AI-writing tropes over
// a constituency parse. Its single runtime dependency is `compromise`, pure JS,
// no network at lint time. rfc/20260830-stale-state-charter.md carries the
// justification for the dependency.
//
// Two things this script does that the package does not:
//
//   - Blanks YAML front matter. `extractProse` blanks fenced code, tables and
//     HTML comments, but not front matter, so `title: "..."` was being read as
//     prose and reported as a rhetorical colon on every Hugo page.
//   - Blanks Hugo shortcodes ({{< relref >}}, {{% hint %}}), which are markup.
//
// Both replace with spaces rather than deleting, so reported offsets stay true
// to the source file.

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { extractProse } from "sentences/lint/markdown-prose";
import { buildDocAnalysis } from "sentences/lint/build-doc";
import { RULES } from "sentences/lint/registry";
import { runRules } from "sentences/lint/engine";
import { buildReport } from "sentences/lint/report";

const ROOT = new URL(".", import.meta.url).pathname.replace(/\/$/, "");

// Hand-written prose only. A generated page's findings are the generator's to
// answer for, and editing the output loses them on the next render.
const ROOTS = ["site/content/docs", "live", "rfc"];
const SKIP = [
  "site/content/docs/progress/", // rendered by tools/gauntlet
  "site/themes/",
  "node_modules/",
];
// Inherited upstream OpenTofu RFCs. Historical documents, not this fork's prose.
const INHERITED_RFC = /^rfc\/20(2[3-5])/;

const blank = (s) => " ".repeat(s.length);

function proseOf(text) {
  return extractProse(text)
    .replace(/^---\n[\s\S]*?\n---\n/, blank) // YAML front matter
    .replace(/\{\{[<%][\s\S]*?[>%]\}\}/g, blank); // Hugo shortcodes
}

function lint(text) {
  const prose = proseOf(text);
  const { findings, errors } = runRules(RULES, buildDocAnalysis(prose));
  return buildReport(prose, findings, errors, RULES);
}

function* walk(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) yield* walk(path);
    else if (name.endsWith(".md")) yield path;
  }
}

function tracked() {
  const out = [];
  for (const r of ROOTS) {
    let st;
    try {
      st = statSync(join(ROOT, r));
    } catch {
      continue;
    }
    if (!st.isDirectory()) continue;
    for (const path of walk(join(ROOT, r))) {
      const rel = relative(ROOT, path);
      if (SKIP.some((s) => rel.startsWith(s))) continue;
      if (INHERITED_RFC.test(rel)) continue;
      out.push(rel);
    }
  }
  return out.sort();
}

// A finding's span is a character offset into the prose, which shares the
// source file's offsets because every strip above preserves length.
function lineOf(text, offset) {
  let line = 1;
  for (let i = 0; i < offset && i < text.length; i++) if (text[i] === "\n") line++;
  return line;
}

const args = process.argv.slice(2);
const asJSON = args.includes("-json");
const files = args.filter((a) => a !== "-json");

if (files.length === 1) {
  const rel = relative(ROOT, join(process.cwd(), files[0]));
  const text = readFileSync(files[0], "utf8");
  const report = lint(text);
  if (asJSON) {
    console.log(JSON.stringify({ file: rel, ...report }, null, 2));
  } else {
    console.log(`${rel}  ${report.wordCount} words  ${report.counts.findings} findings  score ${report.score.total.toFixed(1)}`);
    for (const f of report.findings) {
      console.log(`  ${rel}:${lineOf(text, f.span.start)}  ${f.ruleId}  ${f.message}`);
    }
    for (const e of report.errors) console.log(`  RULE ERROR ${e.ruleId}: ${e.message}`);
  }
  process.exit(0);
}

const rows = [];
for (const rel of files.length ? files.map((f) => relative(ROOT, join(process.cwd(), f))) : tracked()) {
  const report = lint(readFileSync(join(ROOT, rel), "utf8"));
  rows.push({
    file: rel,
    words: report.wordCount,
    findings: report.counts.findings,
    score: report.score.total,
    byRule: report.counts.byRule,
    errors: report.errors,
  });
}

if (asJSON) {
  console.log(JSON.stringify({ version: 1, rules: RULES.length, files: rows }, null, 2));
  process.exit(0);
}

rows.sort((a, b) => b.score - a.score);
console.log(`${rows.length} files, ${RULES.length} rules. Report only; nothing here fails a build.\n`);
console.log("score  findings  words  file");
for (const r of rows) {
  console.log(
    `${r.score.toFixed(1).padStart(5)}  ${String(r.findings).padStart(8)}  ${String(r.words).padStart(5)}  ${r.file}`,
  );
}
const total = rows.reduce((n, r) => n + r.findings, 0);
const byRule = {};
for (const r of rows) for (const [id, n] of Object.entries(r.byRule)) byRule[id] = (byRule[id] ?? 0) + n;
console.log(`\n${total} findings across ${rows.length} files. By rule:`);
for (const [id, n] of Object.entries(byRule).sort((a, b) => b[1] - a[1])) {
  console.log(`  ${String(n).padStart(4)}  ${id}`);
}
console.log("\nOne file in full:  node prose-lint.mjs site/content/docs/model/plan-cost.md");
