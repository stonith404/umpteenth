import BriefcaseIcon from '@lucide/svelte/icons/briefcase';
import LayoutDashboardIcon from '@lucide/svelte/icons/layout-dashboard';
import PlayIcon from '@lucide/svelte/icons/play';
import ServerIcon from '@lucide/svelte/icons/server';
import SettingsIcon from '@lucide/svelte/icons/settings';
import type { Component } from 'svelte';

export type NavItem = {
	label: string;
	href: string;
	icon: Component;
};

export type NavGroup = {
	// Groups without a label sit at the top, like the account home in the Cloudflare dashboard
	label?: string;
	items: NavItem[];
};

export const navGroups: NavGroup[] = [
	{ items: [{ label: 'Dashboard', href: '/', icon: LayoutDashboardIcon }] },
	{
		label: 'Automate',
		items: [
			{ label: 'Jobs', href: '/jobs', icon: BriefcaseIcon },
			{ label: 'Runs', href: '/runs', icon: PlayIcon }
		]
	},
	{ label: 'Connect', items: [{ label: 'MCP Servers', href: '/mcp', icon: ServerIcon }] }
];

export const mainNav: NavItem[] = navGroups.flatMap((group) => group.items);

export const secondaryNav: NavItem[] = [
	{ label: 'Settings', href: '/settings', icon: SettingsIcon }
];

export const settingsTabs = [
	{ value: 'general', label: 'General', href: '/settings/general' },
	{ value: 'providers', label: 'Providers & models', href: '/settings/providers' },
	{ value: 'secrets', label: 'Secrets', href: '/settings/secrets' },
	{ value: 'tokens', label: 'API tokens', href: '/settings/tokens' }
] as const;

// Breadcrumb labels of static route segments
// Pages with dynamic segments (e.g. a run ID) add their own labels through `breadcrumbLabels` in their load data
export const segmentLabels: Record<string, string> = {
	runs: 'Runs',
	jobs: 'Jobs',
	mcp: 'MCP Servers',
	settings: 'Settings',
	general: 'General',
	providers: 'Providers & models',
	secrets: 'Secrets',
	tokens: 'API tokens',
	new: 'New job',
	playbook: 'Playbook',
	environment: 'Environment',
	state: 'State'
};

// The dashboard only matches itself, every other item also matches its sub-pages
export function isNavItemActive(href: string, pathname: string) {
	if (href === '/') return pathname === '/';
	return pathname === href || pathname.startsWith(`${href}/`);
}
