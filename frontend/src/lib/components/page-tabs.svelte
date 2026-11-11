<!--
	Cloudflare's sub-nav strip for a resource whose sections are routes of their own, e.g. a job: a 58px bar under the header with segmented tabs on the left and the resource's actions on the right
	Every tab is a link, so it preloads on hover, opens in a new browser tab on cmd-click, and keeps the current page visible while the next one loads
	Usage: <PageTabs tabs={[{ label: 'Overview', href: '/jobs/1' }]} label="Job sections" {actions}>{@render children()}</PageTabs>
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
	import * as Tabs from '#lib/components/ui/tabs/index.js';
	import type { Snippet } from 'svelte';

	let {
		tabs,
		label,
		actions,
		children
	}: {
		tabs: readonly PageTab[];
		// The accessible name of the tab list, e.g. `Job sections`
		label?: string;
		// Buttons at the right end of the strip, set in the strip's compact size
		actions?: Snippet;
		// The active tab's page, rendered in a tab panel labelled by its tab
		children: Snippet;
	} = $props();

	const id = $props.id();

	const current = $derived(activePageTab(tabs, page.url.pathname)?.href ?? tabs[0]?.href);

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

<Tabs.Root value={shown} activationMode="manual" spacing="lg">
	<!-- The strip runs edge to edge under the header, so it cancels the page's gutters and keeps the header's own 16px -->
	<div
		class="-mx-4 -mt-4 flex min-h-14.5 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b px-4 py-2.5 md:-mx-8 md:-mt-8"
	>
		<!-- Six tabs outgrow a phone, so the track scrolls sideways there instead of wrapping -->
		<div
			class="-m-0.5 max-w-full overflow-x-auto p-0.5 scrollbar-none [&::-webkit-scrollbar]:hidden"
		>
			<Tabs.List aria-label={label}>
				{#each tabs as tab (tab.href)}
					<Tabs.Trigger value={tab.href} id={triggerId(tab.href)} class="flex-none">
						{#snippet child({ props })}
							<!-- Focus stays on the tab and the page keeps its scroll position, like switching tabs in place -->
							<a
								{...props}
								href={tab.href}
								data-sveltekit-reset="false"
								onclick={undefined}
								onkeydown={(event) => handleKeydown(event, props.onkeydown)}
							>
								{tab.label}
							</a>
						{/snippet}
					</Tabs.Trigger>
				{/each}
			</Tabs.List>
		</div>
		{#if actions}
			<div data-slot="page-tabs-actions" class="flex items-center gap-2">
				{@render actions()}
			</div>
		{/if}
	</div>
	<div
		role="tabpanel"
		data-slot="page-tabs-panel"
		aria-labelledby={current ? triggerId(current) : undefined}
		class="min-w-0 outline-none"
	>
		{@render children()}
	</div>
</Tabs.Root>
