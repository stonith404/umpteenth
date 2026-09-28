import ProviderService from '$lib/services/provider-service';
import type { PageLoad } from './$types';

// Every provider is loaded for the models table's provider filter and the model dialog's provider picker
export const load: PageLoad = async ({ fetch, depends }) => {
	depends('app:providers');
	return { providers: await new ProviderService(fetch).listAll() };
};
