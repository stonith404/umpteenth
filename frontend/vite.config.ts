import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// The dev server proxies everything the backend owns, so the SPA can use same-origin requests in development too
const backendUrl = process.env.DEVELOPMENT_BACKEND_URL || 'http://localhost:8080';

export default defineConfig({
	plugins: [sveltekit(), tailwindcss()],
	server: {
		host: process.env.HOST,
		proxy: {
			'/api': { target: backendUrl },
			'/hooks': { target: backendUrl },
			'/healthz': { target: backendUrl }
		}
	}
});
