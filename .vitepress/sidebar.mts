import { readdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import type { DefaultTheme } from 'vitepress'

const root = resolve(__dirname, '..')

const skippedDirectories = new Set(['node_modules', 'dist', 'build', 'public'])
const skippedFiles = new Set(['CLAUDE.md', 'AGENTS.md', 'report.md'])

/** Read the first Markdown heading. */
function heading(path: string, fallback: string): string {
	const match = readFileSync(path, 'utf8').match(/^#\s+(.+)$/m)
	return match ? match[1].trim() : fallback
}

function route(relativePath: string): string {
	const withoutExtension = relativePath.replace(/\.md$/, '')
	return '/' + withoutExtension.replace(/(^|\/)(README|index)$/, '$1')
}

/** Build the sidebar of one directory. A directory with no page becomes no group. */
export function section(directory: string): DefaultTheme.SidebarItem[] {
	const entries = readdirSync(join(root, directory), { withFileTypes: true })

	const files = entries
		.filter((entry) => entry.isFile() && entry.name.endsWith('.md') && !skippedFiles.has(entry.name))
		.sort((left, right) => Number(isEntry(right.name)) - Number(isEntry(left.name)) || left.name.localeCompare(right.name))

	const items: DefaultTheme.SidebarItem[] = files.map((file) => ({
		text: heading(join(root, directory, file.name), file.name),
		link: route(join(directory, file.name)),
	}))

	for (const entry of entries) {
		if (!entry.isDirectory() || entry.name.startsWith('.') || skippedDirectories.has(entry.name)) continue

		const nested = section(join(directory, entry.name))
		if (nested.length) items.push({ text: entry.name, collapsed: true, items: nested })
	}

	return items
}

function isEntry(name: string): boolean {
	return name === 'README.md' || name === 'index.md'
}
