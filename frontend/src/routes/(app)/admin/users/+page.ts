import UserService from '#lib/services/user-service.js';
import type { PageLoad } from './$types';

// Admins only create accounts while passkeys are turned on, which the login page's providers tell
export const load: PageLoad = async ({ fetch }) => {
	const providers = await new UserService(fetch).loginProviders();
	return { passkeys: providers.some((p) => p.type === 'passkey') };
};
