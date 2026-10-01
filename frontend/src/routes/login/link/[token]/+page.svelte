<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { isApiError } from '$lib/api/api-error';
	import Logo from '$lib/components/logo.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Spinner } from '$lib/components/ui/spinner';
	import UserService from '$lib/services/user-service';
	import { getErrorMessage } from '$lib/utils/error-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import { onMount } from 'svelte';

	const userService = new UserService();

	let problem = $state<{ title: string; message: string } | null>(null);

	// The link signs in as soon as it opens, since it only works once anyway, and it replaces the session of anyone signed in already
	onMount(async () => {
		const result = await tryCatch(userService.useSignInLink(page.params.token!));
		if (result.error) {
			problem = isApiError(result.error, 'not_found')
				? {
						title: 'This sign-in link no longer works',
						message:
							'It was used already, replaced by a newer one, or expired. Ask an instance admin for a new one.'
					}
				: { title: "This sign-in link can't be used", message: getErrorMessage(result.error) };
			return;
		}
		await goto(result.data.redirect, { invalidateAll: true, replaceState: true });
	});
</script>

<svelte:head>
	<title>Sign in · Umpteenth</title>
</svelte:head>

<!-- The frame of the invite page, which also stands between a link and the app -->
<div class="grid min-h-svh grid-rows-frame px-6 py-7 md:px-10">
	<header class="flex items-center gap-2.5">
		<Logo class="size-8" />
		<Wordmark class="h-4.25" />
	</header>

	<main
		class="mx-auto flex w-full max-w-sm flex-col items-stretch justify-center gap-5 py-10 text-center"
	>
		{#if problem}
			<div class="flex flex-col items-center gap-3">
				<span
					class="bg-warning-tint text-warning-foreground flex size-12 items-center justify-center rounded-xl"
					aria-hidden="true"
				>
					<CircleAlertIcon class="size-6" />
				</span>
				<h1 class="display-title">{problem.title}</h1>
				<p class="text-muted-foreground max-w-lede text-balance">{problem.message}</p>
			</div>
			<div class="flex justify-center">
				<Button href="/login" variant="outline">Go to sign in</Button>
			</div>
		{:else}
			<div class="flex flex-col items-center gap-3">
				<Spinner />
				<p class="text-muted-foreground">Signing you in</p>
			</div>
		{/if}
	</main>

	<div class="h-8" aria-hidden="true"></div>
</div>
