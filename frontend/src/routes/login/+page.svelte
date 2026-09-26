<script lang="ts">
	import { page } from '$app/state';
	import type { LoginProvider } from '$lib/api/types';
	import Logo from '$lib/components/logo.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { getLoginErrorMessage } from '$lib/utils/error-util';
	import { docsUrl } from '$lib/navigation';
	import { lastLoginProvider } from '$lib/utils/login-provider-util';
	import { providerLoginUrl } from '$lib/utils/redirection-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import { SvelteSet } from 'svelte/reactivity';
	import DitherField from './dither-field.svelte';
	import GitHubIcon from './github-icon.svelte';

	let { data } = $props();

	const error = $derived(page.url.searchParams.get('error'));
	const redirect = $derived(page.url.searchParams.get('redirect'));

	// The primary provider gets the large button, and so does a lone provider that isn't marked primary
	const primary = $derived(
		data.providers.find((p) => p.primary) ??
			(data.providers.length === 1 ? data.providers[0] : undefined)
	);
	const others = $derived(data.providers.filter((p) => p !== primary));

	// Pointing out the provider used last time only helps when there is a choice
	const lastUsed = $derived(data.providers.length > 1 ? lastLoginProvider() : null);

	// An icon that fails to load falls back to the generic one instead of showing a broken image
	const brokenIcons = new SvelteSet<string>();
</script>

<svelte:head>
	<title>Sign in · Umpteenth</title>
</svelte:head>

{#snippet providerButton(provider: LoginProvider, large: boolean)}
	<!-- The login endpoint belongs to the backend, so the SvelteKit router must not handle this link -->
	<Button
		href={providerLoginUrl(provider.id, redirect)}
		variant={large ? 'default' : 'outline'}
		size={large ? 'lg' : 'default'}
		class="relative w-full"
		data-sveltekit-reload
	>
		{#if provider.icon && !brokenIcons.has(provider.id)}
			<img
				src={provider.icon}
				alt=""
				class="size-4 shrink-0 object-contain"
				onerror={() => brokenIcons.add(provider.id)}
			/>
		{:else if provider.type === 'github'}
			<GitHubIcon class="size-4 shrink-0" />
		{:else}
			<LogInIcon />
		{/if}
		<!-- The label stands a little further from the provider's logo than the button's usual icon gap -->
		<span class="ml-0.5 truncate">Sign in with {provider.name}</span>
		<!-- The hint sits on the top edge, so it never takes room from the label in a narrow column -->
		{#if provider.id === lastUsed}
			<Badge variant="outline" floating class="absolute -top-2.5 right-3 h-5">Last used</Badge>
		{/if}
	</Button>
{/snippet}

<div class="grid min-h-svh md:grid-cols-sign-in">
	<main class="grid grid-rows-frame px-6 py-7 md:px-10 lg:px-12">
		<header class="flex items-center gap-2.5">
			<Logo class="size-8" />
			<Wordmark class="h-4.25" />
		</header>

		<div
			class="mx-auto flex w-full max-w-sm flex-col items-stretch justify-center gap-5 py-10 text-center"
		>
			<div class="flex flex-col items-center gap-2">
				<h1 class="display-title">Sign in to Umpteenth</h1>
				<p class="text-muted-foreground max-w-lede-narrow text-balance">
					Continue with your team's account.
				</p>
			</div>
			{#if error}
				<Alert.Root variant="destructive" class="text-left">
					<CircleAlertIcon />
					<Alert.Title>Sign-in failed</Alert.Title>
					<Alert.Description>{getLoginErrorMessage(error)}</Alert.Description>
				</Alert.Root>
			{/if}
			{#if data.providers.length === 0}
				<Alert.Root variant="warning" class="text-left">
					<CircleAlertIcon />
					<Alert.Title>Sign-in is not configured</Alert.Title>
					<!-- The option is a config key, so it is set in the code font, and the link leads to the page on sign-in providers -->
					<Alert.Description>
						Add a sign-in provider under <code class="font-mono text-code-inline"
							>auth.providers</code
						>
						in the
						<a href={docsUrl('/deployment/sign-in/')} target="_blank" rel="noopener"
							>server configuration</a
						>.
					</Alert.Description>
				</Alert.Root>
			{:else}
				<div class="flex flex-col gap-2.5">
					{#if primary}
						{@render providerButton(primary, true)}
					{/if}
					{#if primary && others.length > 0}
						<div class="text-muted-foreground flex items-center gap-3 py-1 text-sm">
							<span class="bg-border h-px flex-1"></span>
							or
							<span class="bg-border h-px flex-1"></span>
						</div>
					{/if}
					{#each others as provider (provider.id)}
						{@render providerButton(provider, false)}
					{/each}
				</div>
			{/if}
		</div>

		<!-- Mirrors the header's height so the form sits at the true vertical center -->
		<div class="h-8" aria-hidden="true"></div>
	</main>

	<!-- The brand panel stays ink in both themes because the lime only reads on dark, and small screens skip it entirely -->
	<aside class="bg-brand-ink relative hidden overflow-hidden border-l md:block" aria-hidden="true">
		<DitherField class="absolute inset-0" />
		<div
			class="from-brand-ink/95 via-brand-ink/60 pointer-events-none absolute inset-x-0 bottom-0 h-2/5 bg-linear-to-t to-transparent mask-r-from-35% mask-r-to-72%"
		></div>
		<p class="sign-in-tagline text-brand-ink-foreground absolute">
			The umpteenth time runs itself.
		</p>
	</aside>
</div>
