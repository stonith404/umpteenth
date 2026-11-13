import RobotIcon from 'phosphor-svelte/lib/Robot';
import BriefcaseIcon from 'phosphor-svelte/lib/Briefcase';
import KeyIcon from 'phosphor-svelte/lib/Key';
import PasswordIcon from 'phosphor-svelte/lib/Password';
import StackIcon from 'phosphor-svelte/lib/Stack';
import SquaresFourIcon from 'phosphor-svelte/lib/SquaresFour';
import PlayIcon from 'phosphor-svelte/lib/Play';
import PuzzlePieceIcon from 'phosphor-svelte/lib/PuzzlePiece';
import HardDrivesIcon from 'phosphor-svelte/lib/HardDrives';
import GearSixIcon from 'phosphor-svelte/lib/GearSix';
import ShieldIcon from 'phosphor-svelte/lib/Shield';
import SlidersHorizontalIcon from 'phosphor-svelte/lib/SlidersHorizontal';
import UsersIcon from 'phosphor-svelte/lib/Users';
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
	// Pages the item expands to in the sidebar, like Cloudflare's Manage account group
	children?: readonly { label: string; href: string }[];
};

export type NavGroup = {
	// Groups without a label sit at the top, like the account home in the Cloudflare dashboard
	label?: string;
	items: NavItem[];
};

export const navGroups: NavGroup[] = [
	{ items: [{ label: 'Dashboard', href: '/', icon: SquaresFourIcon }] },
	{
		label: 'Automate',
		items: [
			{ label: 'Jobs', href: '/jobs', icon: BriefcaseIcon },
			{ label: 'Runs', href: '/runs', icon: PlayIcon }
		]
	},
	{
		label: 'Connect',
		items: [
			{ label: 'MCP servers', href: '/mcp', icon: HardDrivesIcon },
			{ label: 'Skills', href: '/skills', icon: PuzzlePieceIcon }
		]
	}
];

export const mainNav: NavItem[] = navGroups.flatMap((group) => group.items);

// `icon` and `keywords` only serve the command palette, the sidebar reads `label` and `href`, and the page header the `description` and `docsPath`
export const settingsTabs = [
	{
		label: 'General',
		description: 'Workspace defaults, models, spend limits and notifications.',
		href: '/settings/general',
		icon: SlidersHorizontalIcon,
		keywords:
			'workspace name default models spend limit budget retention usage tokens price cost notifications webhook sandbox image cpu memory timeout delete leave'
	},
	{
		label: 'Members',
		description: 'Everyone who can use this workspace and what they may change.',
		href: '/settings/members',
		docsPath: '/guides/workspaces/',
		icon: UsersIcon,
		keywords: 'invite people team users roles workspace'
	},
	{
		label: 'Providers & models',
		description: 'The model providers and models that jobs run on.',
		href: '/settings/providers',
		docsPath: '/guides/models/',
		icon: RobotIcon,
		keywords: 'llm models openai anthropic api key'
	},
	{
		label: 'Secrets',
		description: 'Encrypted API keys and tokens for jobs and MCP servers.',
		href: '/settings/secrets',
		docsPath: '/guides/sandboxes/#secrets',
		icon: KeyIcon,
		keywords: 'env environment variables password credentials'
	},
	{
		label: 'API tokens',
		description: 'Tokens for the API and the MCP server, for scripts and agents like Claude Code.',
		href: '/settings/tokens',
		docsPath: '/reference/api/',
		icon: PasswordIcon,
		keywords: 'api key token access cli webhook'
	}
] as const;

// The admin area's tabs, where workspaces only show when people can have several
export function adminTabs(user: { workspacesEnabled: boolean }) {
	return [
		{
			label: 'Users',
			description: 'Everyone who signed in to this instance.',
			href: '/admin/users',
			icon: UsersIcon,
			keywords: 'people accounts instance admins'
		},
		...(user.workspacesEnabled
			? [
					{
						label: 'Workspaces',
						description: 'Every workspace on this instance.',
						href: '/admin/workspaces',
						icon: StackIcon,
						keywords: 'teams tenants instance'
					}
				]
			: [])
	];
}

// Settings, and the instance administration for instance admins, each expanding to its pages
export function secondaryNav(user: { isAdmin: boolean; workspacesEnabled: boolean }): NavItem[] {
	const settings: NavItem = {
		label: 'Settings',
		href: '/settings',
		icon: GearSixIcon,
		children: settingsTabs
	};
	if (!user.isAdmin) return [settings];
	return [
		settings,
		{ label: 'Admin', href: '/admin', icon: ShieldIcon, children: adminTabs(user) }
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
	skills: 'Skills',
	settings: 'Settings',
	general: 'General',
	providers: 'Providers & models',
	secrets: 'Secrets',
	tokens: 'API tokens',
	members: 'Members',
	admin: 'Admin',
	account: 'Account',
	users: 'Users',
	workspaces: 'Workspaces',
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
