#!/usr/bin/env bash
# Render choudoufu's pixel cube logo: scripts/brand/logo.py writes the SVG,
# then every PNG, the favicon and the share cards are made from it.
# Needs rsvg-convert (brew install librsvg) and python3 with Pillow.
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
public="$repo_root/site-next/public"
brand="$public/brand"
mkdir -p "$brand"

command -v rsvg-convert >/dev/null || {
  echo "rsvg-convert not found; install with: brew install librsvg" >&2
  exit 1
}
python3 -c 'import PIL' 2>/dev/null || {
  echo "Pillow not found; install with: python3 -m pip install Pillow" >&2
  exit 1
}

python3 "$repo_root/scripts/brand/logo.py" > "$brand/cube.svg"
echo "wrote site-next/public/brand/cube.svg"

# One pixel per grid cell; Pillow scales it from there.
side=$(sed -E 's/.*width="([0-9]+)".*/\1/;q' "$brand/cube.svg")

py=$(cat <<'PY'
import io
import sys
from PIL import Image, ImageDraw, ImageFont

public = sys.argv[1]
brand = f"{public}/brand"
art = Image.open(io.BytesIO(sys.stdin.buffer.read())).convert("RGBA")
BG = (0x18, 0x18, 0x1B, 255)


def scaled(size):
    # Nearest neighbour keeps the pixels square. Below half the grid size it
    # drops whole rows (the steam and plate rim vanish at 16px), so the
    # smallest icons average each block of pixels instead.
    if size * 2 < art.width:
        return art.resize((size, size), Image.BOX)
    return art.resize((size, size), Image.NEAREST)


def on_dark(canvas, size, x, y):
    canvas.alpha_composite(scaled(size), (x, y))


for size in (160, 128, 64, 48, 32, 16):
    scaled(size).save(f"{brand}/cube-{size}.png", optimize=True)
    print(f"wrote site-next/public/brand/cube-{size}.png")

scaled(32).save(f"{public}/favicon-32.png", optimize=True)
print("wrote site-next/public/favicon-32.png")

scaled(48).save(
    f"{public}/favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)],
    append_images=[scaled(16), scaled(32)],
)
print("wrote site-next/public/favicon.ico")

# Opaque icons and share cards, the cube on the site's dark background.
touch = Image.new("RGBA", (180, 180), BG)
on_dark(touch, 160, 10, 10)
touch.convert("RGB").save(f"{public}/apple-touch-icon.png", optimize=True)
print("wrote site-next/public/apple-touch-icon.png")

square = Image.new("RGBA", (400, 400), BG)
on_dark(square, 320, 40, 40)
square.convert("RGB").save(f"{public}/cube-share.png", optimize=True)
print("wrote site-next/public/cube-share.png")


def font(size):
    for path in (
        "/System/Library/Fonts/Helvetica.ttc",
        "/System/Library/Fonts/Supplemental/Arial.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    ):
        try:
            return ImageFont.truetype(path, size)
        except OSError:
            continue
    return ImageFont.load_default(size)


og = Image.new("RGBA", (1200, 630), BG)
on_dark(og, 360, 110, 135)
draw = ImageDraw.Draw(og)
draw.text((520, 315), "choudoufu", font=font(120), fill=(0xF4, 0xF4, 0xF5, 255), anchor="lm")
og.convert("RGB").save(f"{public}/og.png", optimize=True)
print("wrote site-next/public/og.png")
PY
)
rsvg-convert -w "$side" -h "$side" "$brand/cube.svg" | python3 -c "$py" "$public"
