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


def markdown_lines() -> list[tuple[str, int, str]]:
	result = subprocess.run(
		["git", "grep", "-n", "-I", "-e", "](", "--", "*.md"],
		capture_output=True,
		text=True,
	)
	if result.returncode not in (0, 1):
		raise subprocess.CalledProcessError(result.returncode, result.args, result.stdout, result.stderr)

	lines = []
	for match in result.stdout.splitlines():
		path, number, line = match.split(":", 2)
		if not os.path.islink(path):
			lines.append((path, int(number), line))
	return lines


def broken_links(path: str, number: int, line: str) -> list[str]:
	base = SERVED_FROM.get(path, os.path.dirname(path))
	broken = []
	for target in LINK.findall(line):
		if re.match(r"^([a-z][a-z0-9+.-]*:|/|\.\.\.$)", target):
			continue
		if not os.path.exists(os.path.normpath(os.path.join(base, target))):
			broken.append(f"{path}:{number}: {target}")
	return broken


def main() -> int:
	broken = [link for path, number, line in markdown_lines() for link in broken_links(path, number, line)]
	print("\n".join(broken) or "All Markdown links resolve.")
	return 1 if broken else 0


if __name__ == "__main__":
	sys.exit(main())
