// @ts-check
import starlight from '@astrojs/starlight';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'astro/config';
import starlightLlmsTxt from 'starlight-llms-txt';
import { sharedHead } from './src/sharedHead.ts';

// The site is served by Cloudflare at the root of the domain, see wrangler.jsonc
const site = 'https://umpteenth.dev';

export default defineConfig({
	site,
	vite: {
		plugins: [tailwindcss()]
	},
	integrations: [
		starlight({
			title: 'Umpteenth',
			plugins: [
				// AI assistants and answer engines read the docs as plain Markdown from /llms.txt and the files it links to, plus the raw API spec
				starlightLlmsTxt({
					promote: ['getting-started/**'],
					optionalLinks: [{ label: 'OpenAPI spec', url: `${site}/openapi.json`, description: 'every route of the REST API with its parameters and schemas' }]
				})
			],
			// The fallback meta description, for the few generated pages without one of their own
			description: 'A self-hosted AI agent for recurring jobs, which runs each one in a disposable sandbox and gets cheaper as it learns.',
			favicon: '/favicon.svg',
			head: sharedHead(site),
			// Search titles and the not-found page's noindex, see src/routeData.ts
			routeMiddleware: './src/routeData.ts',
			// The lockup replaces the text title, in the ink version on light and the lime version on dark
			logo: {
				light: './src/assets/lockup-light.svg',
				dark: './src/assets/lockup-dark.svg',
				replacesTitle: true
			},
			// Tailwind, Kumo neutrals, the app's fonts and the brand tokens, see src/styles/global.css
			customCss: ['./src/styles/global.css'],
			// Code blocks follow Kumo too: 8px corners, a hairline edge, no drop shadow and no window dots on shell frames
			expressiveCode: {
				styleOverrides: {
					borderRadius: '0.5rem',
					borderColor: 'var(--sl-color-hairline)',
					codeFontSize: '0.8125rem',
					codeLineHeight: '1.6',
					frames: {
						shadowColor: 'transparent',
						terminalTitlebarDotsForeground: 'transparent',
						terminalTitlebarDotsOpacity: '0',
						terminalTitlebarBorderBottomColor: 'var(--sl-color-hairline)',
						editorActiveTabIndicatorTopColor: 'var(--sl-color-accent)',
						editorActiveTabIndicatorBottomColor: 'transparent'
					}
				}
			},
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/stonith404/umpteenth' }],
			editLink: { baseUrl: 'https://github.com/stonith404/umpteenth/edit/main/docs/' },
			lastUpdated: true,
			// The sidebar follows a reader's path: install it, build jobs, connect what they need, run it in production, look things up
			// Both ways to install sit side by side, since a reader picks one and never needs the other
			sidebar: [
				{
					label: 'Start here',
					items: ['getting-started/introduction', 'getting-started/installation', 'getting-started/kubernetes', 'getting-started/first-job']
				},
				{
					label: 'Jobs',
					items: [
						'guides/writing-instructions',
						'guides/jobs',
						'guides/triggers',
						'guides/runs',
						'guides/self-improvement',
						'guides/sandboxes'
					]
				},
				{
					label: 'Workspace',
					items: ['guides/models', 'guides/mcp', 'guides/skills', 'guides/notifications', 'guides/workspaces']
				},
				{
					label: 'Self-hosting',
					items: [
						'deployment/sign-in',
						'deployment/reverse-proxy',
						'deployment/configuration',
						'deployment/security',
						'deployment/backups',
						'deployment/upgrading',
						'deployment/gvisor'
					]
				},
				{
					label: 'Reference',
					items: ['reference/job-settings', 'reference/api', { label: 'API endpoints', link: '/reference/api-endpoints/' }, 'reference/ump-cli']
				}
			]
		})
	]
});
