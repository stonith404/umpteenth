<!--
	The body of both error pages: what went wrong, and the way out
	Inside the app it sits in the shell under the header, the root error page shows it full screen in the frame of the sign-in and invite pages
-->
<script lang="ts">
	import { goto, invalidateAll } from '$app/navigation';
	import { page } from '$app/state';
	import ErrorMark from '$lib/components/error-mark.svelte';
	import Logo from '$lib/components/logo.svelte';
	import RequestId from '$lib/components/request-id.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
	import {
		errorPageContent,
		recentlyRedirectedToLogin,
		rememberLoginRedirect
	} from '$lib/utils/error-util';
	import { LOGIN_PATH, loginUrl } from '$lib/utils/redirection-util';
	import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';

	let {
		branded = false
	}: {
		// Shows the page full screen with the wordmark, for the error page outside the app shell
		branded?: boolean;
	} = $props();

	const content = $derived(
		errorPageContent(page.error, page.status, page.route.id, page.url.pathname)
	);

	// Signing in again fixes a lost session, and the login page brings the user back here afterwards
	// A page that fails the same way right after signing in would bounce between here and the login page forever, so the second attempt shows the error instead
	const returnTo = $derived(page.url.pathname + page.url.search);
	const redirecting = $derived(
		content.sessionLost && page.url.pathname !== LOGIN_PATH && !recentlyRedirectedToLogin(returnTo)
	);
	$effect(() => {
		if (!redirecting) return;
		rememberLoginRedirect(returnTo);
		void goto(loginUrl(returnTo), { replaceState: true, invalidateAll: true });
	});

	// The way back of the full-screen page can lead straight into the same failure: it links to the page that failed, or it needs a session a signed-out visitor doesn't have, e.g. when the sign-in page's providers can't load
	// Trying again is then the only way out, so it replaces the way back
	const backIsDeadEnd = $derived.by(() => {
		if (!branded) return false;
		if (new URL(content.back.href, page.url).pathname === page.url.pathname) return true;
		return !page.data.user && !content.sessionLost;
	});
	const showRetry = $derived(content.retry || backIsDeadEnd);

	let retrying = $state(false);

	// Reruns the page's load functions, which replaces this page with the real one when the request succeeds
	async function retry() {
		retrying = true;
		try {
			await invalidateAll();
		} finally {
			retrying = false;
		}
	}
</script>

<svelte:head>
	<title>{redirecting ? 'Sign in' : content.title} · Umpteenth</title>
</svelte:head>

{#snippet actions()}
	{#if showRetry}
		<Button isLoading={retrying} onclick={retry}>
			{#if !retrying}
				<RotateCwIcon data-icon="inline-start" />
			{/if}
			Try again
		</Button>
	{/if}
	{#if !backIsDeadEnd}
		<Button href={content.back.href} variant="outline">{content.back.label}</Button>
	{/if}
{/snippet}

{#if redirecting}
	<!-- Nothing to show while the login page loads -->
{:else if branded}
	<!-- The frame of the sign-in and invite pages: the wordmark top left, then the message centered in the remaining height -->
	<div class="grid min-h-svh grid-rows-frame px-6 py-7 md:px-10" data-slot="error-page">
		<header class="flex items-center gap-2.5">
			<Logo class="size-8" />
			<Wordmark class="h-4.25" />
		</header>

		<main
			class="mx-auto flex w-full max-w-sm flex-col items-stretch justify-center gap-5 py-10 text-center"
		>
			<div class="flex flex-col items-center gap-3">
				<ErrorMark class="mb-1" />
				<!-- The sign-in page's display title, so the pages outside the app read as one family -->
				<h1 class="display-title">{content.title}</h1>
				<p class="text-muted-foreground max-w-lede text-balance">{content.description}</p>
				{#if content.requestId}
					<RequestId id={content.requestId} />
				{/if}
			</div>
			<div class="flex flex-wrap justify-center gap-2">
				{@render actions()}
			</div>
		</main>

		<!-- Mirrors the header's height so the message sits at the true vertical center -->
		<div class="h-8" aria-hidden="true"></div>
	</div>
{:else}
	<Empty.Root size="page" class="flex-none" data-slot="error-page">
		<Empty.Header>
			<Empty.Media>
				<ErrorMark />
			</Empty.Media>
			<Empty.Title role="heading" aria-level={1} size="page">{content.title}</Empty.Title>
			<Empty.Description size="page">{content.description}</Empty.Description>
			{#if content.requestId}
				<RequestId id={content.requestId} />
			{/if}
		</Empty.Header>
		<Empty.Content>
			<div class="flex flex-wrap justify-center gap-2">
				{@render actions()}
			</div>
		</Empty.Content>
	</Empty.Root>
{/if}
