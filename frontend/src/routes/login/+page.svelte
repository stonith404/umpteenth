<script lang="ts">
	import { page } from '$app/state';
	import ModeSwitcher from '$lib/components/layout/mode-switcher.svelte';
	import Logo from '$lib/components/logo.svelte';
	import Wordmark from '$lib/components/wordmark.svelte';
	import * as Alert from '$lib/components/ui/alert';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import { getLoginErrorMessage } from '$lib/utils/error-util';
	import { oidcLoginUrl } from '$lib/utils/redirection-util';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import LogInIcon from '@lucide/svelte/icons/log-in';

	const error = $derived(page.url.searchParams.get('error'));
	const loginHref = $derived(oidcLoginUrl(page.url.searchParams.get('redirect')));
</script>

<svelte:head>
	<title>Sign in · Umpteenth</title>
</svelte:head>

<div class="absolute top-4 right-4">
	<ModeSwitcher />
</div>

<main class="bg-muted/40 flex min-h-svh items-center justify-center p-6">
	<Card.Root class="w-full max-w-sm">
		<Card.Header class="justify-items-center text-center">
			<Logo class="mx-auto mb-2 size-12" />
			<Card.Title class="justify-center"><Wordmark class="h-5" /></Card.Title>
			<Card.Description
				>Sign in with your organization's identity provider to continue.</Card.Description
			>
		</Card.Header>
		<Card.Content class="flex flex-col gap-4">
			{#if error}
				<Alert.Root variant="destructive">
					<CircleAlertIcon />
					<Alert.Title>Sign-in failed</Alert.Title>
					<Alert.Description>{getLoginErrorMessage(error)}</Alert.Description>
				</Alert.Root>
			{/if}
			<!-- The login endpoint belongs to the backend, so the SvelteKit router must not handle this link -->
			<Button href={loginHref} size="lg" class="w-full" data-sveltekit-reload>
				<LogInIcon data-icon="inline-start" />
				Sign in with OIDC
			</Button>
		</Card.Content>
	</Card.Root>
</main>
