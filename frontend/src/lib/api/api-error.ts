// One client-actionable validation failure, mirrors `apperror.FieldError`
export type ApiFieldError = {
	field: string;
	code: string;
	message: string;
};

// The JSON body of every error response, mirrors `apperror.Body`
// It is declared here because the backend's OpenAPI spec does not describe the error schema yet
export type ApiErrorBody = {
	code: string;
	message: string;
	fields?: ApiFieldError[];
	details?: Record<string, string>;
	requestId?: string;
};

// Status 0 marks failures where no response arrived at all, e.g. the network is down
const NETWORK_ERROR_STATUS = 0;

// An error returned by the Umpteenth API, carrying the backend's stable error code and HTTP status
export class ApiError extends Error {
	readonly status: number;
	readonly code: string;
	readonly fields: ApiFieldError[];
	readonly details: Record<string, string>;
	readonly requestId?: string;

	constructor(status: number, body: ApiErrorBody) {
		super(body.message);
		this.name = 'ApiError';
		this.status = status;
		this.code = body.code;
		this.fields = body.fields ?? [];
		this.details = body.details ?? {};
		this.requestId = body.requestId;
	}

	// Builds an error from a failed response, tolerating bodies that are not in the apperror shape (e.g. from a proxy)
	static fromResponse(response: Response, body: unknown): ApiError {
		const requestId = response.headers.get('X-Request-ID') ?? undefined;
		if (isErrorBody(body)) {
			return new ApiError(response.status, { requestId, ...body });
		}
		return new ApiError(response.status, {
			code: response.status >= 500 ? 'internal_error' : 'unknown_error',
			message: response.statusText || `Request failed with status ${response.status}`,
			requestId
		});
	}

	// Builds an error for a request that never got a response
	static network(cause: unknown): ApiError {
		const error = new ApiError(NETWORK_ERROR_STATUS, {
			code: 'network_error',
			message: 'Could not reach the server'
		});
		error.cause = cause;
		return error;
	}

	get isNetworkError() {
		return this.status === NETWORK_ERROR_STATUS;
	}
}

function isErrorBody(body: unknown): body is ApiErrorBody {
	return (
		typeof body === 'object' &&
		body !== null &&
		typeof (body as ApiErrorBody).code === 'string' &&
		typeof (body as ApiErrorBody).message === 'string'
	);
}

// Reports whether an error is an ApiError, optionally with one of the given codes
export function isApiError(error: unknown, ...codes: string[]): error is ApiError {
	return error instanceof ApiError && (codes.length === 0 || codes.includes(error.code));
}
