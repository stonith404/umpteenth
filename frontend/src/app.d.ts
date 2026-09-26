// See https://svelte.dev/docs/kit/types#app.d.ts
declare global {
	namespace App {
		interface Error {
			message: string;
			status?: number;
			requestId?: string;
		}
		interface PageData {
			// Labels for dynamic route segments in the breadcrumb, e.g. `{ [runId]: 'Run #12' }`
			breadcrumbLabels?: Record<string, string>;
		}
	}
}

export {};
