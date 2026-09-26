import { settingsTabs } from '$lib/navigation';
import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = () => {
	redirect(307, settingsTabs[0].href);
};
