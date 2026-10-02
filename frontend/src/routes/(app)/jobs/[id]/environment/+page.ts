import ImageService from '#lib/services/image-service.js';
import PlaybookService from '#lib/services/playbook-service.js';
import SettingsService from '#lib/services/settings-service.js';
import { tryCatch } from '#lib/utils/try-catch-util.js';
import type { PageLoad } from './$types';

// The workspace's default image seeds the Dockerfile template of jobs without a base image of their own
// One build is enough to know whether the page shows the builds table, so a job that never had a Dockerfile gets a calm page instead
export const load: PageLoad = async ({ params, fetch, depends }) => {
	depends('app:playbook');
	const [playbook, settings, builds] = await Promise.all([
		new PlaybookService(fetch).get(params.id),
		new SettingsService(fetch).get(),
		tryCatch(new ImageService(fetch).list(params.id, { page: 1, pageSize: 1 }))
	]);
	// A failed count must not take the page down, so the builds table shows instead and reports the error itself
	const hasBuilds = builds.error ? true : builds.data.total > 0;
	return { playbook, defaultImage: settings.defaultImage, hasBuilds };
};
