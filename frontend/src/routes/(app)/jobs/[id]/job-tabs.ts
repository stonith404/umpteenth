// The tabs of the job page, each its own route so they are linkable and load only their own data
export const jobTabs = [
	{ value: 'overview', label: 'Overview', path: '' },
	{ value: 'runs', label: 'Runs', path: '/runs' },
	{ value: 'playbook', label: 'Playbook', path: '/playbook' },
	{ value: 'environment', label: 'Environment', path: '/environment' },
	{ value: 'state', label: 'State', path: '/state' },
	{ value: 'settings', label: 'Settings', path: '/settings' }
] as const;

export function activeJobTab(pathname: string, jobId: string) {
	const base = `/jobs/${jobId}`;
	const rest = pathname.startsWith(base) ? pathname.slice(base.length) : '';
	return jobTabs.find((tab) => tab.path !== '' && rest.startsWith(tab.path))?.value ?? 'overview';
}
