#!/usr/bin/env python3
"""Emit choudoufu's pixel cube logo as a crispEdges SVG.

The art is the ASCII grid below, one character per pixel. Edit the grid
or the palette, then run scripts/brand/render.sh to rebuild the SVG and
every PNG and icon made from it.

    python3 scripts/brand/logo.py > site/public/brand/cube.svg
"""

GRID = """
................MM........MM............
...............MmmM......MmmM...........
...............MmmM......MmmM...........
..............MmmM........MmmM..........
.............MmmM..........MmmM.........
.............MmmM..........MmmM.........
..............MmmM........MmmM..........
...............MmmM......MmmM...........
................MmmM....MmmM............
................MmmM....MmmM............
...............MmmM......MmmM...........
..............MmmM........MmmM..........
..............MmmM........MmmM..........
...............MKKKKKKKKKKKKK...........
...............KhhhhhhhhhhhhhK..........
..............KhttttpttpttthsK..........
.............KhtpttttttttthssK..........
............KhtttttpttttphsssK..........
...........KhttttttttttthssssK..........
..........KhhhhhhhhhhhhesssssK..........
..........KffffffffffffesssssK..........
..........KffffffffffffesssssK..........
..........KffffffffffqfesssssK..........
..........KfffqffffffffesssssK..........
.......KKKKffffffffffffesssssKKKK.......
....KKKWWWWffffffffffffessssrrWWWKKK....
...KWWWWWWWffffffffqfffesssrrWWWWWWWK...
..KWWWWWWWwffffffffffffessrrwwWWWWWWWK..
.KWWWWWWwwwffffffffffffesrrwwwwwWWWWWWK.
KWWWWWWWwwwfffffffffffferrwwwwwwWWWWWWWK
.KWWWWWWwwwrrrrrrrrrrrrrrrwwwwwwWWWWWWK.
..KWWWWWWWwwwwwwwwwwwwwwwwwwwwWWWWWWWK..
...KrrrrrrrWWwwwwwwwwwwwwwwWWrrrrrrrK...
....KKKrrrrrrrrrrWWWWWWrrrrrrrrrrKKK....
.......KKKKKrrrrrrrrrrrrrrrrKKKKK.......
............KKKKKKKKKKKKKKKK............
"""

# One colour per grid character. "." is transparent.
PALETTE = {
    "K": "#141416",  # outline around cube and plate
    "h": "#6e6b64",  # cube edges catching the light, so it reads on a dark page
    "t": "#4a4843",  # cube top face
    "p": "#2b2a27",  # pores on the top face
    "f": "#2c2b30",  # cube front face
    "q": "#1f1e23",  # pores on the front face
    "e": "#3a3940",  # front and side corner
    "s": "#1c1b20",  # cube side face, in shadow
    "W": "#f4f3ee",  # plate rim
    "w": "#e2e1db",  # plate well
    "r": "#a9a9b0",  # rim and contact shadow
    "m": "#bfeadb",  # steam, a pale tint of the #009E73 accent
    "M": "#2f7a64",  # steam edge, so it reads on a white page
}


def rows():
    lines = [l for l in GRID.strip("\n").split("\n")]
    width = max(len(l) for l in lines)
    return [l.ljust(width, ".") for l in lines], width


def svg():
    lines, width = rows()
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
    # Square canvas, art centred vertically, so every icon size is square.
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

    sys.stdout.write(svg())
