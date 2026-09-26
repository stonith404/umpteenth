<!--
	Kumo underline tabs for the sections of a page that are routes of their own, e.g. the job, settings and admin tabs
	Every tab is a link, so it preloads on hover, opens in a new browser tab on cmd-click, and keeps the current page visible while the next one loads
	Usage: <PageTabs tabs={[{ label: 'General', href: '/settings/general' }]} label="Settings sections">{@render children()}</PageTabs>
-->
<script lang="ts" module>
	export type PageTab = {
		label: string;
		// The tab's route, which also marks it active on every page below it
		href: string;
	};

	// Finds the tab a path belongs to: the one whose href is the longest match of the path or one of its parents
	// The longest match lets a tab at the base of a section, like a job's Overview at `/jobs/<id>`, sit next to tabs below it
	export function activePageTab<T extends PageTab>(
		tabs: readonly T[],
		pathname: string
	): T | undefined {
		let best: T | undefined;
		for (const tab of tabs) {
			const base = tab.href.replace(/\/+$/, '');
			const matches = pathname === base || pathname.startsWith(`${base}/`);
			if (matches && (!best || base.length > best.href.replace(/\/+$/, '').length)) best = tab;
		}
		return best;
	}
</script>

<script lang="ts">
	import { navigating, page } from '$app/state';
	import * as Tabs from '$lib/components/ui/tabs';
	import { cn } from '$lib/utils/style';
	import type { Snippet } from 'svelte';

	let {
		tabs,
		label,
		active,
		class: className,
		panelClass,
		children
	}: {
		tabs: readonly PageTab[];
		// The accessible name of the tab list, e.g. `Job sections`
		label?: string;
		// The href of the active tab, for pages whose URL doesn't decide it, otherwise the URL picks it
		active?: string;
		class?: string;
		// Classes of the panel that holds `children`, e.g. `flex flex-col gap-6`
		panelClass?: string;
		// The active tab's page, rendered in a tab panel labelled by its tab
		children?: Snippet;
	} = $props();

	const id = $props.id();

	const current = $derived(active ?? activePageTab(tabs, page.url.pathname)?.href ?? tabs[0]?.href);

	// The underline moves as soon as a navigation to another tab starts, and moves back if it fails
	const pending = $derived(
		navigating.to ? activePageTab(tabs, navigating.to.url.pathname)?.href : undefined
	);
	const shown = $derived(pending ?? current);

	function triggerId(href: string) {
		return `${id}-tab-${tabs.findIndex((tab) => tab.href === href)}`;
	}

	// Arrow keys move the focus between tabs as usual, while Enter and Space follow the link rather than switching a panel in place
	function handleKeydown(event: KeyboardEvent, tabKeydown: unknown) {
		if (event.key === 'Enter') return;
		if (event.key === ' ') {
			event.preventDefault();
			(event.currentTarget as HTMLElement).click();
			return;
		}
		if (typeof tabKeydown === 'function') tabKeydown(event);
	}
</script>

<Tabs.Root value={shown} activationMode="manual" class={cn('gap-6', className)}>
	<Tabs.List variant="line" aria-label={label}>
		{#each tabs as tab (tab.href)}
			<Tabs.Trigger value={tab.href} id={triggerId(tab.href)}>
				{#snippet child({ props })}
					<!-- Focus stays on the tab and the page keeps its scroll position, like switching tabs in place -->
					<a
						{...props}
						href={tab.href}
						data-sveltekit-keepfocus
						data-sveltekit-noscroll
						onclick={undefined}
						onkeydown={(event) => handleKeydown(event, props.onkeydown)}
					>
						{tab.label}
					</a>
				{/snippet}
			</Tabs.Trigger>
		{/each}
	</Tabs.List>
	{#if children}
		<div
			role="tabpanel"
			data-slot="page-tabs-panel"
			aria-labelledby={current ? triggerId(current) : undefined}
			class={cn('min-w-0 outline-none', panelClass)}
		>
			{@render children()}
		</div>
	{/if}
</Tabs.Root>
