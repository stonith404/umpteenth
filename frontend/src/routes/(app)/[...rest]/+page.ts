import { error } from '@sveltejs/kit';
import type { PageLoad } from './$types';

// Unknown URLs get the not-found page inside the app shell, where the navigation stays at hand
export const load: PageLoad = () => {
	error(404, { message: 'Not found', code: 'not_found' });
};
