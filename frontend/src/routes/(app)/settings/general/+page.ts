import ProviderService from '$lib/services/provider-service';
import SecretService from '$lib/services/secret-service';
import SettingsService from '$lib/services/settings-service';
import { tryCatch } from '$lib/utils/try-catch-util';
import type { PageLoad } from './$types';

// The secrets only feed the notifications card, so their failure leaves the rest of the page usable
export const load: PageLoad = async ({ fetch }) => {
	const [settings, models, secrets] = await Promise.all([
		new SettingsService(fetch).get(),
		new ProviderService(fetch).listAllModels(),
		tryCatch(new SecretService(fetch).listAll())
	]);
	return { settings, models, secrets: secrets.data ?? null };
};
