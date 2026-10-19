#!/usr/bin/env python3
"""Emit choudoufu's pixel cube logo as a crispEdges SVG.

The mark is one block of dark fried tofu in three-quarter view: a lighter
top face, two darker side faces, a bright edge where the faces meet, and a
few pores. The art is the ASCII grids below, one character per pixel. Edit
a grid or the palette, then run scripts/brand/render.sh to rebuild the SVG
and every PNG and icon made from it.

    python3 scripts/brand/logo.py > site/public/brand/cube.svg
    python3 scripts/brand/logo.py --small   # the hand-tuned 16px grid
"""

GRID = """
...............KK...............
.............KKttKK.............
...........KKttttttKK...........
.........KKtttpptttttKK.........
.......KKttpttddtttttttKK.......
.....KKtttttttttttttpptttKK.....
...KKttttpttttttttttddtttttKK...
.KKttttttttttttttttttttttttttKK.
KhhtttppttttttttpttttttttpttthhK
KffhhtddttttttttdttttttttdthhssK
KffffhhtttttptttttttttptthhssssK
KffffffhhttttttttttpttthhssssssK
KffffffffhhttttttpttthhssssssssK
KffffffffffhhtttttthhssssssssssK
KfffqqfffffffhhtthhssssssssrrssK
KfffffffffffqffhhssssrsssssssssK
KffffffffffffffesssssssssssssssK
KffffffffffffffesrsssssssssssssK
KffffffffqfffffesssssssssssssssK
KffffffffffffqfesssssssssrsssssK
KffqfffffffffqfessrsssssssssrssK
KffffffqfffffffesssssssssssssssK
KffffffffffffffesssssssssssssssK
.KKffffffffqfffesssssssssssssKK.
...KKffffffffffessssssrrsssKK...
.....KKffffffffesssssssssKK.....
.......KKffffffesssrsssKK.......
.........KKffffesssssKK.........
...........KKffesssKK...........
.............KKesKK.............
...............KK...............
"""

# Box-averaging the 32px grid down to 16px muddies the edges, so the 16px
# icon is drawn by hand.
SMALL = """
.......KK.......
.....KKttKK.....
...KKttpttdKK...
.KKtttttttttpKK.
KhhttpttttttthhK
KffhhtttttthhssK
KffffhhtthhssssK
KffffffhhssssssK
KffffffesssssssK
KfqffffessssrssK
KffffffesssssssK
.KKffffesssssKK.
...KKffesssKK...
.....KKesKK.....
.......KK.......
"""

# One colour per grid character. "." is transparent.
PALETTE = {
    "K": "#0a0908",  # outline, so the block reads on a white page
    "h": "#cbbfae",  # top face's front edges catching the light
    "e": "#b3a797",  # front vertical edge where the side faces meet
    "t": "#5d544a",  # top face, lighter so the block reads on a dark page
    "p": "#857a6e",  # pores on the top face
    "d": "#4a423a",  # pits beside the top face's pores
    "f": "#3b352f",  # left face
    "q": "#5a5249",  # pores on the left face
    "s": "#2a2522",  # right face, in shadow
    "r": "#433c36",  # pores on the right face
}


def rows(grid):
    lines = grid.strip("\n").split("\n")
    width = max(len(l) for l in lines)
    return [l.ljust(width, ".") for l in lines], width


def svg(grid=GRID):
    lines, width = rows(grid)
    height = len(lines)
    paths = {}
    for y, line in enumerate(lines):
        x = 0
        while x < width:
            c = line[x]
            run = 1
            while x + run < width and line[x + run] == c:
                run += 1
            if c != ".":
                if c not in PALETTE:
                    raise SystemExit(f"unknown grid character {c!r} at {x},{y}")
                paths.setdefault(c, []).append(f"M{x} {y}h{run}v1h-{run}z")
            x += run
    # Square canvas, art centred, so every icon size is square.
    side = max(width, height)
    top = (side - height) // 2
    left = (side - width) // 2
    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{-left} {-top} {side} {side}" '
        f'width="{side}" height="{side}" shape-rendering="crispEdges">'
    ]
    for c in PALETTE:
        if c in paths:
            out.append(f'<path fill="{PALETTE[c]}" d="{"".join(paths[c])}"/>')
    out.append("</svg>")
    return "".join(out) + "\n"


if __name__ == "__main__":
    import sys

    sys.stdout.write(svg(SMALL if "--small" in sys.argv[1:] else GRID))
