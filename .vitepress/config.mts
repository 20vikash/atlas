import { defineConfig } from 'vitepress'
import { resolve } from 'node:path'
import { sidebar } from './sidebar.mts'

export default defineConfig({
	title: 'Atlas',
	description: 'The virtual machine control system for Frappe Cloud V2.',
	base: '/atlas/',
	cleanUrls: true,
	lastUpdated: true,
	// A link can point at a script or at a directory that GitHub lists. Neither becomes a page.
	ignoreDeadLinks: [/\.(sh|go|py|toml)$/, /\/docs\/index$/],

	srcExclude: ['**/CLAUDE.md', '**/AGENTS.md', 'report.md', '.tmp/**', '**/node_modules/**'],
	markdown: {
		config(markdown) {
			const defaultFence = markdown.renderer.rules.fence

			markdown.renderer.rules.fence = (tokens, index, options, environment, renderer) => {
				const token = tokens[index]

				if (token.info.trim() === 'mermaid') {
					return `<MermaidDiagram source="${encodeURIComponent(token.content)}" />`
				}

				return defaultFence?.(tokens, index, options, environment, renderer) ?? ''
			}
		},
	},
	rewrites: {
		'README.md': 'index.md',
		':directory(.*)/README.md': ':directory/index.md',
	},

	vite: {
		publicDir: resolve(__dirname, 'public'),
		server: {
			allowedHosts: ['.trycloudflare.com'],

			// Each directory needs a pattern next to its contents pattern, or the walk
			// still enters it. The proxy build root holds a complete chroot.
			watch: {
				followSymlinks: false,
				ignored: [
					'**/.build',
					'**/.build/**',
					'**/.git',
					'**/.git/**',
					'**/.tmp',
					'**/.tmp/**',
					'**/.venv',
					'**/.venv/**',
					'**/.vitepress/dist',
					'**/.vitepress/dist/**',
					'**/node_modules',
					'**/node_modules/**',
				],
			},
		},
	},

	themeConfig: {
		logo: '/logo.svg',
		search: { provider: 'local' },
		outline: { level: [2, 3], label: 'On this page' },

		nav: [
			{ text: 'Start here', link: '/' },
			{ text: 'Architecture', link: '/docs/architecture' },
			{ text: 'Atlas app', link: '/atlas/' },
			{ text: 'Metal', link: '/metal/' },
			{
				text: 'Services',
				items: [
					{ text: 'HTTP proxy', link: '/services/http-proxy/' },
					{ text: 'WG Mesh', link: '/services/wg-mesh/' },
				],
			},
			{ text: 'Specification', link: '/SPEC' },
		],

		sidebar,

		socialLinks: [{ icon: 'github', link: 'https://github.com/frappe/atlas' }],
		footer: { message: 'AGPL-3.0', copyright: 'Frappe Technologies' },
	},
})
