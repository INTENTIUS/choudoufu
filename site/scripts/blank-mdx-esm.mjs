// Blanks MDX ESM lines (`import ...` and `export ...`) outside code fences so
// the prose lint does not read module syntax as sentences. Line numbers are
// kept: each blanked line becomes an empty line. A statement that spans lines
// is blanked until its brackets balance and, for an import, its `from "x"`
// has been seen.
export function blankMdxEsm(src) {
  const lines = src.split("\n");
  let fence = null;
  let open = null; // { kind, depth } while inside a multi-line statement
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (open === null) {
      const f = /^\s{0,3}(`{3,}|~{3,})/.exec(line);
      if (f) {
        if (fence === null) fence = f[1][0];
        else if (f[1][0] === fence) fence = null;
        continue;
      }
      if (fence !== null) continue;
      const m = /^(import|export)\s/.exec(line);
      if (!m) continue;
      open = { kind: m[1], depth: 0, from: false };
    }
    lines[i] = "";
    open.depth += (line.match(/[{(\[]/g) ?? []).length - (line.match(/[})\]]/g) ?? []).length;
    if (/^import\s+["']|\bfrom\s+["']/.test(line)) open.from = true;
    if (open.depth <= 0 && (open.kind === "export" || open.from)) open = null;
  }
  return lines.join("\n");
}
