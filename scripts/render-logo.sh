#!/usr/bin/env bash
# Render PNG versions of the choudoufu logo from the source SVG.
# Requires rsvg-convert (brew install librsvg).
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
src="$repo_root/docs/images/choudoufu.svg"
out="$repo_root/docs/images"

command -v rsvg-convert >/dev/null || {
  echo "rsvg-convert not found; install with: brew install librsvg" >&2
  exit 1
}

render() {
  local size=$1 name=$2
  rsvg-convert -w "$size" -h "$size" "$src" -o "$out/$name"
  echo "rendered $name (${size}x${size})"
}

# favicon sizes for the browser tab
render 16 choudoufu-favicon-16.png
render 32 choudoufu-favicon-32.png
render 48 choudoufu-favicon-48.png

# docs inline sizes
render 64 choudoufu-inline-64.png
render 128 choudoufu-inline-128.png
render 256 choudoufu-docs.png

# hero size for the intro / README header, plus a smaller
# inline version for doc layouts
render 1024 choudoufu-hero.png
render 512 choudoufu-hero-inline.png

# pack the favicon PNGs into a single favicon.ico (ICO files may carry
# PNG-encoded entries, so no ImageMagick needed)
python3 - "$out" <<'EOF'
import struct, sys, os
out = sys.argv[1]
sizes = [16, 32, 48]
blobs = [open(os.path.join(out, f"choudoufu-favicon-{s}.png"), "rb").read() for s in sizes]
offset = 6 + 16 * len(sizes)
with open(os.path.join(out, "favicon.ico"), "wb") as f:
    f.write(struct.pack("<HHH", 0, 1, len(sizes)))
    for s, blob in zip(sizes, blobs):
        f.write(struct.pack("<BBBBHHII", s, s, 0, 0, 1, 32, len(blob), offset))
        offset += len(blob)
    for blob in blobs:
        f.write(blob)
EOF
echo "packed favicon.ico"

# the docs site embeds its assets from site/static/
for f in choudoufu-favicon-16.png choudoufu-favicon-32.png \
         choudoufu-favicon-48.png choudoufu-inline-64.png \
         choudoufu-hero.png favicon.ico; do
  cp "$out/$f" "$repo_root/site/static/$f"
  echo "copied $f to site/static/"
done
