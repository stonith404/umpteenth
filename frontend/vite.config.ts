import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// The dev server proxies everything the backend owns, so the SPA can use same-origin requests in development too
const backendUrl = process.env.DEVELOPMENT_BACKEND_URL || 'http://localhost:8080';

export default defineConfig({
	plugins: [
		sveltekit({
			preprocess: vitePreprocess(),
			compilerOptions: {
				// Components intentionally read props once to seed local state, e.g. forms built from loaded data
				warningFilter: (warning) => warning.code !== 'state_referenced_locally'
			},
			// `version.name` keeps its default, the build time, since SvelteKit compares it after a failed navigation to detect a newer deploy and package.json's version never changes
			// The app is a static SPA that the Go backend embeds and serves with an index.html fallback
			adapter: adapter({
				fallback: 'index.html',
				pages: process.env.BUILD_OUTPUT_PATH ?? '../backend/frontend/dist',
				precompress: true
			})
		}),
		tailwindcss()
	],
	server: {
		host: process.env.HOST,
		proxy: {
			'/api': { target: backendUrl },
			'/hooks': { target: backendUrl },
			'/healthz': { target: backendUrl },
			'/.well-known/oauth-protected-resource': { target: backendUrl }
		}
	}
});
