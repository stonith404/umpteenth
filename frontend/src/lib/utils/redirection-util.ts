export const LOGIN_PATH = '/login';

// Only same-origin relative paths are accepted, so the redirect parameter can't be used as an open redirect
// The value is resolved like the browser will resolve it, since browsers drop tabs and newlines and read backslashes as slashes, which turns "/\t/evil.example" into another origin
export function safeRedirectPath(value: string | null | undefined): string | null {
	if (!value || !value.startsWith('/')) return null;
	const base = 'http://same-origin.invalid';
	let url: URL;
	try {
		url = new URL(value, base);
	} catch {
		return null;
	}
	if (url.origin !== base) return null;
	return url.pathname + url.search + url.hash;
}

// Builds the login URL that brings the user back to `returnTo` after signing in
export function loginUrl(returnTo?: string): string {
	const redirect = safeRedirectPath(returnTo);
	if (!redirect || redirect === '/' || redirect.startsWith(LOGIN_PATH)) return LOGIN_PATH;
	return `${LOGIN_PATH}?redirect=${encodeURIComponent(redirect)}`;
}

// Builds the backend URL that starts the OIDC login flow
export function oidcLoginUrl(redirect: string | null): string {
	const safe = safeRedirectPath(redirect);
	return safe ? `/api/auth/login?redirect=${encodeURIComponent(safe)}` : '/api/auth/login';
}
