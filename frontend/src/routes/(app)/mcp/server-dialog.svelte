<script lang="ts">
	import { isApiError } from '$lib/api/api-error';
	import type { McpServer, McpServerBody } from '$lib/api/types';
	import KeyValueEditor, {
		entriesToRecord,
		recordToEntries,
		type KeyValueEntry
	} from '$lib/components/form/key-value-editor.svelte';
	import StringListEditor from '$lib/components/form/string-list-editor.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Tabs from '$lib/components/ui/tabs';
	import McpService from '$lib/services/mcp-service';
	import SecretService from '$lib/services/secret-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { cn } from '$lib/utils/style';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import { untrack } from 'svelte';

	let {
		open = $bindable(false),
		server,
		onSaved
	}: {
		open?: boolean;
		// The server to edit, or null to add one
		server: McpServer | null;
		onSaved: (server: McpServer, created: boolean) => void;
	} = $props();

	// Mirrors the backend's validation of server names
	const NAME_PATTERN = /^[A-Za-z0-9_-]+$/;

	const mcpService = new McpService();
	const secretService = new SecretService();

	let name = $state('');
	let description = $state('');
	let transport = $state<'stdio' | 'http'>('stdio');
	let command = $state('');
	let args = $state<string[]>([]);
	let env = $state<KeyValueEntry[]>([]);
	let url = $state('');
	let headers = $state<KeyValueEntry[]>([]);
	let oauthClientId = $state('');
	let oauthClientSecret = $state('');
	let oauthScopes = $state<string[]>([]);
	let oauthOpen = $state(false);
	// The table's switch turns servers on and off, so the dialog only carries the state through a save
	let enabled = $state(true);
	let errors = $state<Record<string, string>>({});
	let secretNames = $state<string[]>([]);
	let isLoading = $state(false);

	// A fresh dialog starts from the server being edited, or from an empty stdio server
	$effect(() => {
		if (!open) return;
		untrack(() => {
			name = server?.name ?? '';
			description = server?.description ?? '';
			transport = server?.transport === 'http' ? 'http' : 'stdio';
			command = server?.command ?? '';
			args = [...(server?.args ?? [])];
			env = recordToEntries(server?.env);
			url = server?.url ?? '';
			headers = recordToEntries(server?.headers);
			oauthClientId = server?.oauth.clientId ?? '';
			oauthClientSecret = server?.oauth.clientSecret ?? '';
			oauthScopes = [...(server?.oauth.scopes ?? [])];
			oauthOpen = !!oauthClientId || oauthScopes.length > 0;
			enabled = server?.enabled ?? true;
			errors = {};
			void loadSecrets();
		});
	});

	// Secret names feed the "Insert secret" menus, a failure only hides them
	async function loadSecrets() {
		const result = await tryCatch(secretService.listAll());
		secretNames = (result.data ?? []).map((s) => s.name);
	}

	function validate() {
		const next: Record<string, string> = {};
		if (!name.trim()) next.name = 'Required';
		else if (!NAME_PATTERN.test(name.trim())) {
			next.name = 'Only letters, digits, dashes and underscores';
		}
		if (transport === 'stdio' && !command.trim()) next.command = 'Required for stdio servers';
		if (transport === 'http') {
			if (!url.trim()) next.url = 'Required for HTTP servers';
			else if (!/^https?:\/\//.test(url.trim())) next.url = 'Must start with http:// or https://';
			if (oauthClientSecret.trim() && !oauthClientId.trim()) {
				next['oauth.clientSecret'] = 'Needs a client ID';
				oauthOpen = true;
			}
		}
		errors = next;
		return Object.keys(next).length === 0;
	}

	// Only the fields of the chosen transport are sent, so switching transports doesn't leave stale values behind
	function body(): McpServerBody {
		const base = {
			name: name.trim(),
			description: description.trim() || undefined,
			enabled
		};
		if (transport === 'stdio') {
			return {
				...base,
				transport,
				command: command.trim(),
				args: args.map((a) => a.trim()).filter(Boolean),
				env: entriesToRecord(env)
			};
		}
		return {
			...base,
			transport,
			url: url.trim(),
			headers: entriesToRecord(headers),
			oauth: {
				clientId: oauthClientId.trim() || undefined,
				clientSecret: oauthClientSecret.trim() || undefined,
				scopes: oauthScopes.map((s) => s.trim()).filter(Boolean)
			}
		};
	}

	async function onSubmit() {
		if (!validate()) return;
		isLoading = true;
		const result = await tryCatch(
			server ? mcpService.update(server.id, body()) : mcpService.create(body())
		);
		isLoading = false;

		if (result.error) {
			if (isApiError(result.error, 'validation_failed', 'invalid_field', 'already_in_use')) {
				const next: Record<string, string> = {};
				for (const f of result.error.fields) {
					const field = f.field.replace(/^body\./, '');
					next[field.startsWith('oauth.') ? field.replace(/\[\d+\]$/, '') : field.split('.')[0]] =
						f.message;
				}
				// An OAuth setting's error sits inside the collapsed section, so it opens to show it
				if (Object.keys(next).some((key) => key.startsWith('oauth.'))) oauthOpen = true;
				if (result.error.code === 'already_in_use') next.name = result.error.message;
				errors = next;
			}
			apiErrorToast(result.error, 'Failed to save the MCP server');
			return;
		}
		open = false;
		onSaved(result.data, !server);
	}
</script>

{#snippet secretHint()}
	Values can reference secrets as <code class="font-mono text-xs">{'{{secret:NAME}}'}</code>, use
	the key button to insert one.
{/snippet}

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-2xl">
		<Dialog.Header>
			<Dialog.Title class="wrap-anywhere"
				>{server ? `Edit ${server.name}` : 'Add MCP server'}</Dialog.Title
			>
			<Dialog.Description>
				Jobs you attach the server to can use its tools. stdio servers run inside each run's
				sandbox, HTTP servers are called from the host.
			</Dialog.Description>
		</Dialog.Header>
		<form
			novalidate
			id="mcp-server-form"
			class="scroll-fade-y -mx-1 min-h-0 overflow-y-auto px-1"
			onsubmit={preventDefault(onSubmit)}
		>
			<Field.Group>
				<div class="grid gap-x-6 gap-y-5 sm:grid-cols-2">
					<Field.Field data-invalid={!!errors.name}>
						<Field.Label for="mcp-name">Name</Field.Label>
						<Input
							id="mcp-name"
							bind:value={name}
							maxlength={64}
							mono
							placeholder="github"
							aria-invalid={!!errors.name}
						/>
						{#if errors.name}
							<Field.Error>{errors.name}</Field.Error>
						{:else}
							<Field.Description
								>Tools show up as <code class="font-mono text-xs">{name || 'name'}__tool</code
								>.</Field.Description
							>
						{/if}
					</Field.Field>
					<Field.Field>
						<Field.Label for="mcp-description" optional>Description</Field.Label>
						<Input
							id="mcp-description"
							bind:value={description}
							maxlength={500}
							placeholder="What the server gives access to"
						/>
					</Field.Field>
				</div>

				<Field.Field>
					<Field.Label>Transport</Field.Label>
					<Tabs.Root
						value={transport}
						onValueChange={(value) => (transport = value as 'stdio' | 'http')}
					>
						<Tabs.List aria-label="Transport">
							<Tabs.Trigger value="stdio">stdio</Tabs.Trigger>
							<Tabs.Trigger value="http">HTTP</Tabs.Trigger>
						</Tabs.List>
					</Tabs.Root>
					<Field.Description>
						{transport === 'stdio'
							? "A command started inside the run's sandbox, e.g. an npx or uvx package."
							: 'A Streamable HTTP endpoint. Credentials stay on the host and never enter the sandbox.'}
					</Field.Description>
				</Field.Field>

				{#if transport === 'stdio'}
					<Field.Field data-invalid={!!errors.command}>
						<Field.Label for="mcp-command">Command</Field.Label>
						<Input
							id="mcp-command"
							bind:value={command}
							mono
							placeholder="npx"
							aria-invalid={!!errors.command}
						/>
						{#if errors.command}<Field.Error>{errors.command}</Field.Error>{/if}
					</Field.Field>
					<Field.Set>
						<Field.Legend variant="label" optional>Arguments</Field.Legend>
						<StringListEditor
							bind:items={args}
							label="Argument"
							placeholder="-y"
							addLabel="Add argument"
						/>
					</Field.Set>
					<Field.Set>
						<Field.Legend variant="label" optional>Environment</Field.Legend>
						<Field.Description>{@render secretHint()}</Field.Description>
						<KeyValueEditor
							bind:entries={env}
							label="Environment variable"
							keyPlaceholder="GITHUB_TOKEN"
							valuePlaceholder={'{{secret:GITHUB_TOKEN}}'}
							addLabel="Add variable"
							secrets={secretNames}
						/>
					</Field.Set>
				{:else}
					<Field.Field data-invalid={!!errors.url}>
						<Field.Label for="mcp-url">URL</Field.Label>
						<Input
							id="mcp-url"
							bind:value={url}
							type="url"
							mono
							placeholder="https://mcp.example.com/mcp"
							aria-invalid={!!errors.url}
						/>
						{#if errors.url}<Field.Error>{errors.url}</Field.Error>{/if}
					</Field.Field>
					<Field.Set>
						<Field.Legend variant="label" optional>Headers</Field.Legend>
						<Field.Description>{@render secretHint()}</Field.Description>
						<KeyValueEditor
							bind:entries={headers}
							label="Header"
							keyPlaceholder="Authorization"
							valuePlaceholder={'Bearer {{secret:API_TOKEN}}'}
							addLabel="Add header"
							secrets={secretNames}
						/>
					</Field.Set>
					<Collapsible.Root bind:open={oauthOpen}>
						<Collapsible.Trigger>
							{#snippet child({ props })}
								<Button {...props} variant="ghost" size="sm" class="-ml-2">
									<ChevronRightIcon
										data-icon="inline-start"
										class={cn('transition-transform', oauthOpen && 'rotate-90')}
									/>
									OAuth client
								</Button>
							{/snippet}
						</Collapsible.Trigger>
						<Collapsible.Content>
							<Field.Group class="mt-3">
								<Field.Description>
									OAuth logins are detected from the server, and a client is registered
									automatically. Set one here only when the authorization server doesn't allow that,
									or to request other scopes.
								</Field.Description>
								<div class="grid gap-x-6 gap-y-5 sm:grid-cols-2">
									<Field.Field>
										<Field.Label for="mcp-oauth-client-id" optional>Client ID</Field.Label>
										<Input
											id="mcp-oauth-client-id"
											bind:value={oauthClientId}
											maxlength={500}
											mono
											autocomplete="off"
										/>
									</Field.Field>
									<Field.Field data-invalid={!!errors['oauth.clientSecret']}>
										<Field.Label for="mcp-oauth-client-secret" optional>Client secret</Field.Label>
										<Input
											id="mcp-oauth-client-secret"
											bind:value={oauthClientSecret}
											maxlength={2000}
											mono
											autocomplete="off"
											placeholder={'{{secret:CLIENT_SECRET}}'}
											aria-invalid={!!errors['oauth.clientSecret']}
										/>
										{#if errors['oauth.clientSecret']}
											<Field.Error>{errors['oauth.clientSecret']}</Field.Error>
										{/if}
									</Field.Field>
								</div>
								{#if server?.auth.callbackUrl}
									<Field.Field>
										<Field.Label>Callback URL</Field.Label>
										<code class="bg-muted/50 rounded-lg px-3 py-2 font-mono text-xs break-all"
											>{server.auth.callbackUrl}</code
										>
										<Field.Description
											>Allow this redirect URL in the OAuth client's settings.</Field.Description
										>
									</Field.Field>
								{:else}
									<Field.Description>
										The callback URL to allow in the OAuth client shows up here once the server is
										added.
									</Field.Description>
								{/if}
								<Field.Set data-invalid={!!errors['oauth.scopes']}>
									<Field.Legend variant="label" optional>Scopes</Field.Legend>
									<Field.Description
										>Leave empty to request what the server asks for.</Field.Description
									>
									<StringListEditor
										bind:items={oauthScopes}
										label="Scope"
										placeholder="read"
										addLabel="Add scope"
									/>
									{#if errors['oauth.scopes']}
										<Field.Error>{errors['oauth.scopes']}</Field.Error>
									{/if}
								</Field.Set>
							</Field.Group>
						</Collapsible.Content>
					</Collapsible.Root>
				{/if}
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="mcp-server-form" {isLoading}>
				{server ? 'Save' : 'Add MCP server'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
