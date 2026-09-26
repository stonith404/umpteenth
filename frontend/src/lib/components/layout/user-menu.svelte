<script lang="ts">
	import { goto } from '$app/navigation';
	import type { User } from '$lib/api/types';
	import * as Avatar from '$lib/components/ui/avatar';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import UserService from '$lib/services/user-service';
	import unsavedChanges from '$lib/stores/unsaved-changes-store.svelte';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
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
		// With unsaved changes the navigation below is blocked by the unsaved-changes bar, so the session must stay intact too
		if (!unsavedChanges.hasChanges) {
			const result = await tryCatch(userService.logout());
			if (result.error) {
				apiErrorToast(result.error, 'Failed to sign out');
				return;
			}
		}
		await goto('/login', { invalidateAll: true });
	}
</script>

<DropdownMenu.Root>
	<DropdownMenu.Trigger class="rounded-full" aria-label="Account menu">
		<Avatar.Root class="size-8">
			<Avatar.Fallback class="text-xs font-medium">{initials}</Avatar.Fallback>
		</Avatar.Root>
	</DropdownMenu.Trigger>
	<DropdownMenu.Content class="min-w-56" align="end">
		<DropdownMenu.Label class="font-normal">
			<div class="flex flex-col gap-1">
				<p class="text-sm leading-none font-medium">{displayName}</p>
				{#if user.email && user.email !== displayName}
					<p class="text-muted-foreground text-xs leading-none">{user.email}</p>
				{/if}
			</div>
		</DropdownMenu.Label>
		<DropdownMenu.Separator />
		<DropdownMenu.Group>
			<DropdownMenu.Item onclick={logout}>
				<LogOutIcon />
				Log out
			</DropdownMenu.Item>
		</DropdownMenu.Group>
	</DropdownMenu.Content>
</DropdownMenu.Root>
