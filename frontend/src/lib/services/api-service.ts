import { ApiError } from '$lib/api/api-error';
import type { paths } from '$lib/api/schema';
import createClient, { type Client } from 'openapi-fetch';

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
}

export default APIService;
