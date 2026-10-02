<script lang="ts">
	import type { JobSecret, Secret } from '$lib/api/types';
	import FormCard from '$lib/components/form/form-card.svelte';
	import { Button } from '$lib/components/ui/button';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import JobService from '$lib/services/job-service';
	import { createForm } from '$lib/utils/form-util';
	import { envNameOf, mergeListChanges } from '$lib/utils/job-util';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import XIcon from '@lucide/svelte/icons/x';
	import { z } from 'zod/v4';
	import LoadError from './load-error.svelte';

	let {
		jobId,
		mappings: initial,
		secrets,
		secretsError = null
	}: {
		jobId: string;
		mappings: JobSecret[];
		// Every secret of the workspace, without values, or null when they could not be listed
		secrets: Secret[] | null;
		secretsError?: unknown;
	} = $props();

	// Mirrors the backend's pattern for environment variable names
	const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

	const jobService = new JobService();

	const pick = ({ secretId, envName }: JobSecret) => ({ secretId, envName });

	const form = createForm(
		z.object({ mappings: z.array(z.object({ secretId: z.string(), envName: z.string().trim() })) }),
		{ mappings: initial.map(pick) }
	);
	const inputs = form.inputs;
	const mappings = $derived($inputs.mappings.value);

	// Problems show once a save was tried, and then follow the fixes as they are made
	let showErrors = $state(false);

	const secretById = $derived(new Map((secrets ?? []).map((s) => [s.id, s])));

	// The mappings carry their secret's name, which keeps them readable when the workspace's secrets failed to load
	const mappedNames = new Map(initial.map((m) => [m.secretId, m.secretName]));

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

	// Rows left empty are dropped rather than saved, so an unused 'Add secret' doesn't block saving
	async function submit() {
		$inputs.mappings.value = mappings.filter((m) => m.secretId || m.envName.trim());
		showErrors = errors.some((e) => e !== null);
		if (showErrors) return;
		await form.submit(save);
	}

	// The mappings as the card last loaded or saved them, which tells the card's own changes apart from ones saved elsewhere meanwhile
	let saved = initial.map(pick);

	// Saving replaces every mapping, so the card's changes are applied to the stored mappings rather than to the ones it loaded
	async function save(values: { mappings: { secretId: string; envName: string }[] }) {
		const stored = (await jobService.getSecrets(jobId)).map(pick);
		await jobService.setSecrets(
			jobId,
			mergeListChanges(saved, values.mappings, stored, (m) => m.envName)
		);
		saved = values.mappings;
	}

	function add() {
		$inputs.mappings.value = [...mappings, { secretId: '', envName: '' }];
	}

	function remove(index: number) {
		$inputs.mappings.value = mappings.filter((_, i) => i !== index);
	}

	// Suggests an environment variable name from the secret's name, e.g. github-token becomes GITHUB_TOKEN
	function pickSecret(index: number, secretId: string) {
		mappings[index].secretId = secretId;
		if (!mappings[index].envName.trim()) {
			mappings[index].envName = envNameOf(secretById.get(secretId)?.name ?? '');
		}
	}
</script>

<FormCard title="Secrets" dirty={form.isDirty()} saving={form.saving} onsubmit={submit}>
	{#snippet description()}
		Secrets reach the sandbox as environment variables. Their values are never shown, and you manage
		them on the <a href="/settings/secrets" class="underline underline-offset-3">Secrets</a> page of the
		workspace settings.
	{/snippet}

	<div class="flex flex-col gap-3">
		{#if secretsError}
			<LoadError title="Couldn't load your secrets" error={secretsError} />
		{/if}
		{#if mappings.length > 0}
			<!-- Rows follow the card's width rather than the window's, and wide ones get column headers since filled inputs lose their placeholders -->
			<div class="flex flex-col gap-2 @container">
				<div
					aria-hidden="true"
					class="text-muted-foreground hidden grid-cols-secret-mapping-header gap-2 text-xs font-medium @md:grid"
				>
					<span>Secret</span>
					<span>Environment variable</span>
				</div>
				<ul class="flex flex-col gap-3 @md:gap-2" aria-label="Secret mappings">
					{#each mappings as mapping, i (i)}
						{@const error = showErrors ? errors[i] : null}
						{@const name =
							secretById.get(mapping.secretId)?.name ?? mappedNames.get(mapping.secretId)}
						<li
							class="flex flex-col gap-1 not-last:border-b not-last:pb-3 @md:not-last:border-b-0 @md:not-last:pb-0"
						>
							<div class="grid grid-cols-trailing-action gap-2 @md:grid-cols-secret-mapping">
								<Select.Root
									type="single"
									value={mapping.secretId}
									disabled={secrets === null}
									onValueChange={(value) => pickSecret(i, value)}
								>
									<Select.Trigger class="w-full" aria-label="Secret {i + 1}">
										{#if name}
											<span class="truncate font-mono">{name}</span>
										{:else}
											<span class="text-muted-foreground">Pick a secret</span>
										{/if}
									</Select.Trigger>
									<Select.Content align="start">
										{#each secrets ?? [] as secret (secret.id)}
											<Select.Item value={secret.id} label={secret.name}>
												<span class="font-mono">{secret.name}</span>
											</Select.Item>
										{/each}
									</Select.Content>
								</Select.Root>
								<Input
									bind:value={mapping.envName}
									placeholder="ENV_NAME"
									mono
									class="order-last col-span-2 @md:order-none @md:col-span-1"
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
			</div>
		{:else if secrets === null || secrets.length > 0}
			<p class="text-muted-foreground text-sm">No secrets are passed to this job</p>
		{:else}
			<p class="text-muted-foreground text-sm">
				No secrets yet. <a href="/settings/secrets" class="underline underline-offset-3"
					>Create one</a
				> first.
			</p>
		{/if}
	</div>

	{#snippet footer()}
		{#if secrets && secrets.length > 0}
			<Button variant="outline" onclick={add}>
				<PlusIcon data-icon="inline-start" />
				Add secret
			</Button>
		{/if}
	{/snippet}
</FormCard>
