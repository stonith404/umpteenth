import type { McpServerAuth } from '$lib/api/types';
import type { BadgeVariant } from '$lib/components/ui/badge';

const transportLabels: Record<string, string> = {
	http: 'HTTP',
	stdio: 'stdio'
};

// A server's transport as it is written in prose, e.g. `HTTP` for `http`
export function transportLabel(transport: string) {
	return transportLabels[transport] ?? transport;
}

// How a server's authentication status reads in badges, following Codex's MCP auth states
export function authBadge(auth: McpServerAuth): { label: string; variant: BadgeVariant } {
	switch (auth.status) {
		case 'oauth':
			return { label: 'OAuth', variant: 'success' };
		case 'not_logged_in':
			return { label: auth.loggedInAt ? 'Login expired' : 'Not logged in', variant: 'warning' };
		case 'bearer_token':
			return { label: 'Bearer token', variant: 'secondary' };
		case 'unknown':
			return { label: 'Unknown', variant: 'secondary' };
		default:
			return { label: 'None', variant: 'outline' };
	}
}

// A sentence that explains the status and what to do about it
export function authDescription(auth: McpServerAuth): string {
	switch (auth.status) {
		case 'oauth':
			return auth.refreshable
				? 'Access tokens are refreshed automatically.'
				: 'The server issued no refresh token, so the login ends when its access token expires.';
		case 'not_logged_in':
			return auth.loggedInAt
				? 'The OAuth login expired. Log in again so jobs can use the server.'
				: 'The server advertises an OAuth login. Log in so jobs can use it.';
		case 'bearer_token':
			return 'An Authorization header authenticates every request.';
		case 'unknown':
			return "OAuth detection couldn't reach the server yet. Test the server to check again.";
		default:
			return "The server doesn't advertise an OAuth login.";
	}
}

// Whether a login can be started, which is only useful for HTTP servers that don't send their own Authorization header
export function canLogIn(auth: McpServerAuth) {
	return auth.status === 'oauth' || auth.status === 'not_logged_in' || auth.status === 'unknown';
}
