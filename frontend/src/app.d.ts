// See https://svelte.dev/docs/kit/types#app.d.ts
declare global {
	namespace App {
		interface Error {
			message: string;
			status?: number;
			// The API's stable error code, e.g. `not_found` or `not_signed_in`
			code?: string;
			requestId?: string;
		}
		interface PageData {
			// Labels for dynamic route segments in the breadcrumb, e.g. `{ [runId]: 'Run #12' }`
			breadcrumbLabels?: Record<string, string>;
		}
	}
}

export {};
