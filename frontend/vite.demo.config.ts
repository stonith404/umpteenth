import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';

// The landing page's live demo: the app's run page built without SvelteKit, see src/demo/main.ts
// SvelteKit's `$app` modules are replaced with the demo's own, which drive the pages without a router
const path = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));

export default defineConfig({
	root: path('./src/demo'),
	// The docs serve the build from a subdirectory, so every asset is referenced relative to the page
	base: './',
	publicDir: false,
	plugins: [svelte({ configFile: path('./svelte.config.js') }), tailwindcss()],
	resolve: {
		alias: [
			// The code editor and the workspace dialog bring CodeMirror and zod, which the demo only needs on demand, so they load lazily
			{
				find: '$lib/components/code/code-editor.svelte',
				replacement: path('./src/demo/lazy/code-editor.svelte')
			},
			{
				find: '$lib/components/workspaces/create-workspace-dialog.svelte',
				replacement: path('./src/demo/lazy/create-workspace-dialog.svelte')
			},
			{ find: '$lib', replacement: path('./src/lib') },
			{ find: '$app/state', replacement: path('./src/demo/kit/state.svelte.ts') },
			{ find: '$app/navigation', replacement: path('./src/demo/kit/navigation.ts') }
		]
	},
	build: {
		outDir: process.env.DEMO_OUTPUT_PATH ?? path('../docs/public/app-demo'),
		emptyOutDir: true
	}
});
