<script lang="ts" module>
	import { MediaQuery } from 'svelte/reactivity';

	// From Tailwind's sm breakpoint up there is room for the inline action next to the menu
	const wide = new MediaQuery('min-width: 40rem', true);
</script>

<!--
@component
The standard actions of a table row: at most one inline outline button for the row's main forward action, then a '⋯' menu with the rest.
Use it in a column made by `actionsColumn`, e.g.
`<RowActions name={secret.name} items={[{ label: 'Update value', icon: PencilIcon, onSelect: () => edit(secret) }, { label: 'Delete', icon: Trash2Icon, variant: 'destructive', onSelect: () => remove(secret) }]} />`
-->
<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import EllipsisIcon from '@lucide/svelte/icons/ellipsis';
	import type { RowAction } from './types';

	type Props = {
		// The row's name, which makes the menu button's accessible name 'Actions for <name>'
		name: string;
		// The row's one main forward action ('Open', 'Log in', 'Test') as an outline button; on phones it moves to the top of the menu
		inline?: RowAction | false | null;
		// The menu items; falsy entries are skipped so callers can write `canDelete && { … }`, and destructive items always go last
		items?: (RowAction | false | null | undefined)[];
	};

	let { name, inline, items = [] }: Props = $props();

	const inlineAction = $derived(inline && wide.current ? inline : null);
	const menuItems = $derived.by(() => {
		const all = items.filter((item): item is RowAction => !!item);
		if (inline && !wide.current) all.unshift(inline);
		return {
			regular: all.filter((item) => item.variant !== 'destructive'),
			destructive: all.filter((item) => item.variant === 'destructive')
		};
	});
	const hasMenu = $derived(menuItems.regular.length > 0 || menuItems.destructive.length > 0);
</script>

{#snippet menuItem(item: RowAction)}
	{#if item.href && !item.disabled}
		<DropdownMenu.Item variant={item.variant}>
			{#snippet child({ props })}
				<a href={item.href} {...props}>
					{#if item.icon}
						<item.icon />
					{/if}
					{item.label}
				</a>
			{/snippet}
		</DropdownMenu.Item>
	{:else}
		<DropdownMenu.Item
			variant={item.variant}
			disabled={item.disabled}
			onSelect={() => item.onSelect?.()}
		>
			{#if item.icon}
				<item.icon />
			{/if}
			{item.label}
		</DropdownMenu.Item>
	{/if}
{/snippet}

{#if inlineAction || hasMenu}
	<!-- The 32px buttons reach into the cell's padding, so rows with actions stay as tall as a line of text while the hit area and focus ring keep their full size -->
	<div data-slot="row-actions" class="-my-2 flex items-center justify-end gap-1">
		{#if inlineAction}
			<Button
				variant="outline"
				size="sm"
				href={inlineAction.href}
				disabled={inlineAction.disabled}
				onclick={() => inlineAction.onSelect?.()}
			>
				{#if inlineAction.icon}
					<inlineAction.icon data-icon="inline-start" />
				{/if}
				{inlineAction.label}
			</Button>
		{/if}
		{#if hasMenu}
			<DropdownMenu.Root>
				<DropdownMenu.Trigger>
					{#snippet child({ props })}
						<Button {...props} variant="ghost" size="icon-sm" aria-label={`Actions for ${name}`}>
							<EllipsisIcon />
						</Button>
					{/snippet}
				</DropdownMenu.Trigger>
				<DropdownMenu.Content align="end">
					{#each menuItems.regular as item, i (i)}
						{@render menuItem(item)}
					{/each}
					{#if menuItems.destructive.length > 0 && menuItems.regular.length > 0}
						<DropdownMenu.Separator />
					{/if}
					{#each menuItems.destructive as item, i (i)}
						{@render menuItem(item)}
					{/each}
				</DropdownMenu.Content>
			</DropdownMenu.Root>
		{/if}
	</div>
{/if}
