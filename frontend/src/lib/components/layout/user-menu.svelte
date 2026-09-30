<script lang="ts">
	import { goto } from '$app/navigation';
	import type { User } from '$lib/api/types';
	import * as Avatar from '$lib/components/ui/avatar';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import UserService from '$lib/services/user-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { clearNewJobDrafts } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { DOCS_URL } from '$lib/navigation';
	import BookOpenIcon from '@lucide/svelte/icons/book-open';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import LogOutIcon from '@lucide/svelte/icons/log-out';

	let { user }: { user: User } = $props();

	const userService = new UserService();

	const displayName = $derived(user.name || user.email || 'Signed in');
	const initials = $derived(
		displayName
			.split(/[\s@.]+/)
			.filter(Boolean)
			.slice(0, 2)
			.map((part) => part[0]?.toUpperCase())
			.join('')
	);

	async function logout() {
		const result = await tryCatch(userService.logout());
		if (result.error) {
			apiErrorToast(result.error, 'Failed to sign out');
			return;
		}
		clearNewJobDrafts();
		await goto('/login', { invalidateAll: true });
	}
</script>

<DropdownMenu.Root>
	<!-- The focus outline sits off the avatar's edge, like on filled buttons, since a ring on a photo is hard to see -->
	<DropdownMenu.Trigger aria-label="Account menu">
		{#snippet child({ props })}
			<button
				type="button"
				{...props}
				class="focus-visible:outline-ring rounded-full outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-solid"
			>
				<Avatar.Root class="size-8">
					{#if user.picture}
						<Avatar.Image src={user.picture} alt={displayName} referrerpolicy="no-referrer" />
					{/if}
					<Avatar.Fallback>{initials}</Avatar.Fallback>
				</Avatar.Root>
			</button>
		{/snippet}
	</DropdownMenu.Trigger>
	<!-- Capped, so a long email truncates instead of stretching the menu across the header -->
	<DropdownMenu.Content class="max-w-72 min-w-56" align="end">
		<DropdownMenu.Label>
			<div class="flex flex-col gap-1">
				<p class="truncate text-sm leading-tight font-medium" title={displayName}>{displayName}</p>
				{#if user.email && user.email !== displayName}
					<p class="text-muted-foreground truncate text-xs leading-tight" title={user.email}>
						{user.email}
					</p>
				{/if}
			</div>
		</DropdownMenu.Label>
		<DropdownMenu.Separator />
		<DropdownMenu.Group>
			<DropdownMenu.Item>
				{#snippet child({ props })}
					<a href={DOCS_URL} target="_blank" rel="noopener" {...props}>
						<BookOpenIcon />
						Documentation
						<ExternalLinkIcon class="text-muted-foreground ml-auto" />
					</a>
				{/snippet}
			</DropdownMenu.Item>
			<DropdownMenu.Item onclick={logout}>
				<LogOutIcon />
				Sign out
			</DropdownMenu.Item>
		</DropdownMenu.Group>
	</DropdownMenu.Content>
</DropdownMenu.Root>
