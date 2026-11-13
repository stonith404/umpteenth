<script lang="ts">
	import { invalidate } from '$app/navigation';
	import type { Job, JobMount, SandboxFolder } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Field from '#lib/components/ui/field/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import JobService from '#lib/services/job-service.js';
	import { createForm } from '#lib/utils/form-util.js';
	import PlusIcon from 'phosphor-svelte/lib/Plus';
	import XIcon from 'phosphor-svelte/lib/X';
	import { z } from 'zod/v4';

	let {
		job,
		folders = [],
		isAdmin = false
	}: {
		job: Job;
		// The folders the operator configured, the only paths a job may mount
		folders?: SandboxFolder[];
		isAdmin?: boolean;
	} = $props();

	const jobService = new JobService();

	// Mirrors the backend's mount name pattern, since the name becomes a directory
	const NAME = /^[a-z0-9][a-z0-9-]{0,39}$/;

	const schema = z.object({
		mounts: z.array(
			z.object({ name: z.string().trim(), path: z.string(), mode: z.enum(['ro', 'rw']) })
		)
	});
	const pick = ({ name, path, mode }: JobMount) => ({ name, path, mode });

	const form = createForm(schema, { mounts: (job.mounts ?? []).map(pick) });
	const inputs = form.inputs;
	const mounts = $derived($inputs.mounts.value);

	// The configured folder covering a path, the most specific one, which limits the mode and fills the pickers
	function covering(path: string) {
		return folders
			.filter((f) => path === f.path || path.startsWith(f.path + '/'))
			.sort((a, b) => b.path.length - a.path.length)[0];
	}

	const baseOf = (m: JobMount) => covering(m.path)?.path ?? '';
	const subPathOf = (m: JobMount) => {
		const base = baseOf(m);
		return base ? m.path.slice(base.length + 1) : m.path;
	};
	const modesOf = (m: JobMount) => (covering(m.path)?.mode === 'rw' ? ['ro', 'rw'] : ['ro']);

	// Suggests a name from the folder's last segment, e.g. /var/www/site becomes site
	const slugOf = (path: string) =>
		(path.split('/').pop() ?? '')
			.toLowerCase()
			.replace(/[^a-z0-9-]+/g, '-')
			.replace(/^-+|-+$/g, '')
			.slice(0, 40);

	function pickBase(m: JobMount, base: string) {
		const sub = subPathOf(m);
		m.path = sub ? `${base}/${sub}` : base;
		if (!modesOf(m).includes(m.mode)) m.mode = 'ro';
		if (!m.name.trim()) m.name = slugOf(m.path);
	}

	function pickSubPath(m: JobMount, sub: string) {
		const base = baseOf(m);
		const clean = sub.replace(/^\/+|\/+$/g, '');
		m.path = base ? (clean ? `${base}/${clean}` : base) : clean;
		if (!modesOf(m).includes(m.mode)) m.mode = 'ro';
	}

	// Problems show once a save was tried, and then follow the fixes as they are made
	let showErrors = $state(false);

	const errors = $derived.by(() => {
		const names: string[] = [];
		const paths: string[] = [];
		return mounts.map((m) => {
			const name = m.name.trim();
			if (!name) return 'Required';
			if (!NAME.test(name)) return 'Lowercase letters, digits and dashes';
			if (names.includes(name)) return `${name} is used twice`;
			names.push(name);
			if (!m.path) return 'Pick a folder';
			if (!covering(m.path)) return 'Not inside a configured folder';
			if (paths.includes(m.path)) return 'This folder is mounted twice';
			paths.push(m.path);
			return null;
		});
	});

	function add() {
		$inputs.mounts.value = [
			...mounts,
			{ name: '', path: folders[0]?.path ?? '', mode: 'ro' as const }
		];
	}

	function remove(index: number) {
		$inputs.mounts.value = mounts.filter((_, i) => i !== index);
	}

	async function submit() {
		showErrors = errors.some((e) => e !== null);
		if (showErrors) return;
		await form.submit(save);
	}

	async function save(values: z.infer<typeof schema>) {
		await jobService.update(job.id, { mounts: values.mounts.map(pick) });
		await invalidate('app:job');
	}
</script>

<!-- Members see what the job reaches, which an instance admin granted and only an instance admin can change -->
{#if isAdmin}
	<FormCard
		title="Host folders"
		description="Folders on the machine Umpteenth runs on, mounted at /ump/folders/<name> for the whole run. Only instance admins can change them."
		dirty={form.isDirty()}
		saving={form.saving}
		onsubmit={submit}
	>
		<Field.Group>
			{#if folders.length === 0}
				<p class="text-muted-foreground text-sm">
					No base folders are allowed yet, so there is nothing to mount. An operator adds them in
					the config under <code class="font-mono text-xs">sandbox.folders</code>.
				</p>
			{/if}
			{#if mounts.length > 0}
				<ul class="flex flex-col gap-4" aria-label="Host folders">
					{#each mounts as mount, i (i)}
						{@const error = showErrors ? errors[i] : null}
						<li class="flex flex-col gap-1 not-last:border-b not-last:pb-4">
							<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
								<Field.Field>
									<Field.Label for="mount-name-{i}">Name</Field.Label>
									<Input
										id="mount-name-{i}"
										bind:value={mount.name}
										placeholder={slugOf(mount.path) || 'websites'}
										mono
										aria-invalid={!!error}
									/>
								</Field.Field>
								<Field.Field>
									<Field.Label for="mount-base-{i}">Base folder</Field.Label>
									<Select.Root
										type="single"
										value={baseOf(mount)}
										onValueChange={(value) => pickBase(mount, value)}
									>
										<Select.Trigger class="w-full" aria-label="Base folder {i + 1}">
											{#if baseOf(mount)}
												<span class="truncate font-mono">{baseOf(mount)}</span>
											{:else}
												<span class="text-muted-foreground">Pick a base folder</span>
											{/if}
										</Select.Trigger>
										<Select.Content align="start">
											{#each folders as folder (folder.path)}
												<Select.Item value={folder.path} label={folder.path}>
													<span class="font-mono">{folder.path}</span>
												</Select.Item>
											{/each}
										</Select.Content>
									</Select.Root>
									<Field.Description>
										Only folders allowed in the config under
										<code class="font-mono text-xs">sandbox.folders</code> can be picked.
									</Field.Description>
								</Field.Field>
								<Field.Field>
									<Field.Label for="mount-subpath-{i}" optional>Subfolder</Field.Label>
									<Input
										id="mount-subpath-{i}"
										value={subPathOf(mount)}
										oninput={(e) => pickSubPath(mount, e.currentTarget.value)}
										placeholder="site/assets"
										mono
									/>
								</Field.Field>
								<Field.Field>
									<Field.Label for="mount-mode-{i}">Access</Field.Label>
									<Select.Root
										type="single"
										value={mount.mode}
										onValueChange={(value) => (mount.mode = value as 'ro' | 'rw')}
									>
										<Select.Trigger class="w-full" aria-label="Access of folder {i + 1}">
											<span>{mount.mode === 'rw' ? 'Read-write' : 'Read-only'}</span>
										</Select.Trigger>
										<Select.Content align="start">
											<Select.Item value="ro" label="Read-only" />
											{#if modesOf(mount).includes('rw')}
												<Select.Item value="rw" label="Read-write" />
											{/if}
										</Select.Content>
									</Select.Root>
								</Field.Field>
								<Button
									variant="ghost"
									size="icon-sm"
									class="self-center"
									aria-label="Remove folder {i + 1}"
									onclick={() => remove(i)}
								>
									<XIcon />
								</Button>
							</div>
							{#if error}<Field.Error>{error}</Field.Error>{/if}
							{#if mount.mode === 'rw'}
								<Field.Description
									>Changes here are permanent and stay after the run ends.</Field.Description
								>
							{/if}
						</li>
					{/each}
				</ul>
			{:else if folders.length > 0}
				<p class="text-muted-foreground text-sm">This job mounts no host folders</p>
			{/if}
		</Field.Group>

		{#snippet footer()}
			{#if folders.length > 0}
				<Button variant="outline" onclick={add}>
					<PlusIcon data-icon="inline-start" />
					Add folder
				</Button>
			{/if}
		{/snippet}
	</FormCard>
{:else if mounts.length > 0}
	<FormCard
		title="Host folders"
		description="An instance admin lets this job use folders outside its sandbox."
		dirty={false}
		readOnly
		onsubmit={() => {}}
	>
		<ul class="flex flex-col gap-2">
			{#each mounts as mount (mount.name)}
				<li class="flex flex-wrap items-baseline gap-x-2 text-sm">
					<span class="font-mono">/ump/folders/{mount.name}</span>
					<span class="text-muted-foreground font-mono text-xs">{mount.path}</span>
					<span class="text-muted-foreground text-xs">
						{mount.mode === 'rw' ? 'read-write' : 'read-only'}
					</span>
				</li>
			{/each}
		</ul>
	</FormCard>
{/if}
