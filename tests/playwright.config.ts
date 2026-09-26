import { defineConfig, devices } from '@playwright/test';

// The suite drives a running Umpteenth built with the e2etest tag, see setup/docker-compose.yml
// Every spec resets the backend and signs in again before each test, since a reset ends all sessions
export default defineConfig({
	outputDir: './.output',
	timeout: 15_000,
	testDir: './specs',
	fullyParallel: false,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	// Every spec resets the shared backend, so specs must never run concurrently
	workers: 1,
	reporter: process.env.CI
		? [['html', { outputFolder: '.report' }], ['github']]
		: [['line'], ['html', { open: 'never', outputFolder: '.report' }]],
	use: {
		baseURL: process.env.APP_URL ?? 'http://localhost:8080',
		video: 'retain-on-failure',
		trace: 'on-first-retry'
	},
	projects: [{ name: 'browser-chrome', use: devices['Desktop Chrome'] }]
});
