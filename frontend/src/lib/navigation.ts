import BotIcon from '@lucide/svelte/icons/bot';
import BriefcaseIcon from '@lucide/svelte/icons/briefcase';
import KeyRoundIcon from '@lucide/svelte/icons/key-round';
import KeySquareIcon from '@lucide/svelte/icons/key-square';
import LayersIcon from '@lucide/svelte/icons/layers';
import LayoutDashboardIcon from '@lucide/svelte/icons/layout-dashboard';
import PlayIcon from '@lucide/svelte/icons/play';
import ServerIcon from '@lucide/svelte/icons/server';
import SettingsIcon from '@lucide/svelte/icons/settings';
import ShieldIcon from '@lucide/svelte/icons/shield';
import SlidersHorizontalIcon from '@lucide/svelte/icons/sliders-horizontal';
import UsersIcon from '@lucide/svelte/icons/users';
import type { Component } from 'svelte';

// The documentation site, linked from the user menu and from hints that point at the server configuration
export const DOCS_URL = 'https://umpteenth.dev';

// Builds a link to a page of the documentation, e.g. `docsUrl('/deployment/sign-in/')`
export function docsUrl(path = '/'): string {
	return `${DOCS_URL}${path}`;
}

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
	{ label: 'Connect', items: [{ label: 'MCP servers', href: '/mcp', icon: ServerIcon }] }
];

export const mainNav: NavItem[] = navGroups.flatMap((group) => group.items);

const settingsNavItem: NavItem = { label: 'Settings', href: '/settings', icon: SettingsIcon };
const adminNavItem: NavItem = { label: 'Admin', href: '/admin', icon: ShieldIcon };

// Settings, and the instance administration for instance admins
export function secondaryNav(user: { isAdmin: boolean }): NavItem[] {
	return user.isAdmin ? [settingsNavItem, adminNavItem] : [settingsNavItem];
}

// `icon` and `keywords` only serve the command palette, the settings layout reads `value`, `label` and `href`
export const settingsTabs = [
	{
		value: 'general',
		label: 'General',
		href: '/settings/general',
		icon: SlidersHorizontalIcon,
		keywords:
			'workspace name default models spend limit budget retention usage tokens price cost notifications webhook sandbox image cpu memory timeout delete leave'
	},
	{
		value: 'members',
		label: 'Members',
		href: '/settings/members',
		icon: UsersIcon,
		keywords: 'invite people team users roles workspace'
	},
	{
		value: 'providers',
		label: 'Providers & models',
		href: '/settings/providers',
		icon: BotIcon,
		keywords: 'llm models openai anthropic api key'
	},
	{
		value: 'secrets',
		label: 'Secrets',
		href: '/settings/secrets',
		icon: KeyRoundIcon,
		keywords: 'env environment variables password credentials'
	},
	{
		value: 'tokens',
		label: 'API tokens',
		href: '/settings/tokens',
		icon: KeySquareIcon,
		keywords: 'api key token access cli webhook'
	}
] as const;

// The admin area's tabs, where workspaces only show when people can have several
export function adminTabs(user: { workspacesEnabled: boolean }) {
	return [
		{
			value: 'users',
			label: 'Users',
			href: '/admin/users',
			icon: UsersIcon,
			keywords: 'people accounts instance admins'
		},
		...(user.workspacesEnabled
			? [
					{
						value: 'workspaces',
						label: 'Workspaces',
						href: '/admin/workspaces',
						icon: LayersIcon,
						keywords: 'teams tenants instance'
					}
				]
			: [])
	];
}

// A page the command palette can jump to
export type SearchablePage = NavItem & {
	id: string;
	// Words that find the page besides its label, e.g. 'password' for Secrets
	keywords?: string;
};

// Every page worth jumping to from the command palette: the sidebar's pages, then each settings and admin tab as 'Settings › Secrets'
export function searchablePages(user: { isAdmin: boolean; workspacesEnabled: boolean }) {
	const pages: SearchablePage[] = [...mainNav, ...secondaryNav(user)].map((item) => ({
		...item,
		id: `page-${item.href}`
	}));
	for (const tab of settingsTabs) {
		pages.push({
			id: `page-${tab.href}`,
			label: `Settings › ${tab.label}`,
			href: tab.href,
			icon: tab.icon,
			keywords: tab.keywords
		});
	}
	if (user.isAdmin) {
		for (const tab of adminTabs(user)) {
			pages.push({
				id: `page-${tab.href}`,
				label: `Admin › ${tab.label}`,
				href: tab.href,
				icon: tab.icon,
				keywords: tab.keywords
			});
		}
	}
	return pages;
}

// Breadcrumb labels of static route segments
// Pages with dynamic segments (e.g. a run ID) add their own labels through `breadcrumbLabels` in their load data
export const segmentLabels: Record<string, string> = {
	runs: 'Runs',
	jobs: 'Jobs',
	mcp: 'MCP servers',
	settings: 'Settings',
	general: 'General',
	providers: 'Providers & models',
	secrets: 'Secrets',
	tokens: 'API tokens',
	members: 'Members',
	admin: 'Admin',
	users: 'Users',
	workspaces: 'Workspaces',
	invite: 'Invite',
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
