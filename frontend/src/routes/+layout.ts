import { isApiError } from '$lib/api/api-error';
import type { User } from '$lib/api/types';
import UserService from '$lib/services/user-service';
import { LOGIN_PATH, loginUrl, safeRedirectPath } from '$lib/utils/redirection-util';
import { tryCatch } from '$lib/utils/try-catch-util';
import { redirect } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

// The app is a static SPA, the Go backend serves index.html for every route
export const ssr = false;

export const load: LayoutLoad = async ({ fetch, url, untrack }) => {
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
		return { user: null as User | null };
	}

	// A signed-in user has no business on the login page, so send them where they wanted to go
	if (isLoginPage) {
		redirect(307, safeRedirectPath(redirectParam) ?? '/');
	}

	return { user: result.data as User | null };
};
