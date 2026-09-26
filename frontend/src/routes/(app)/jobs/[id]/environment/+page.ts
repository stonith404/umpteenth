import PlaybookService from '$lib/services/playbook-service';
import SettingsService from '$lib/services/settings-service';
import type { PageLoad } from './$types';

// The workspace's default image seeds the Dockerfile template, since a job's image builds on it
export const load: PageLoad = async ({ params, fetch, depends }) => {
	depends('app:playbook');
	const [playbook, settings] = await Promise.all([
		new PlaybookService(fetch).get(params.id),
		new SettingsService(fetch).get()
	]);
	return { playbook, defaultImage: settings.defaultImage };
};
