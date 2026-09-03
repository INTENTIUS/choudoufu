"""Terminal presentation for the demo.

Thin on purpose. Every module talks to the screen through here so two things
stay swappable without touching logic: the styling (rich today, something
richer later — the visualization the README lists as a next step), and the
pacing. In ``--auto`` mode ``pause`` and ``confirm`` stop blocking, which is
what lets the same beats run unattended in a rehearsal or under CI.
"""

from __future__ import annotations

import os
import sys

from rich.console import Console
from rich.panel import Panel
from rich.text import Text

console = Console()

# Set by the CLI from --auto / TLMIG_AUTO. When true the run never waits for a
# keypress and never asks for a confirmation, so a rehearsal or a test can run
# end to end. It does NOT relax the guard's fences; it only removes the human
# in the loop, which is why --auto is a rehearsal aid, not a way past safety.
AUTO = os.environ.get("TLMIG_AUTO") == "1"


def rule(title: str) -> None:
    console.rule(f"[bold]{title}")


def say(text: str) -> None:
    """Narration — the sentence the presenter reads while the audience looks
    at the panel."""
    console.print(Panel(Text(text, justify="left"), border_style="dim"))


def cmd(text: str) -> None:
    """Show a command exactly as it will run, before it runs, so the room can
    read it first."""
    console.print(f"  [bold cyan]$[/] [cyan]{text}[/]")


def kv(label: str, value: str, good: bool | None = None) -> None:
    """A measured fact: a label and its value, coloured by whether it is the
    good side of the comparison when that is known."""
    style = "green" if good else "yellow" if good is False else "white"
    console.print(f"  [dim]{label}[/] [bold {style}]{value}[/]")


def ok(text: str) -> None:
    console.print(f"  [green]✓[/] {text}")


def warn(text: str) -> None:
    console.print(f"  [yellow]⚠[/] {text}")


def err(text: str) -> None:
    console.print(f"  [red]✗[/] {text}")


def pause(prompt: str = "next") -> None:
    """Hold between beats so the presenter can talk. Returns immediately in
    --auto."""
    if AUTO:
        return
    console.print(f"\n[dim]⏎ {prompt}[/]", end="")
    try:
        input()
    except EOFError:
        # A non-interactive stdin (piped input, no TTY) behaves like --auto
        # rather than crashing the demo.
        console.print(" [dim](no tty, continuing)[/]")


def confirm(prompt: str) -> bool:
    """Ask before something destructive. True without asking in --auto, so a
    rehearsal is not gated on a human, but the guard still fences WHAT can be
    destroyed regardless of the answer here."""
    if AUTO:
        return True
    console.print(f"[bold yellow]{prompt}[/] [dim](y/N)[/] ", end="")
    try:
        return input().strip().lower() in ("y", "yes")
    except EOFError:
        return False


def fatal(text: str) -> None:
    """A guard refusal or a failed assertion: loud, and it stops the run."""
    console.print(Panel(Text(text), title="[bold red]refused", border_style="red"))
    sys.exit(1)
