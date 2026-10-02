<script lang="ts">
	import type { McpServer, McpTestResult } from '#lib/api/types.js';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import McpService from '#lib/services/mcp-service.js';
	import { Clock } from '#lib/utils/clock.svelte.js';
	import { apiErrorToast } from '#lib/utils/error-util.js';
	import { formatDuration } from '#lib/utils/format-util.js';
	import { authDescription } from '#lib/utils/mcp-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import CircleAlertIcon from '@lucide/svelte/icons/circle-alert';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import LogInIcon from '@lucide/svelte/icons/log-in';
	import { untrack } from 'svelte';
	import ToolList from './tool-list.svelte';

	let {
		server = $bindable(null),
		onTested,
		onLogIn
	}: {
		// The server to test, the dialog is open while it is set
		server: McpServer | null;
		// Called after every finished test, since a successful one refreshes the cached tools
		onTested?: () => void;
		// Starts the server's OAuth login, offered when the test shows the server needs one
		onLogIn?: (server: McpServer) => void;
	} = $props();

	const mcpService = new McpService();
	const clock = new Clock(500);

	let result = $state.raw<McpTestResult | null>(null);
	let testing = $state(false);
	// Whether a test of the open server finished, which turns the button's 'Test' into 'Test again'
	let finished = $state(false);
	let startedAt = $state(0);
	let requestSeq = 0;

	const elapsed = $derived(testing ? Math.max(0, clock.now - startedAt) : 0);
	const needsLogin = $derived(!!result && !result.ok && result.auth.status === 'not_logged_in');

	// Opening the dialog starts a test right away
	$effect(() => {
		if (!server) return;
		untrack(() => {
			finished = false;
			void runTest();
		});
	});

	async function runTest() {
		const current = server;
		if (!current) return;
		const seq = ++requestSeq;
		result = null;
		testing = true;
		startedAt = Date.now();

		const response = await tryCatch(mcpService.test(current.id));
		if (seq !== requestSeq) return;
		testing = false;
		finished = true;

		if (response.error) {
			apiErrorToast(response.error, 'Failed to test the server');
			return;
		}
		result = response.data;
		onTested?.();
	}

	function onOpenChange(open: boolean) {
		if (!open) {
			requestSeq++;
			server = null;
			testing = false;
		}
	}
</script>

<Dialog.Root open={server !== null} {onOpenChange}>
	<Dialog.Content class="sm:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title class="wrap-anywhere">Test {server?.name}</Dialog.Title>
			<Dialog.Description>
				Connects to the server and lists its tools. The tool list is cached for the job settings.
			</Dialog.Description>
		</Dialog.Header>
		<div class="flex min-h-0 min-w-0 flex-col gap-4 overflow-y-auto">
			{#if testing}
				<div class="flex items-center gap-3 rounded-lg border px-4 py-6" aria-live="polite">
					<Spinner class="size-5" />
					<div class="flex flex-col gap-0.5">
						<span class="text-sm font-medium">
							{server?.transport === 'stdio'
								? 'Starting a temporary sandbox and launching the server…'
								: 'Connecting to the server…'}
						</span>
						<span class="text-muted-foreground text-xs">
							{server?.transport === 'stdio'
								? 'This can take up to a minute, e.g. while npx downloads the package.'
								: 'This usually takes a few seconds.'}
							<span class="numeric">{Math.floor(elapsed / 1000)}s</span>
						</span>
					</div>
				</div>
			{:else if result}
				{#if result.ok}
					<Alert.Root variant="success">
						<CircleCheckIcon />
						<Alert.Title>Connected</Alert.Title>
						<Alert.Description>
							Took {formatDuration(result.latencyMs)} and found {result.tools?.length ?? 0}
							{result.tools?.length === 1 ? 'tool' : 'tools'}.
						</Alert.Description>
					</Alert.Root>
					<ToolList tools={result.tools ?? []} />
				{:else if needsLogin}
					<Alert.Root variant="warning">
						<LogInIcon />
						<Alert.Title>Login required</Alert.Title>
						<Alert.Description>{authDescription(result.auth)}</Alert.Description>
					</Alert.Root>
				{:else}
					<Alert.Root variant="destructive">
						<CircleAlertIcon />
						<Alert.Title>Connection failed after {formatDuration(result.latencyMs)}</Alert.Title>
						<Alert.Description>
							<p class="font-mono text-xs break-all whitespace-pre-wrap">{result.error}</p>
						</Alert.Description>
					</Alert.Root>
				{/if}
			{/if}
		</div>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => onOpenChange(false)}>Close</Button>
			{#if needsLogin && server && onLogIn}
				<Button variant="outline" onclick={runTest} isLoading={testing}>Test again</Button>
				<Button onclick={() => server && onLogIn?.(server)}>
					<LogInIcon data-icon="inline-start" />
					Log in
				</Button>
			{:else}
				<Button onclick={runTest} isLoading={testing}>{finished ? 'Test again' : 'Test'}</Button>
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
