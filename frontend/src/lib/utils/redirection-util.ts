export const LOGIN_PATH = '/login';
// A sign-in link an instance admin hands out opens this page, followed by its token, and works without a session
export const SIGN_IN_LINK_PATH = '/login/link/';

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

	// Parsing also removes dot segments, which turns "/.//evil.example" into the protocol-relative "//evil.example", so the normalized path is checked again
	const path = url.pathname + url.search + url.hash;
	if (path.startsWith('//') || path.startsWith('/\\') || new URL(path, base).origin !== base) {
		return null;
	}
	return path;
}

// Builds the login URL that brings the user back to `returnTo` after signing in
export function loginUrl(returnTo?: string): string {
	const redirect = safeRedirectPath(returnTo);
	if (!redirect || redirect === '/' || redirect.startsWith(LOGIN_PATH)) return LOGIN_PATH;
	return `${LOGIN_PATH}?redirect=${encodeURIComponent(redirect)}`;
}

// Builds the backend URL that starts signing in with a provider
export function providerLoginUrl(providerId: string, redirect: string | null): string {
	const path = `/api/auth/login/${encodeURIComponent(providerId)}`;
	const safe = safeRedirectPath(redirect);
	return safe ? `${path}?redirect=${encodeURIComponent(safe)}` : path;
}
