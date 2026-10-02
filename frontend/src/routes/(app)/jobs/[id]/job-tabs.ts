import type { PageTab } from '#lib/components/page-tabs.svelte';

// The tabs of the job page, each its own route so they are linkable and load only their own data
const jobTabs = [
	{ label: 'Overview', path: '' },
	{ label: 'Runs', path: '/runs' },
	{ label: 'Playbook', path: '/playbook' },
	{ label: 'Environment', path: '/environment' },
	{ label: 'State', path: '/state' },
	{ label: 'Settings', path: '/settings' }
] as const;

// The job's tabs as links for PageTabs, which picks the active one from the URL
export function jobPageTabs(jobId: string): PageTab[] {
	return jobTabs.map((tab) => ({ label: tab.label, href: `/jobs/${jobId}${tab.path}` }));
}
