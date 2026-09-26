<script lang="ts">
	import type { JobSecret, Secret } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import * as Card from '$lib/components/ui/card';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import JobService from '$lib/services/job-service';
	import { deepCopy, deepEqual } from '$lib/utils/form-util';
	import { trackUnsavedSection } from '$lib/utils/unsaved-changes-util.svelte';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import XIcon from '@lucide/svelte/icons/x';

	let {
		jobId,
		mappings: initial,
		secrets
	}: {
		jobId: string;
		mappings: JobSecret[];
		// Every secret of the workspace, without values
		secrets: Secret[];
	} = $props();

	type Mapping = { secretId: string; envName: string };

	// Mirrors the backend's pattern for environment variable names
	const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

	const jobService = new JobService();

	const toMappings = (list: JobSecret[]): Mapping[] =>
		list.map(({ secretId, envName }) => ({ secretId, envName }));

	let mappings = $state(toMappings(initial));
	let saved = $state.raw(toMappings(initial));
	let showErrors = $state(false);

	const secretById = $derived(new Map(secrets.map((s) => [s.id, s])));

	// Rows still being filled in are ignored, so an empty row doesn't block saving
	const complete = $derived(mappings.filter((m) => m.secretId || m.envName.trim()));

	const errors = $derived.by(() => {
		const seen: string[] = [];
		return mappings.map((m) => {
			const name = m.envName.trim();
			if (!m.secretId && !name) return null;
			if (!m.secretId) return 'Pick a secret';
			if (!name) return 'Required';
			if (!ENV_NAME.test(name)) return 'Letters, digits and underscores, not starting with a digit';
			if (seen.includes(name)) return `${name} is mapped twice`;
			seen.push(name);
			return null;
		});
	});

	trackUnsavedSection(
		() => !deepEqual(normalize(complete), saved),
		async () => {
			const result = await jobService.setSecrets(jobId, normalize(complete));
			mappings = toMappings(result ?? []);
			saved = deepCopy(mappings);
			showErrors = false;
		},
		() => {
			mappings = deepCopy(saved);
			showErrors = false;
		},
		{
			validate: () => {
				showErrors = true;
				return errors.every((e) => e === null);
			}
		}
	);

	function normalize(list: Mapping[]): Mapping[] {
		return list.map((m) => ({ secretId: m.secretId, envName: m.envName.trim() }));
	}

	function add() {
		mappings = [...mappings, { secretId: '', envName: '' }];
	}

	function remove(index: number) {
		mappings = mappings.filter((_, i) => i !== index);
	}

	// Suggests an environment variable name from the secret's name, e.g. github-token becomes GITHUB_TOKEN
	function pickSecret(index: number, secretId: string) {
		mappings[index].secretId = secretId;
		if (!mappings[index].envName.trim()) {
			const name = secretById.get(secretId)?.name ?? '';
			mappings[index].envName = name
				.toUpperCase()
				.replace(/[^A-Z0-9_]/g, '_')
				.replace(/^(\d)/, '_$1');
		}
	}
</script>

<Card.Root>
	<Card.Header>
		<Card.Title>Secrets</Card.Title>
		<Card.Description>
			Secrets reach the sandbox as environment variables. Values are never shown, manage them in
			<a href="/settings/secrets" class="underline underline-offset-3">Settings</a>.
		</Card.Description>
	</Card.Header>
	<Card.Content class="flex flex-col gap-3">
		{#if mappings.length > 0}
			<ul class="flex flex-col gap-3" aria-label="Secret mappings">
				{#each mappings as mapping, i (i)}
					{@const error = showErrors ? errors[i] : null}
					<li class="flex flex-col gap-1">
						<div class="grid grid-cols-[1fr_auto] gap-2 sm:grid-cols-[1fr_1fr_auto]">
							<Select.Root
								type="single"
								value={mapping.secretId}
								onValueChange={(value) => pickSecret(i, value)}
							>
								<Select.Trigger class="w-full" aria-label="Secret {i + 1}">
									{#if secretById.get(mapping.secretId)}
										<span class="truncate font-mono">{secretById.get(mapping.secretId)?.name}</span>
									{:else}
										<span class="text-muted-foreground">Pick a secret</span>
									{/if}
								</Select.Trigger>
								<Select.Content>
									{#each secrets as secret (secret.id)}
										<Select.Item value={secret.id} label={secret.name}>
											<span class="font-mono">{secret.name}</span>
										</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
							<Input
								bind:value={mapping.envName}
								placeholder="ENV_NAME"
								class="order-last col-span-2 font-mono sm:order-none sm:col-span-1"
								aria-label="Environment variable {i + 1}"
								aria-invalid={!!error}
							/>
							<Button
								variant="ghost"
								size="icon-sm"
								class="self-center"
								aria-label="Remove secret mapping {i + 1}"
								onclick={() => remove(i)}
							>
								<XIcon />
							</Button>
						</div>
						{#if error}<Field.Error>{error}</Field.Error>{/if}
					</li>
				{/each}
			</ul>
		{:else}
			<p class="text-muted-foreground text-sm">No secrets are passed to this job.</p>
		{/if}
		{#if secrets.length > 0}
			<Button variant="outline" size="sm" class="w-fit" onclick={add}>
				<PlusIcon data-icon="inline-start" />
				Add secret
			</Button>
		{:else}
			<p class="text-muted-foreground text-sm">
				No secrets yet. <a href="/settings/secrets" class="underline underline-offset-3"
					>Create one</a
				> first.
			</p>
		{/if}
	</Card.Content>
</Card.Root>
