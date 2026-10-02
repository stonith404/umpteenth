<script lang="ts" module>
	// One credential the job needs, with the secret picked for it or an empty ID while none is
	export type SecretRow = { envName: string; why: string; secretId: string };
</script>

<script lang="ts">
	import type { Secret } from '$lib/api/types';
	import * as Select from '$lib/components/ui/select';
	import SecretService from '$lib/services/secret-service';
	import { envNameOf } from '$lib/utils/job-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let {
		rows = $bindable([]),
		secrets = $bindable([])
	}: {
		// The credentials the compiled spec says the job's commands read from environment variables
		rows: SecretRow[];
		// Every secret of the workspace
		secrets: Secret[];
	} = $props();

	// Marks a row whose variable is left out, since the select needs a value for it
	const NONE = '__none__';

	const secretService = new SecretService();

	// A secret created in another tab shows up once you come back, picked for the variable its name suggests
	async function refresh() {
		const result = await tryCatch(secretService.listAll());
		if (!result.data) return;
		secrets = result.data;
		for (const row of rows) {
			if (row.secretId) continue;
			row.secretId = secrets.find((s) => envNameOf(s.name) === row.envName)?.id ?? '';
		}
	}

	function secretName(id: string) {
		return secrets.find((s) => s.id === id)?.name;
	}
</script>

<svelte:window onfocus={refresh} />

<ul class="flex flex-col gap-2" aria-label="Secrets the job needs">
	{#each rows as row (row.envName)}
		{@const name = secretName(row.secretId)}
		<li class="bg-muted/40 flex flex-col gap-3 rounded-lg px-3 py-2.5 sm:flex-row sm:items-start">
			<div class="flex min-w-0 flex-1 items-start gap-3">
				{#if name}
					<CircleCheckIcon class="text-success mt-0.5 size-4 shrink-0" />
				{:else}
					<TriangleAlertIcon class="text-warning-foreground mt-0.5 size-4 shrink-0" />
				{/if}
				<div class="flex min-w-0 flex-col gap-0.5">
					<span class="font-mono text-sm font-medium break-all">{row.envName}</span>
					{#if row.why}
						<span class="text-muted-foreground text-sm">{row.why}</span>
					{/if}
					{#if !name}
						<span class="text-muted-foreground text-xs">
							No secret is picked, so runs won't get this variable.
							<a href="/settings/secrets" target="_blank" class="underline underline-offset-3"
								>Create one</a
							> if it doesn't exist yet.
						</span>
					{/if}
				</div>
			</div>
			<Select.Root
				type="single"
				value={row.secretId || NONE}
				onValueChange={(value) => (row.secretId = value === NONE ? '' : value)}
			>
				<Select.Trigger class="w-full sm:w-56" aria-label="Secret for {row.envName}">
					{#if name}
						<span class="truncate font-mono">{name}</span>
					{:else}
						<span class="text-muted-foreground">Pick a secret</span>
					{/if}
				</Select.Trigger>
				<Select.Content align="end">
					<Select.Item value={NONE} label="None">
						<span class="text-muted-foreground">None</span>
					</Select.Item>
					{#each secrets as secret (secret.id)}
						<Select.Item value={secret.id} label={secret.name}>
							<span class="font-mono">{secret.name}</span>
						</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		</li>
	{/each}
</ul>
