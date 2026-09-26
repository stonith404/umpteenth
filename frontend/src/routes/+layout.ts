import { isApiError } from '$lib/api/api-error';
import type { User } from '$lib/api/types';
import { setShownWorkspace } from '$lib/services/api-service';
import UserService from '$lib/services/user-service';
import { rememberLoginProvider } from '$lib/utils/login-provider-util';
import { LOGIN_PATH, loginUrl, safeRedirectPath } from '$lib/utils/redirection-util';
import { tryCatch } from '$lib/utils/try-catch-util';
import { redirect } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

// The app is a static SPA, the Go backend serves index.html for every route
export const ssr = false;

export const load: LayoutLoad = async ({
	fetch,
	url,
	untrack,
	depends
}): Promise<{ user: User | null }> => {
	// Settings that come with the session's workspace, such as the usage unit, reload the session by invalidating this
	depends('app:user');

	// The URL is untracked, so navigating between pages or changing table query parameters doesn't refetch the user
	const { pathname, search, redirectParam } = untrack(() => ({
		pathname: url.pathname,
		search: url.search,
		redirectParam: url.searchParams.get('redirect')
	}));
	const isLoginPage = pathname === LOGIN_PATH;

	// Load the signed-in user, where a 401 simply means there is no session
	// A session whose user no longer exists (e.g. after a database reset) is treated the same way
	const result = await tryCatch(new UserService(fetch).me());
	if (result.error) {
		if (!isApiError(result.error, 'not_signed_in', 'invalid_token', 'not_found'))
			throw result.error;
		if (!isLoginPage) redirect(307, loginUrl(pathname + search));
		return { user: null };
	}

	// Writes from this tab only go through while the session is still in the workspace the tab shows
	setShownWorkspace(result.data.workspace.id);

	// The session names the sign-in provider it signed in with, which the login page points out next time
	if (result.data.loginProvider) rememberLoginProvider(result.data.loginProvider);

	// A signed-in user has no business on the login page, so send them where they wanted to go
	if (isLoginPage) {
		redirect(307, safeRedirectPath(redirectParam) ?? '/');
	}

	return { user: result.data };
};
