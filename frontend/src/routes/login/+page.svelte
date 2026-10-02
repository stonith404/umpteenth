<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { LoginProvider } from '#lib/api/types.js';
	import FormInput from '#lib/components/form/form-input.svelte';
	import Logo from '#lib/components/logo.svelte';
	import Wordmark from '#lib/components/wordmark.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import UserService from '#lib/services/user-service.js';
	import { apiErrorToast, getLoginErrorMessage } from '#lib/utils/error-util.js';
	import { preventDefault } from '#lib/utils/event-util.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { docsUrl } from '#lib/navigation.js';
	import { lastLoginProvider } from '#lib/utils/login-provider-util.js';
	import { passkeyAccountSchema } from '#lib/utils/passkey-util.js';
	import { providerLoginUrl, safeRedirectPath } from '#lib/utils/redirection-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import { SvelteSet } from 'svelte/reactivity';
	import DitherField from './dither-field.svelte';
	import GitHubIcon from './github-icon.svelte';

	let { data } = $props();

	const userService = new UserService();

	const error = $derived(page.url.searchParams.get('error'));
	const redirect = $derived(page.url.searchParams.get('redirect'));

	// The primary provider gets the large button, and so does a lone provider that isn't marked primary
	// Passkeys are listed last and are only primary on an instance without sign-in providers
	const redirectProviders = $derived(data.providers.filter((p) => p.type !== 'passkey'));
	const primary = $derived(
		data.providers.find((p) => p.primary) ??
			(redirectProviders.length === 1 ? redirectProviders[0] : undefined)
	);
	const others = $derived(data.providers.filter((p) => p !== primary));
	const passkeys = $derived(data.providers.some((p) => p.type === 'passkey'));

	// Someone without an account can create one with a passkey for an invite link, which the login page has as its redirect
	const fromInvite = $derived(safeRedirectPath(redirect)?.startsWith('/invite/') ?? false);

	// An instance without users is set up by whoever opens it first, and an invite link lets newcomers create their account
	let signingUp = $state(false);
	const creatingAccount = $derived(data.setupOpen || signingUp);

	const form = createForm(passkeyAccountSchema, { name: '', email: '' });
	const inputs = form.inputs;
	let busy = $state(false);

	async function signInWithPasskey() {
		busy = true;
		const result = await tryCatch(userService.signInWithPasskey(redirect));
		busy = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to sign in with a passkey');
			return;
		}
		if (result.data) await goto(result.data.redirect, { refreshAll: true });
	}

	async function signUpWithPasskey() {
		const values = form.validate();
		if (!values) return;

		busy = true;
		const result = await tryCatch(
			userService.signUpWithPasskey({
				name: values.name,
				email: values.email || undefined,
				redirect: safeRedirectPath(redirect) ?? undefined
			})
		);
		busy = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to create the account');
			return;
		}
		if (result.data) await goto(result.data.redirect, { refreshAll: true });
	}

	// Pointing out the provider used last time only helps when there is a choice
	const lastUsed = $derived(data.providers.length > 1 ? lastLoginProvider() : null);

	// An icon that fails to load falls back to the generic one instead of showing a broken image
	const brokenIcons = new SvelteSet<string>();
</script>

<svelte:head>
	<title>Sign in · Umpteenth</title>
</svelte:head>

{#snippet providerButton(provider: LoginProvider, large: boolean)}
	{#if provider.type === 'passkey'}
		<!-- Passkeys sign in right here, with the browser's own prompt -->
		<Button
			variant={large ? 'default' : 'outline'}
			size={large ? 'lg' : 'default'}
			class="relative w-full"
			isLoading={busy}
			onclick={signInWithPasskey}
		>
			<KeyRoundIcon />
			<span class="ml-0.5 truncate">Sign in with a passkey</span>
			{#if provider.id === lastUsed}
				<Badge variant="outline" floating class="absolute -top-2.5 right-3 h-5">Last used</Badge>
			{/if}
		</Button>
	{:else}
		{@render redirectButton(provider, large)}
	{/if}
{/snippet}

{#snippet redirectButton(provider: LoginProvider, large: boolean)}
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
				{#if data.setupOpen}
					<h1 class="display-title">Set up Umpteenth</h1>
					<!-- The longer ledes above a form span the form's width, so they break into two even lines instead of three short ones -->
					<p class="text-muted-foreground text-balance">
						The first account signs in with a passkey and becomes the instance admin.
					</p>
				{:else if signingUp}
					<h1 class="display-title">Create your account</h1>
					<p class="text-muted-foreground text-balance">
						You'll sign in with a passkey, so there's no password to remember.
					</p>
				{:else}
					<h1 class="display-title">Sign in to Umpteenth</h1>
					<p class="text-muted-foreground max-w-lede-narrow text-balance">
						Continue with your team's account.
					</p>
				{/if}
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
			{:else if creatingAccount}
				<form
					novalidate
					class="flex flex-col gap-5 text-left"
					onsubmit={preventDefault(signUpWithPasskey)}
				>
					<Field.Group>
						<FormInput label="Name" bind:input={$inputs.name} autocomplete="name" />
						<FormInput
							label="Email"
							type="email"
							description="Shown to the people you work with."
							bind:input={$inputs.email}
							autocomplete="email"
						/>
					</Field.Group>
					<Button type="submit" size="lg" class="w-full" isLoading={busy}>
						<KeyRoundIcon />
						Create account with a passkey
					</Button>
				</form>
				{#if signingUp}
					<p class="text-muted-foreground text-sm">
						Have an account?
						<button
							type="button"
							class="text-foreground focus-visible:ring-ring rounded-sm underline underline-offset-4 outline-none hover:opacity-80 focus-visible:ring-2"
							onclick={() => (signingUp = false)}
						>
							Sign in
						</button>
					</p>
				{:else if redirectProviders.length > 0}
					<!-- The first account can also come from a sign-in provider, which then decides on the instance admins -->
					<div class="flex flex-col gap-2.5">
						<div class="text-muted-foreground flex items-center gap-3 py-1 text-sm">
							<span class="bg-border h-px flex-1"></span>
							or
							<span class="bg-border h-px flex-1"></span>
						</div>
						{#each redirectProviders as provider (provider.id)}
							{@render redirectButton(provider, false)}
						{/each}
					</div>
				{/if}
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
				{#if passkeys && fromInvite}
					<p class="text-muted-foreground text-sm">
						New to Umpteenth?
						<button
							type="button"
							class="text-foreground focus-visible:ring-ring rounded-sm underline underline-offset-4 outline-none hover:opacity-80 focus-visible:ring-2"
							onclick={() => (signingUp = true)}
						>
							Create an account
						</button>
					</p>
				{/if}
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
