#!/usr/bin/env python3
"""Generate a static PEP 503 simple index from wheel/sdist files.

The generated HTML can be served by GitHub Pages so that pip can install
directly from the index:

    pip install --index-url https://OWNER.github.io/REPO/simple cycletls
"""

from __future__ import annotations

import argparse
import html
from pathlib import Path


def generate_index(
    package_name: str,
    wheel_files: list[str],
    base_url: str,
    output_dir: Path,
) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)

    # Top-level simple/index.html
    simple_index = output_dir / "index.html"
    with open(simple_index, "w") as f:
        f.write("<!DOCTYPE html>\n<html>\n<body>\n")
        f.write(
            f'<a href="{html.escape(package_name)}/">{html.escape(package_name)}</a>\n'
        )
        f.write("</body>\n</html>\n")

    # Package-specific simple/{package}/index.html
    pkg_dir = output_dir / package_name
    pkg_dir.mkdir(exist_ok=True)

    pkg_index = pkg_dir / "index.html"
    with open(pkg_index, "w") as f:
        f.write("<!DOCTYPE html>\n<html>\n<body>\n")
        for wheel_file in sorted(wheel_files):
            wheel_name = Path(wheel_file).name
            file_url = f"{base_url}/{wheel_name}"
            f.write(
                f'<a href="{html.escape(file_url)}">'
                f"{html.escape(wheel_name)}</a><br/>\n"
            )
        f.write("</body>\n</html>\n")

    print(f"Generated PEP 503 index at {output_dir}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Generate a PEP 503 simple package index"
    )
    parser.add_argument(
        "--package", default="cycletls", help="Package name (default: cycletls)"
    )
    parser.add_argument(
        "--base-url", required=True, help="Base URL where wheel files are hosted"
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=Path("site/simple"),
        help="Output directory for the index (default: site/simple)",
    )
    parser.add_argument("files", nargs="+", help="Wheel/sdist files to index")
    args = parser.parse_args()

    generate_index(args.package, args.files, args.base_url, args.output_dir)


if __name__ == "__main__":
    main()
