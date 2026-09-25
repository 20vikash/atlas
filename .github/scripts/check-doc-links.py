"""Fail when a Markdown link points at a repository path that does not exist.

VitePress checks links between pages only. This script also checks links to code,
specifications, and directories, which the site build skips.
"""

import os
import re
import subprocess
import sys

LINK = re.compile(r"\]\(([^)\s#]+)(?:#[^)\s]*)?\)")
# VitePress serves docs/index.md as the site root, so its links resolve from there.
SERVED_FROM = {"docs/index.md": "."}


def markdown_files() -> list[str]:
	output = subprocess.run(["git", "ls-files", "*.md"], capture_output=True, text=True, check=True).stdout
	return [path for path in output.split() if not os.path.islink(path)]


def broken_links(path: str) -> list[str]:
	base = SERVED_FROM.get(path, os.path.dirname(path))
	broken = []
	with open(path, encoding="utf-8") as file:
		for number, line in enumerate(file, 1):
			for target in LINK.findall(line):
				if re.match(r"^([a-z][a-z0-9+.-]*:|/|\.\.\.$)", target):
					continue
				if not os.path.exists(os.path.normpath(os.path.join(base, target))):
					broken.append(f"{path}:{number}: {target}")
	return broken


def main() -> int:
	broken = [link for path in markdown_files() for link in broken_links(path)]
	print("\n".join(broken) or "All Markdown links resolve.")
	return 1 if broken else 0


if __name__ == "__main__":
	sys.exit(main())
