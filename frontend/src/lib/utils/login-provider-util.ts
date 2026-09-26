const LAST_LOGIN_PROVIDER_KEY = 'last-login-provider';

// Remembers the sign-in provider this browser last signed in with, so the login page can point it out next time
// Storage can be unavailable, e.g. when the browser blocks site data, and the hint is not worth failing over
export function rememberLoginProvider(providerId: string) {
	try {
		localStorage.setItem(LAST_LOGIN_PROVIDER_KEY, providerId);
	} catch {
		// The login page simply shows no hint then
	}
}

// Returns the ID of the sign-in provider this browser last signed in with, if it remembers one
export function lastLoginProvider(): string | null {
	try {
		return localStorage.getItem(LAST_LOGIN_PROVIDER_KEY);
	} catch {
		return null;
	}
}
