import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import packageJson from './package.json' with { type: 'json' };

/** @type {import('@sveltejs/kit').Config} */
const config = {
	preprocess: vitePreprocess(),
	compilerOptions: {
		// Components intentionally read props once to seed local state, e.g. forms built from loaded data
		warningFilter: (warning) => warning.code !== 'state_referenced_locally'
	},
	kit: {
		// The app is a static SPA that the Go backend embeds and serves with an index.html fallback
		adapter: adapter({
			fallback: 'index.html',
			pages: process.env.BUILD_OUTPUT_PATH ?? '../backend/frontend/dist',
			precompress: true
		}),
		version: {
			name: packageJson.version
		}
	}
};

export default config;
