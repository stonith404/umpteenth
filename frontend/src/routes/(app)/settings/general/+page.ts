import ProviderService from '#lib/services/provider-service.js';
import SecretService from '#lib/services/secret-service.js';
import SettingsService from '#lib/services/settings-service.js';
import { tryCatch } from '#lib/utils/try-catch-util.js';
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
