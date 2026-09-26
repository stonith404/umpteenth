import ProviderService from '$lib/services/provider-service';
import type { PageLoad } from './$types';

// Every provider is loaded for the models table's provider filter and the model dialog's provider picker
export const load: PageLoad = async ({ fetch, depends }) => {
	depends('app:providers');
	const page = await new ProviderService(fetch).list({ pageSize: 100, sort: 'name' });
	return { providers: page.items ?? [] };
};
