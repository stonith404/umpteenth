import { ApiError } from '$lib/api/api-error';
import type { paths } from '$lib/api/schema';
import createClient, { type Client } from 'openapi-fetch';

// The largest page the list endpoints return, larger requests are capped by the backend
export const MAX_PAGE_SIZE = 100;

// Every tab shares the session cookie, which moves to another workspace when any tab switches
// Requests name the workspace this tab shows, so the backend refuses a write that would otherwise land in another one
const WORKSPACE_HEADER = 'X-Umpteenth-Workspace';
let shownWorkspaceId: string | null = null;

// The root layout calls this whenever it loads the signed-in user, whose workspace the tab shows from then on
export function setShownWorkspace(id: string) {
	shownWorkspaceId = id;
}

// The shape every openapi-fetch call resolves to, regardless of the operation
type FetchResult<T> = { data?: T; error?: unknown; response: Response };

// Operations without a response body (e.g. 204 No Content) resolve to void instead of never
type Unwrapped<T> = [T] extends [never] ? void : T;

// Base class of the domain services
// Subclasses call `this.api.GET(...)` and friends with typed paths, then pass the result to `unwrap`
abstract class APIService {
	protected api: Client<paths>;

	// Load functions pass SvelteKit's `fetch`, so their requests are tracked for `invalidate()`
	// The base URL is empty, so requests go to the same origin: the Go backend in production and the Vite proxy in development
	constructor(fetchFn?: typeof fetch) {
		this.api = createClient<paths>({ baseUrl: '', fetch: fetchFn });
		this.api.use({
			onRequest: ({ request }) => {
				if (shownWorkspaceId) request.headers.set(WORKSPACE_HEADER, shownWorkspaceId);
			}
		});
	}

	// Returns the response data of a successful call, or throws an ApiError carrying the backend's error body and HTTP status
	protected async unwrap<T>(request: Promise<FetchResult<T>>): Promise<Unwrapped<T>> {
		let result: FetchResult<T>;
		try {
			result = await request;
		} catch (e) {
			throw ApiError.network(e);
		}

		if (!result.response.ok) {
			throw ApiError.fromResponse(result.response, result.error);
		}
		return result.data as Unwrapped<T>;
	}

	// Collects every item of a paginated list for pickers, one page of the largest size at a time
	protected async listAllPages<T>(
		fetchPage: (page: number, pageSize: number) => Promise<{ items: T[]; total: number }>
	): Promise<T[]> {
		const items: T[] = [];
		for (let page = 1; ; page++) {
			const result = await fetchPage(page, MAX_PAGE_SIZE);
			items.push(...result.items);
			if (items.length >= result.total || result.items.length === 0) return items;
		}
	}
}

export default APIService;
