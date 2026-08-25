# live/e2e/lib/strip-output-blocks.py: usage: python3 strip-output-blocks.py
# <file.tf> <needle>. Removes every top-level `output "..." { ... }` block
# in <file.tf> whose body contains the literal string <needle>, leaving
# every other output block untouched. Used by day2_remove crossings that
# delete a whole module block: the module's own outputs would otherwise
# reference an undeclared module and break `terraform plan`/`init` outright,
# and this is the surgical alternative to truncating the whole outputs.tf
# file when other modules' outputs are still worth keeping in the plan.
import re, sys
path, needle = sys.argv[1], sys.argv[2]
with open(path) as f:
    content = f.read()
# match output "name" { ... } blocks (non-greedy, balanced braces assumed simple: no nested { in body except interpolations which don't start a line as brace)
blocks = re.findall(r'output "[^"]+" \{.*?\n\}\n', content, re.S)
removed = 0
for b in blocks:
    if needle in b:
        content = content.replace(b, '', 1)
        removed += 1
with open(path, 'w') as f:
    f.write(content)
print(f"removed {removed} output block(s) referencing {needle}")
