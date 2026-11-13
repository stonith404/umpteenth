<script lang="ts">
	import { goto } from '$app/navigation';
	import type { User } from '#lib/api/types.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import UserService from '#lib/services/user-service.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { clearNewJobDrafts } from '#lib/utils/job-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import { DOCS_URL } from '#lib/navigation.js';
	import BookOpenIcon from 'phosphor-svelte/lib/BookOpen';
	import ArrowSquareOutIcon from 'phosphor-svelte/lib/ArrowSquareOut';
	import CircleHalfIcon from 'phosphor-svelte/lib/CircleHalf';
	import MonitorIcon from 'phosphor-svelte/lib/Monitor';
	import MoonIcon from 'phosphor-svelte/lib/Moon';
	import SignOutIcon from 'phosphor-svelte/lib/SignOut';
	import SunIcon from 'phosphor-svelte/lib/Sun';
	import UserIcon from 'phosphor-svelte/lib/User';
	import { resetMode, setMode, userPrefersMode } from 'mode-watcher';

	let { user }: { user: User } = $props();

	const userService = new UserService();

	const displayName = $derived(user.name || user.email || 'Signed in');

	async function logout() {
		const result = await tryCatch(userService.logout());
		if (result.error) {
			apiErrorToast(result.error, 'Failed to sign out');
			return;
		}
		clearNewJobDrafts();
		await goto('/login', { refreshAll: true });
	}
</script>

<DropdownMenu.Root>
	<!-- A plain person icon like the dashboard's, which keeps the header a row of quiet ghost buttons -->
	<DropdownMenu.Trigger aria-label="Account menu">
		{#snippet child({ props })}
			<Button variant="ghost" size="icon-sm" {...props}>
				<UserIcon />
			</Button>
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
			<!-- Only passkey accounts keep a profile and passkeys of their own, sign-in providers manage the others -->
			{#if user.passkeyAccount}
				<DropdownMenu.Item>
					{#snippet child({ props })}
						<a href="/account" {...props}>
							<UserIcon />
							Account
						</a>
					{/snippet}
				</DropdownMenu.Item>
			{/if}
			<!-- The theme lives in the account menu like the dashboard's Appearance, rather than taking a header button of its own -->
			<DropdownMenu.Sub>
				<DropdownMenu.SubTrigger>
					<CircleHalfIcon />
					Appearance
				</DropdownMenu.SubTrigger>
				<DropdownMenu.SubContent>
					<DropdownMenu.RadioGroup
						value={userPrefersMode.current}
						onValueChange={(value) =>
							value === 'system' ? resetMode() : setMode(value as 'light')}
					>
						<DropdownMenu.RadioItem value="light"><SunIcon /> Light</DropdownMenu.RadioItem>
						<DropdownMenu.RadioItem value="dark"><MoonIcon /> Dark</DropdownMenu.RadioItem>
						<DropdownMenu.RadioItem value="system"><MonitorIcon /> System</DropdownMenu.RadioItem>
					</DropdownMenu.RadioGroup>
				</DropdownMenu.SubContent>
			</DropdownMenu.Sub>
			<DropdownMenu.Item>
				{#snippet child({ props })}
					<a href={DOCS_URL} target="_blank" rel="noopener" {...props}>
						<BookOpenIcon />
						Documentation
						<ArrowSquareOutIcon class="text-muted-foreground ml-auto" />
					</a>
				{/snippet}
			</DropdownMenu.Item>
			<DropdownMenu.Item onclick={logout}>
				<SignOutIcon />
				Sign out
			</DropdownMenu.Item>
		</DropdownMenu.Group>
	</DropdownMenu.Content>
</DropdownMenu.Root>
