import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	compilerOptions: {
		// Components intentionally read props once to seed local state, e.g. forms built from loaded data
		warningFilter: (warning) => warning.code !== 'state_referenced_locally'
	},
	// `version.name` keeps its default, the build time, since SvelteKit compares it after a failed navigation to detect a newer deploy and package.json's version never changes
	kit: {
		// The app is a static SPA that the Go backend embeds and serves with an index.html fallback
		adapter: adapter({
			fallback: 'index.html',
			pages: process.env.BUILD_OUTPUT_PATH ?? '../backend/frontend/dist',
			precompress: true
		})
	}
};

export default config;
