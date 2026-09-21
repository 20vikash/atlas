import type { DefaultTheme } from 'vitepress'

const link = (text: string, link: string): DefaultTheme.SidebarItem => ({ text, link })

export const sidebar: DefaultTheme.Sidebar = {
	'/docs/': [
		{
			text: 'Start here',
			items: [link('How Atlas works', '/docs/architecture'), link('Development setup', '/docs/development'), link('Operations guide', '/docs/operations'), link('Glossary', '/docs/glossary')],
		},
		{
			text: 'Reference',
			collapsed: true,
			items: [link('API clients', '/docs/api-clients'), link('Metal API contract', '/docs/metal-v1-contract')],
		},
	],

	'/atlas/': [
		{
			text: 'Atlas app',
			items: [
				link('Overview', '/atlas/'),
				link('Getting started', '/atlas/docs/getting-started'),
				link('VM control plane', '/atlas/docs/vm-control-plane'),
				link('VM migrations', '/atlas/docs/virtual-machine-migrations'),
				link('Metal Server lifecycle', '/atlas/docs/metal-server-lifecycle'),
				link('Images', '/atlas/docs/images'),
				link('Providers', '/atlas/docs/providers'),
				link('Tenant API', '/atlas/docs/tenant-api'),
				link('Security model', '/atlas/docs/security'),
				link('Operations', '/atlas/docs/operations'),
				link('Development', '/atlas/docs/development'),
			],
		},
		{
			text: 'Reference',
			collapsed: true,
			items: [link('Atlas app specification', '/atlas/SPEC'), link('Settings and providers', '/atlas/atlas/SPEC'), link('Metal Server module', '/atlas/metal_server/SPEC'), link('Virtual machine module', '/atlas/vm/SPEC')],
		},
		{
			text: 'More guides',
			collapsed: true,
			items: [link('Proxy Server', '/atlas/docs/proxy-server'), link('Cargo Server', '/atlas/docs/cargo-server'), link('Wildcard TLS', '/atlas/docs/wildcard-tls')],
		},
	],

	'/metal/': [
		{
			text: 'Metal',
			items: [link('Overview', '/metal/'), link('Architecture', '/metal/docs/architecture'), link('VMs', '/metal/docs/vm'), link('Networking', '/metal/docs/networking'), link('Storage', '/metal/docs/storage'), link('HTTP API', '/metal/docs/api'), link('Development', '/metal/docs/development'), link('Integration testing', '/metal/docs/testing'), link('Operations', '/metal/docs/operations')],
		},
		{
			text: 'Reference',
			collapsed: true,
			items: [link('Metal specification', '/metal/SPEC'), link('Package map', '/metal/internal/SPEC'), link('VM internals', '/metal/internal/vm/SPEC'), link('Migration internals', '/metal/internal/vm/migration/SPEC'), link('Storage internals', '/metal/internal/storage/SPEC'), link('Network internals', '/metal/internal/network/SPEC')],
		},
	],

	'/services/http-proxy/': [
		{
			text: 'HTTP proxy',
			items: [link('Overview', '/services/http-proxy/'), link('Install', '/services/http-proxy/docs/setup'), link('Configuration', '/services/http-proxy/docs/configuration'), link('High availability', '/services/http-proxy/docs/high-availability'), link('Control daemon', '/services/http-proxy/docs/control-daemon'), link('OpenResty data plane', '/services/http-proxy/docs/openresty'), link('Development', '/services/http-proxy/docs/development')],
		},
		{
			text: 'Reference',
			collapsed: true,
			items: [link('HTTP proxy specification', '/services/http-proxy/SPEC')],
		},
	],

	'/services/wg-mesh/': [
		{
			text: 'WG Mesh',
			items: [link('Overview', '/services/wg-mesh/'), link('Design and packet flow', '/services/wg-mesh/docs/design'), link('Operations', '/services/wg-mesh/docs/operations'), link('Unicast discovery', '/services/wg-mesh/docs/unicast-network'), link('Debug in production', '/services/wg-mesh/docs/debug-in-production'), link('Benchmarks', '/services/wg-mesh/docs/benchmark')],
		},
		{
			text: 'Reference',
			collapsed: true,
			items: [link('WG Mesh specification', '/services/wg-mesh/SPEC')],
		},
	],

	'/clients/': [{ text: 'API clients', items: [link('Overview', '/clients/'), link('Atlas client', '/clients/atlas-client/'), link('Proxy client', '/clients/atlas-proxy-client/')] }],
	'/llm/': [{ text: 'Review guides', items: [link('Agent tooling setup', '/llm/'), link('Go review guide', '/llm/go-code-review-guide')] }],
}
