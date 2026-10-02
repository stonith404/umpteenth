import { goto } from '$app/navigation';
import { ApiError, isApiError } from '$lib/api/api-error';
import ErrorToastDescription from '$lib/components/error-toast-description.svelte';
import { PasskeyError } from '$lib/utils/passkey-util';
import { LOGIN_PATH, loginUrl } from '$lib/utils/redirection-util';
import { enterWorkspace } from '$lib/utils/workspace-util';
import { toast } from 'svelte-sonner';

const DEFAULT_MESSAGE = 'An unknown error occurred';

// The codes of errors that mean the session is gone, which signing in again fixes
export const SESSION_ERROR_CODES = ['not_signed_in', 'invalid_token'];

// Friendlier messages for the stable `apperror` codes
// Codes that are missing here fall back to the message the backend sent, which is always client-safe
const codeMessages: Record<string, string> = {
	internal_error: 'Something went wrong on the server, please try again',
	network_error: 'Could not reach the server, check your connection and try again',
	not_signed_in: 'Your session has expired, please sign in again',
	invalid_token: 'Your session is invalid or has expired, please sign in again',
	rate_limited: 'Too many requests, please wait a moment and try again',
	login_not_configured: 'Sign-in is not configured on this server',
	login_failed: 'Sign-in failed, please try again',
	passkey_failed: 'The passkey could not be verified, please try again',
	unavailable: 'The service is temporarily unavailable, please try again'
};

// Errors the login callback reports through `/login?error=<code>`, next to the apperror codes above
const loginErrorMessages: Record<string, string> = {
	access_denied: 'The sign-in provider denied access',
	forbidden: 'Your account is not allowed to use Umpteenth',
	account_disabled: 'Your account has been deactivated, ask an admin to reactivate it',
	login_required: 'Please sign in again',
	not_found: 'This sign-in option is no longer available'
};

// Returns a message that is safe and useful to show to the user
export function getErrorMessage(e: unknown, defaultMessage = DEFAULT_MESSAGE): string {
	// The browser's passkey prompt fails without a request, and its error is worded for people already
	if (e instanceof PasskeyError) {
		return e.message;
	}
	if (!(e instanceof ApiError)) {
		return defaultMessage;
	}

	// Huma's own validation errors only say "Request validation failed", so the field details carry the useful part
	if (e.code === 'validation_failed' && e.fields.length > 0) {
		return e.fields.map((f) => `${fieldLabel(f.field)}: ${f.message}`).join(', ');
	}

	return codeMessages[e.code] ?? (e.message || defaultMessage);
}

// Reports whether an error means the session is gone, from an ApiError or from the `code` the error page receives
export function isSessionError(e: unknown): boolean {
	if (isApiError(e, ...SESSION_ERROR_CODES)) return true;
	const code = typeof e === 'object' && e !== null && 'code' in e ? e.code : undefined;
	return typeof code === 'string' && SESSION_ERROR_CODES.includes(code);
}

// Shows an error as a toast, or sends the user to the login page when the session is gone
// The title says what failed, e.g. 'Failed to delete the job', and the description why, as far as the error tells
export function apiErrorToast(e: unknown, title = DEFAULT_MESSAGE) {
	if (isSessionError(e) && redirectToLogin()) return;

	// The request ID lets users point operators at the matching server log line, so it is set as an ID rather than prose
	const reason = sentence(getErrorMessage(e, ''));
	const requestId = e instanceof ApiError ? e.requestId : undefined;
	toast.error<typeof ErrorToastDescription>(title, {
		description: reason || requestId ? ErrorToastDescription : undefined,
		componentProps: { reason, requestId }
	});

	// The tab still showed a workspace the session left in another tab, so it catches up with the one the session is in now
	if (isApiError(e, 'workspace_changed')) void enterWorkspace();

	if (!(e instanceof ApiError)) {
		console.error(e);
	}
}

// Maps the `error` query parameter of the login page to a message
// Anyone can link to the login page with any text in the parameter, so only code-shaped values are echoed back
export function getLoginErrorMessage(code: string): string {
	const known = loginErrorMessages[code] ?? codeMessages[code];
	if (known) return known;
	return /^[a-z0-9_.-]{1,64}$/i.test(code) ? `Sign-in failed (${code})` : 'Sign-in failed';
}

let loginRedirectPending = false;

// Sends the user to the login page and brings them back to the current page afterwards
// It returns false when signing in again just failed to help on this page, so the caller shows the error instead
export function redirectToLogin(): boolean {
	const { pathname, search } = window.location;
	const path = pathname + search;

	// The user is on the login page already, or on the way there because another request failed the same way
	if (pathname === LOGIN_PATH || loginRedirectPending) return true;
	if (recentlyRedirectedToLogin(path)) return false;

	rememberLoginRedirect(path);
	loginRedirectPending = true;
	void goto(loginUrl(path), { invalidateAll: true }).finally(() => {
		loginRedirectPending = false;
	});
	return true;
}

// The last page this tab sent to the login page for a lost session, and when
const LOGIN_REDIRECT_KEY = 'umpteenth:session-redirect';
const LOGIN_REDIRECT_WINDOW_MS = 15_000;

// Reports whether this tab sent the user from this page to sign in moments ago
// A page that fails the same way right after signing in would bounce between itself and the login page forever, so the second attempt shows the error instead
export function recentlyRedirectedToLogin(path: string): boolean {
	try {
		const last = JSON.parse(sessionStorage.getItem(LOGIN_REDIRECT_KEY) ?? 'null');
		return last?.path === path && Date.now() - last.at < LOGIN_REDIRECT_WINDOW_MS;
	} catch {
		return false;
	}
}

// Notes that this tab is sending the user from this page to sign in, for recentlyRedirectedToLogin
export function rememberLoginRedirect(path: string) {
	try {
		sessionStorage.setItem(LOGIN_REDIRECT_KEY, JSON.stringify({ path, at: Date.now() }));
	} catch {
		// Without storage the guard is off, which only matters for a page that keeps failing after signing in
	}
}

function fieldLabel(field: string) {
	return field.replace(/^(body|query|path)\./, '');
}

// What an error page shows, decided from the error, its status and the route it happened on
export type ErrorPageContent = {
	// The session is gone, so the page sends the user to sign in instead of showing anything
	sessionLost: boolean;
	status: number;
	title: string;
	description: string;
	// The way out, e.g. back to the jobs list when a job is missing
	back: { href: string; label: string };
	// Whether trying again can help, which is the case for server and network errors
	retry: boolean;
	// Operators find the matching log line by it, so it shows only for server errors
	requestId?: string;
};

const MISSING_DESCRIPTION = 'It may have been deleted, or it belongs to another workspace.';

// Pages whose load looks up one resource, which a 404 on them means is missing
const missingResources = [
	{ route: '/(app)/jobs/[id]', title: 'Job not found' },
	{ route: '/(app)/runs/[id]', title: 'Run not found' }
];

// The list a missing page's section starts from, by the first segment of its URL
const sectionHomes: Record<string, { href: string; label: string }> = {
	jobs: { href: '/jobs', label: 'Back to jobs' },
	runs: { href: '/runs', label: 'Back to runs' },
	mcp: { href: '/mcp', label: 'Back to MCP servers' },
	skills: { href: '/skills', label: 'Back to skills' },
	settings: { href: '/settings', label: 'Back to settings' }
};

const DASHBOARD = { href: '/', label: 'Back to the dashboard' };

// Decides the title, description and actions of an error page
// `error` and `status` are page.error and page.status, where API errors carry their real status in the error since the page's own is always 500
export function errorPageContent(
	error: App.Error | null,
	status: number,
	routeId: string | null,
	pathname: string
): ErrorPageContent {
	const effectiveStatus = error?.status || status;
	const section = sectionHomes[pathname.split('/')[1] ?? ''];
	const content: ErrorPageContent = {
		sessionLost: isSessionError(error) || effectiveStatus === 401,
		status: effectiveStatus,
		title: 'Something went wrong',
		description: sentence(error?.message) || 'An unknown error occurred.',
		back: DASHBOARD,
		retry: false
	};

	// The page normally sends the user to sign in right away, and shows this only when signing in didn't help
	if (content.sessionLost) {
		return {
			...content,
			title: 'Your session has ended',
			description: 'Sign in again to continue where you left off.',
			back: { href: loginUrl(pathname), label: 'Sign in' }
		};
	}

	if (effectiveStatus === 404) {
		const resource = missingResources.find((r) => routeId?.startsWith(r.route));
		return {
			...content,
			title: resource?.title ?? 'Page not found',
			description: resource ? MISSING_DESCRIPTION : "The page you're looking for doesn't exist.",
			back: section ?? DASHBOARD
		};
	}

	if (effectiveStatus === 403) {
		return {
			...content,
			title: "You don't have access to this page",
			description: sentence(error?.message) || 'Ask an admin if you think you should have access.'
		};
	}

	// Server and network errors are often temporary, so they offer a retry and the request ID for the logs
	if (error?.code === 'network_error') {
		return {
			...content,
			title: "Can't reach the server",
			description: 'Check your connection, then try again.',
			retry: true
		};
	}
	if (effectiveStatus >= 500) {
		// The generic messages of unexpected failures would only repeat the title
		const generic = !error?.code || error.code === 'internal_error';
		return {
			...content,
			description: generic
				? 'The server ran into a problem loading this page.'
				: content.description,
			retry: true,
			requestId: error?.requestId
		};
	}

	return content;
}

// Ends a message with a period, since the backend's messages are phrases without one
function sentence(message: string | undefined) {
	const text = message?.trim();
	if (!text) return '';
	return /[.!?]$/.test(text) ? text : `${text}.`;
}
