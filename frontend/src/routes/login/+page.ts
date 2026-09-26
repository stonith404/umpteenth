import UserService from '$lib/services/user-service';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch }) => {
	return { providers: await new UserService(fetch).loginProviders() };
};
