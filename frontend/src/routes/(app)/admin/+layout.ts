import { error } from '@sveltejs/kit';
import type { LayoutLoad } from './$types';

// The admin area is only for instance admins, and the API refuses everyone else anyway
export const load: LayoutLoad = async ({ parent }) => {
	const { user } = await parent();
	if (!user.isAdmin) error(403, 'Only instance admins can open the admin area');
	return { user };
};
