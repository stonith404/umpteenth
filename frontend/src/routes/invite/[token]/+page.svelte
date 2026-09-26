<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { isApiError } from '$lib/api/api-error';
	import type { InvitePreview } from '$lib/api/types';
	import Logo from '$lib/components/logo.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Spinner } from '$lib/components/ui/spinner';
	import UserService from '$lib/services/user-service';
	import WorkspaceService from '$lib/services/workspace-service';
	import { apiErrorToast, getErrorMessage } from '$lib/utils/error-util';
	import { formatRelative } from '$lib/utils/format-util';
	import { loginUrl } from '$lib/utils/redirection-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { enterWorkspace, roleLabels, workspaceInitial } from '$lib/utils/workspace-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';

	const workspaceService = new WorkspaceService();
	const userService = new UserService();

	// Signed-out visitors never get here: the root layout sends them to sign in first, which joins someone without a workspace right away and brings anyone else back here to confirm
	const token = $derived(page.params.token!);

	let preview = $state<InvitePreview | null>(null);
	let problem = $state<{ title: string; message: string } | null>(null);
	// The sign-in page's display title, so the pages outside the app read as one family
	const titleClass =
		"font-display text-[1.875rem] leading-[1.1] font-semibold tracking-[-0.02em] text-balance [font-variation-settings:'opsz'_96]";

	let accepting = $state(false);
	let switchingAccount = $state(false);

	// People with a work and a personal login need to know which one the invite would join
	const signedInAs = $derived(page.data.user?.email || page.data.user?.name);

	onMount(async () => {
		const result = await tryCatch(workspaceService.lookupInvite(token));
		if (result.error) {
			problem = isApiError(result.error, 'not_found')
				? {
						title: 'This invite no longer works',
						message: 'It was used already or revoked. Ask whoever invited you for a new one.'
					}
				: { title: "This invite can't be opened", message: getErrorMessage(result.error) };
			return;
		}
		preview = result.data;
		if (preview.expired) {
			problem = {
				title: 'This invite has expired',
				message: 'Ask whoever invited you for a new one.'
			};
		}
	});

	async function accept() {
		accepting = true;
		const result = await tryCatch(workspaceService.acceptInvite(token));
		if (result.error) {
			accepting = false;
			apiErrorToast(result.error, 'Failed to join the workspace');
			return;
		}
		toast.success(`Joined ${result.data.name}`);
		await enterWorkspace();
	}

	// Signs out and comes back to this invite after signing in with the account it was meant for
	async function useAnotherAccount() {
		switchingAccount = true;
		const result = await tryCatch(userService.logout());
		if (result.error) {
			switchingAccount = false;
			apiErrorToast(result.error, 'Failed to sign out');
			return;
		}
		await goto(loginUrl(page.url.pathname), { invalidateAll: true });
	}
</script>

<svelte:head>
	<title>Invite · Umpteenth</title>
</svelte:head>

<div class="grid min-h-svh grid-rows-[auto_1fr_auto] px-6 py-7 md:px-10">
	<header class="flex items-center gap-2.5">
		<Logo class="size-8" />
		<Wordmark class="h-[17px]" />
	</header>

	<main
		class="mx-auto flex w-full max-w-sm flex-col items-stretch justify-center gap-5 py-10 text-center"
	>
		<!-- A broken invite uses the same frame as a working one, with a warning in place of the workspace -->
		{#if problem}
			<div class="flex flex-col items-center gap-3">
				<span
					class="bg-warning-tint text-warning-foreground flex size-12 items-center justify-center rounded-xl"
					aria-hidden="true"
				>
					<CircleAlertIcon class="size-6" />
				</span>
				<h1 class={titleClass}>{problem.title}</h1>
				<p class="text-muted-foreground max-w-[34ch] text-balance">{problem.message}</p>
			</div>
			<div class="flex justify-center">
				<Button href="/" variant="outline">Go to Umpteenth</Button>
			</div>
		{:else if !preview}
			<div class="flex justify-center"><Spinner /></div>
		{:else}
			<div class="flex flex-col items-center gap-3">
				<!-- The neutral tile of the workspace switcher, so the workspace looks the same here as in the sidebar right after joining -->
				<span
					class="bg-fill text-fill-foreground flex size-12 items-center justify-center rounded-xl text-xl font-semibold"
					aria-hidden="true"
				>
					{workspaceInitial(preview.workspaceName)}
				</span>
				<h1 class={titleClass}>
					{#if preview.alreadyMember}
						You're in {preview.workspaceName}
					{:else}
						Join {preview.workspaceName}
					{/if}
				</h1>
				<p class="text-muted-foreground max-w-[34ch] text-balance">
					{#if preview.alreadyMember}
						You're a member already, so the invite stays unused for whoever it was meant for.
					{:else if preview.invitedBy}
						{preview.invitedBy} invited you to join as {roleLabels[preview.role].toLowerCase()}.
					{:else}
						You're invited to join as {roleLabels[preview.role].toLowerCase()}.
					{/if}
				</p>
				{#if !preview.alreadyMember}
					<p class="text-muted-foreground text-sm">
						The invite expires {formatRelative(preview.expiresAt)}
					</p>
				{/if}
			</div>
			<div class="flex flex-col gap-2.5">
				<Button size="lg" onclick={accept} isLoading={accepting}>
					{preview.alreadyMember ? 'Open workspace' : 'Join workspace'}
				</Button>
				<Button href="/" variant="ghost">Not now</Button>
			</div>
			{#if signedInAs}
				<p class="text-muted-foreground text-sm text-balance">
					Signed in as <span class="text-foreground font-medium break-all">{signedInAs}</span>
					·
					<button
						type="button"
						class="text-foreground focus-visible:ring-ring rounded-sm underline underline-offset-4 outline-none hover:opacity-80 focus-visible:ring-2 disabled:opacity-50"
						disabled={switchingAccount}
						onclick={useAnotherAccount}
					>
						Use another account
					</button>
				</p>
			{/if}
		{/if}
	</main>

	<div class="h-8" aria-hidden="true"></div>
</div>
