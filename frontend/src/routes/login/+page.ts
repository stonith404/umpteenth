import UserService from '$lib/services/user-service';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch }) => {
	const userService = new UserService(fetch);
	const providers = await userService.loginProviders();

	// Only an instance with passkeys can be set up from here, every other one is set up through its sign-in providers
	const passkeys = providers.some((p) => p.type === 'passkey');
	const setupOpen = passkeys ? (await userService.setup()).open : false;
	return { providers, setupOpen };
};
