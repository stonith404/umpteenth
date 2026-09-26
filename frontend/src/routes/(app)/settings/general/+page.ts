import ProviderService from '$lib/services/provider-service';
import SecretService from '$lib/services/secret-service';
import SettingsService from '$lib/services/settings-service';
import SystemService from '$lib/services/system-service';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ fetch }) => {
	const [settings, models, system, secrets] = await Promise.all([
		new SettingsService(fetch).get(),
		new ProviderService(fetch).listAllModels(),
		new SystemService(fetch).info(),
		new SecretService(fetch).listAll()
	]);
	return { settings, models, system, secrets };
};
