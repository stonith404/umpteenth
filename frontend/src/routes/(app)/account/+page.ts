import UserService from '$lib/services/user-service';
import type { PageLoad } from './$types';

// Only passkey accounts have passkeys, the others sign in through their provider
export const load: PageLoad = async ({ fetch, parent }) => {
	const { user } = await parent();
	const passkeys = user.passkeyAccount ? await new UserService(fetch).listPasskeys() : [];
	return { passkeys };
};
