// @ts-check
import starlight from '@astrojs/starlight';
import { defineConfig } from 'astro/config';

// The site is served by Cloudflare at the root of the domain, see wrangler.jsonc
export default defineConfig({
	site: 'https://umpteenth.dev',
	// Pages that moved keep answering at their old address
	redirects: {
		'/guides/export-import/': '/deployment/backups/',
		'/reference/gig-cli/': '/reference/ump-cli/'
	},
	integrations: [
		starlight({
			title: 'Umpteenth',
			description: 'Self-hosted agentic jobs in disposable sandboxes, which get cheaper as they learn.',
			favicon: '/favicon.svg',
			// The lockup replaces the text title, in the ink version on light and the lime version on dark
			logo: {
				light: './src/assets/lockup-light.svg',
				dark: './src/assets/lockup-dark.svg',
				replacesTitle: true
			},
			// Kumo neutrals, the app's fonts and the brand tokens, see src/styles/theme.css
			customCss: ['./src/styles/theme.css', './src/styles/diagrams.css'],
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
			// The sidebar follows a reader's path: try it, use it day to day, run it in production, look things up
			sidebar: [
				{
					label: 'Start here',
					items: ['getting-started/introduction', 'getting-started/installation', 'getting-started/first-job']
				},
				{
					label: 'Guides',
					items: [
						'guides/writing-instructions',
						'guides/jobs',
						'guides/triggers',
						'guides/runs',
						'guides/self-improvement',
						'guides/sandboxes',
						'guides/mcp',
						'guides/models',
						'guides/notifications'
					]
				},
				{
					label: 'Self-hosting',
					items: [
						'deployment/configuration',
						'deployment/reverse-proxy',
						'deployment/security',
						'deployment/gvisor',
						'deployment/backups',
						'deployment/upgrading',
						'deployment/high-availability',
						'deployment/troubleshooting'
					]
				},
				{
					label: 'Reference',
					items: ['reference/job-settings', 'reference/api', 'reference/ump-cli', 'reference/server-cli']
				}
			]
		})
	]
});
