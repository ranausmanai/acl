"""
ECL CLI  –  entry point for `ecl run`.

Usage
-----
  ecl run <file.ecl>                    # run with no vars
  ecl run <file.ecl> --vars vars.json   # inject variables
  ecl run <file.ecl> --out receipt.json # custom output path
  ecl run <file.ecl> --no-cache         # disable caching
  ecl cache clear                       # wipe .ecl_cache/
"""

from __future__ import annotations

import json
import os
import sys

import click

from .runtime import run_program
from .cache import Cache


@click.group()
def main() -> None:
    """ECL – Universal AI Execution Protocol CLI."""


@main.command()
@click.argument("file", type=click.Path(exists=True, dir_okay=False))
@click.option("--vars", "vars_file", default=None, type=click.Path(exists=True),
              help="JSON file with input variables.")
@click.option("--out", "out_file", default="receipt.json",
              help="Output path for the receipt JSON (default: receipt.json).")
@click.option("--cache-dir", default=".ecl_cache", show_default=True,
              help="Directory for the tool-call cache.")
@click.option("--no-cache", is_flag=True, default=False,
              help="Disable cache reads/writes for this run.")
@click.option("--quiet", "-q", is_flag=True, default=False,
              help="Suppress progress output.")
def run(
    file: str,
    vars_file: str | None,
    out_file: str,
    cache_dir: str,
    no_cache: bool,
    quiet: bool,
) -> None:
    """Execute an ECL program and write a receipt."""
    # Load source
    with open(file, encoding="utf-8") as fh:
        source = fh.read()

    # Load vars
    vars_env: dict = {}
    if vars_file:
        with open(vars_file, encoding="utf-8") as fh:
            vars_env = json.load(fh)

    if not quiet:
        click.echo(f"ECL › running {file}")

    # Override cache dir to a temp location if --no-cache
    effective_cache_dir = cache_dir
    if no_cache:
        import tempfile
        effective_cache_dir = tempfile.mkdtemp(prefix="ecl_nocache_")

    receipt = run_program(
        source=source,
        vars_env=vars_env,
        cache_dir=effective_cache_dir,
    )

    # Write receipt
    with open(out_file, "w", encoding="utf-8") as fh:
        json.dump(receipt, fh, indent=2, default=str)

    if not quiet:
        status = receipt.get("status", "unknown")
        colour = {"success": "green", "failed": "red", "needs_human": "yellow"}.get(status, "white")
        click.echo(f"ECL › status: " + click.style(status.upper(), fg=colour, bold=True))
        click.echo(f"ECL › receipt written to {out_file}")
        if receipt.get("error"):
            click.echo(f"ECL › error: {receipt['error']}", err=True)

    # Exit non-zero on failure
    if receipt.get("status") != "success":
        sys.exit(1)


@main.group()
def cache() -> None:
    """Manage the tool-call cache."""


@cache.command("clear")
@click.option("--cache-dir", default=".ecl_cache", show_default=True)
def cache_clear(cache_dir: str) -> None:
    """Delete all cached tool results."""
    c = Cache(cache_dir)
    removed = c.clear()
    click.echo(f"ECL › removed {removed} cache entries from {cache_dir}/")


@cache.command("info")
@click.option("--cache-dir", default=".ecl_cache", show_default=True)
def cache_info(cache_dir: str) -> None:
    """Show cache statistics."""
    if not os.path.isdir(cache_dir):
        click.echo("No cache directory found.")
        return
    entries = [f for f in os.listdir(cache_dir) if f.endswith(".json")]
    total_bytes = sum(
        os.path.getsize(os.path.join(cache_dir, f)) for f in entries
    )
    click.echo(f"Cache directory : {cache_dir}/")
    click.echo(f"Entries         : {len(entries)}")
    click.echo(f"Total size      : {total_bytes / 1024:.1f} KB")
