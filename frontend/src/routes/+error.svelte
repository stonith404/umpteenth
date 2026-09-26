<script lang="ts">
	import { page } from '$app/state';
	import Logo from '$lib/components/logo.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Empty from '$lib/components/ui/empty';
</script>

<svelte:head>
	<title>Error · Umpteenth</title>
</svelte:head>

<main class="flex min-h-svh items-center justify-center p-6">
	<Empty.Root>
		<Empty.Header>
			<Empty.Media>
				<Logo class="size-12" />
			</Empty.Media>
			<Empty.Title>
				{page.status === 404 ? 'Page not found' : 'Something went wrong'}
			</Empty.Title>
			<Empty.Description>
				{page.status === 404
					? "The page you're looking for doesn't exist."
					: (page.error?.message ?? 'An unknown error occurred.')}
			</Empty.Description>
			{#if page.error?.requestId}
				<p class="text-muted-foreground font-mono text-xs">Request ID: {page.error.requestId}</p>
			{/if}
		</Empty.Header>
		<Empty.Content>
			<Button href="/" variant="outline">Back to the dashboard</Button>
		</Empty.Content>
	</Empty.Root>
</main>
