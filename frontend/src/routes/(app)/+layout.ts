import { loginUrl } from '#lib/utils/redirection-util.js';
import { redirect } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

// Every page in this group requires a session, which also narrows `user` to non-null for them
export const load: LayoutLoad = async ({ parent, url, untrack }) => {
	const { user } = await parent();

	// The root load doesn't rerun on client-side navigation, e.g. going back after logging out, so the session is checked here too
	if (!user) {
		redirect(
			307,
			untrack(() => loginUrl(url.pathname + url.search))
		);
	}

	return { user };
};
