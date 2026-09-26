import { goto } from '$app/navigation';
import { ApiError, isApiError } from '$lib/api/api-error';
import { LOGIN_PATH, loginUrl } from '$lib/utils/redirection-util';
import { toast } from 'svelte-sonner';

const DEFAULT_MESSAGE = 'An unknown error occurred';

// Friendlier messages for the stable `apperror` codes
// Codes that are missing here fall back to the message the backend sent, which is always client-safe
const codeMessages: Record<string, string> = {
	internal_error: 'Something went wrong on the server, please try again',
	network_error: 'Could not reach the server, check your connection and try again',
	not_signed_in: 'Your session has expired, please sign in again',
	invalid_token: 'Your session is invalid or has expired, please sign in again',
	rate_limited: 'Too many requests, please wait a moment and try again',
	oidc_not_configured: 'Sign-in is not configured on this server',
	oidc_login_failed: 'Sign-in failed, please try again',
	unavailable: 'The service is temporarily unavailable, please try again'
};

// Errors the login callback reports through `/login?error=<code>`, next to the apperror codes above
const loginErrorMessages: Record<string, string> = {
	access_denied: 'The identity provider denied access',
	forbidden: 'Your account is not allowed to use Umpteenth',
	login_required: 'Please sign in with your identity provider'
};

// Returns a message that is safe and useful to show to the user
export function getErrorMessage(e: unknown, defaultMessage = DEFAULT_MESSAGE): string {
	if (!(e instanceof ApiError)) {
		return defaultMessage;
	}

	// Huma's own validation errors only say "Request validation failed", so the field details carry the useful part
	if (e.code === 'validation_failed' && e.fields.length > 0) {
		return e.fields.map((f) => `${fieldLabel(f.field)}: ${f.message}`).join(', ');
	}

	return codeMessages[e.code] ?? e.message ?? defaultMessage;
}

export function getErrorRequestId(e: unknown): string | undefined {
	return e instanceof ApiError ? e.requestId : undefined;
}

// Shows an error as a toast, or sends the user to the login page when the session is gone
export function apiErrorToast(e: unknown, defaultMessage = DEFAULT_MESSAGE) {
	if (isApiError(e, 'not_signed_in')) {
		redirectToLogin();
		return;
	}

	// The request ID lets users point operators at the matching server log line
	const requestId = getErrorRequestId(e);
	toast.error(getErrorMessage(e, defaultMessage), {
		description: requestId ? `Request ID: ${requestId}` : undefined
	});

	if (!(e instanceof ApiError)) {
		console.error(e);
	}
}

// Maps the `error` query parameter of the login page to a message
export function getLoginErrorMessage(code: string): string {
	return loginErrorMessages[code] ?? codeMessages[code] ?? `Sign-in failed (${code})`;
}

// Sends the user to the login page and brings them back to the current page afterwards
export function redirectToLogin() {
	const { pathname, search } = window.location;
	if (pathname === LOGIN_PATH) return;
	void goto(loginUrl(pathname + search), { invalidateAll: true });
}

function fieldLabel(field: string) {
	return field.replace(/^(body|query|path)\./, '');
}
