import PlaybookService from '$lib/services/playbook-service';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params, fetch, depends }) => {
	depends('app:playbook');
	const playbook = await new PlaybookService(fetch).get(params.id);
	return { playbook };
};
