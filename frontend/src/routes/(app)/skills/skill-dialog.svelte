<script lang="ts">
	import { ApiError, isApiError } from '$lib/api/api-error';
	import type { Skill, SkillChoice } from '$lib/api/types';
	import FileDrop from '$lib/components/form/file-drop.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Tabs from '$lib/components/ui/tabs';
	import SkillService, { MAX_SKILL_UPLOAD_BYTES } from '$lib/services/skill-service';
	import { apiErrorToast } from '$lib/utils/error-util';
	import { preventDefault } from '$lib/utils/event-util';
	import { tryCatch } from '$lib/utils/try-catch-util';
	import { untrack } from 'svelte';

	let {
		open = $bindable(false),
		skill,
		onSaved
	}: {
		open?: boolean;
		// The skill to replace with a new version, or null to add one
		skill: Skill | null;
		// The skills added or the one replaced, since a pick from a folder adds several at once
		onSaved: (skills: Skill[], created: boolean) => void;
	} = $props();

	const skillService = new SkillService();

	let source = $state<'upload' | 'link'>('upload');
	let file = $state<File | null>(null);
	let url = $state('');
	let error = $state('');
	let isLoading = $state(false);
	// The skills of a folder of several, listed once the link turned out to hold more than one, and the paths picked from them
	let choices = $state<SkillChoice[] | null>(null);
	let picked = $state<string[]>([]);

	const available = $derived((choices ?? []).filter((c) => !c.exists && !c.problem));

	// A fresh dialog starts without a file, and a skill imported from a link offers that link again
	$effect(() => {
		if (!open) return;
		untrack(() => {
			file = null;
			url = skill?.sourceUrl ?? '';
			source = skill?.sourceUrl ? 'link' : 'upload';
			error = '';
			choices = null;
			picked = [];
		});
	});

	// Another link may hold other skills, so the list it showed no longer applies
	function onLinkInput() {
		error = '';
		choices = null;
		picked = [];
	}

	function toggle(path: string, checked: boolean) {
		picked = checked ? [...picked, path] : picked.filter((p) => p !== path);
	}

	function toggleAll() {
		picked = picked.length === available.length ? [] : available.map((c) => c.path);
	}

	function pickSource(value: string) {
		source = value === 'link' ? 'link' : 'upload';
		error = '';
	}

	// Checks what the browser can tell before sending anything, and returns the request to send
	function request() {
		if (source === 'link') {
			const link = url.trim();
			if (!/^https?:\/\/\S+$/.test(link)) {
				error = 'Enter a link that starts with http:// or https://';
				return null;
			}
			if (skill) return skillService.reimport(skill.id, link).then((s) => [s]);
			if (choices) {
				if (picked.length === 0) {
					error = 'Pick at least one skill';
					return null;
				}
				return skillService.importFromLink(link, picked);
			}
			return skillService.importFromLink(link);
		}
		if (!file) {
			error = 'Choose a zip of the skill folder';
			return null;
		}
		if (file.size > MAX_SKILL_UPLOAD_BYTES) {
			error = 'The zip must be at most 8 MiB';
			return null;
		}
		return (skill ? skillService.replace(skill.id, file) : skillService.create(file)).then((s) => [
			s
		]);
	}

	// The backend words problems as the rest of a sentence about the zip or the link they came from
	function sentence(field: string, message: string) {
		if (field === 'paths') return `The selection ${message}`;
		if (source === 'upload') return `The zip ${message}`;
		return message.startsWith('host ') ? `The link's ${message}` : `The link ${message}`;
	}

	async function onSubmit() {
		error = '';
		const pending = request();
		if (!pending) return;
		isLoading = true;
		const result = await tryCatch(pending);
		isLoading = false;
		if (result.error) {
			// What is wrong with the zip or the link is shown next to it, since the user fixes it there
			const field = isApiError(result.error, 'validation_failed')
				? result.error.fields.find((f) => ['file', 'url', 'paths'].includes(f.field))
				: undefined;

			// A folder of several skills lists them to pick from, rather than refusing the link
			if (field?.code === 'several_skills' && !skill) {
				await listChoices(url.trim());
				return;
			}
			if (field) {
				error = sentence(field.field, field.message);
				return;
			}
			if (isApiError(result.error, 'already_in_use')) {
				error = 'A skill with this name already exists. Replace that one instead.';
				return;
			}
			if (result.error instanceof ApiError && result.error.status === 413) {
				error = 'The zip must be at most 8 MiB';
				return;
			}
			apiErrorToast(
				result.error,
				skill ? 'Failed to replace the skill' : 'Failed to add the skill'
			);
			return;
		}
		open = false;
		onSaved(result.data, !skill);
	}

	async function listChoices(link: string) {
		isLoading = true;
		const result = await tryCatch(skillService.previewLink(link));
		isLoading = false;
		if (result.error) {
			apiErrorToast(result.error, 'Failed to list the skills of the folder');
			return;
		}
		choices = result.data;
		picked = [];
	}
</script>

<!-- A pending upload keeps the dialog open, so its completion can't close or report on whatever the dialog shows next -->
<Dialog.Root bind:open={() => open, (next) => (next || !isLoading) && (open = next)}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title class="wrap-anywhere">
				{skill ? `Replace ${skill.name}` : 'Add skill'}
			</Dialog.Title>
			<Dialog.Description>
				{#if skill}
					Add a new version of the skill. Its SKILL.md must keep the name {skill.name}.
				{:else}
					A skill folder with a SKILL.md whose frontmatter sets the name and description, plus any
					scripts and reference files it uses.
				{/if}
			</Dialog.Description>
		</Dialog.Header>
		<form
			novalidate
			id="skill-form"
			class="flex flex-col gap-5"
			onsubmit={preventDefault(onSubmit)}
		>
			<Tabs.Root value={source} onValueChange={pickSource}>
				<Tabs.List aria-label="Source">
					<Tabs.Trigger value="upload">Upload</Tabs.Trigger>
					<Tabs.Trigger value="link">Link</Tabs.Trigger>
				</Tabs.List>
			</Tabs.Root>
			{#if source === 'upload'}
				<Field.Field data-invalid={!!error}>
					<Field.Label for="skill-file">Zip file</Field.Label>
					<FileDrop
						id="skill-file"
						accept=".zip,.skill,application/zip"
						hint="Drop a .zip or .skill file here"
						bind:file
						invalid={!!error}
						onchange={() => (error = '')}
					/>
					{#if error}
						<Field.Error>{error}</Field.Error>
					{:else}
						<Field.Description>A zip of the skill folder, of at most 8 MiB.</Field.Description>
					{/if}
				</Field.Field>
			{:else}
				<Field.Field data-invalid={!!error}>
					<Field.Label for="skill-url">Link</Field.Label>
					<Input
						id="skill-url"
						type="url"
						bind:value={url}
						maxlength={2000}
						mono="xs"
						placeholder="https://github.com/anthropics/skills/tree/main/skills/pdf"
						aria-invalid={!!error && !choices}
						oninput={onLinkInput}
					/>
					{#if error && !choices}
						<Field.Error>{error}</Field.Error>
					{:else}
						<Field.Description>
							A skill's folder in a public GitHub repository, or a link to a .zip or .skill file.
						</Field.Description>
					{/if}
				</Field.Field>
				{#if choices}
					<Field.Set data-invalid={!!error}>
						<div class="flex items-baseline justify-between gap-2">
							<Field.Legend variant="label">Skills in this folder</Field.Legend>
							{#if available.length > 1}
								<Button variant="link" size="xs" onclick={toggleAll}>
									{picked.length === available.length ? 'Select none' : 'Select all'}
								</Button>
							{/if}
						</div>
						<Field.Description>
							The folder holds {choices.length} skills. Pick the ones to add.
						</Field.Description>
						<div class="scroll-fade-y -mx-1 max-h-72 overflow-y-auto px-1">
							<Field.Group variant="choices">
								{#each choices as choice (choice.path)}
									{@const disabled = choice.exists || !!choice.problem}
									<Field.Field orientation="horizontal" data-disabled={disabled || undefined}>
										<Checkbox
											id="pick-{choice.path}"
											checked={picked.includes(choice.path)}
											{disabled}
											onCheckedChange={(checked) => toggle(choice.path, checked)}
										/>
										<Field.Content>
											<Field.Label for="pick-{choice.path}" variant="choice">
												{choice.name}
												{#if choice.exists}<Badge variant="secondary">Added</Badge>{/if}
											</Field.Label>
											<Field.Description>
												<!-- Descriptions run long, and the full one is a hover away, so the list stays scannable -->
												<span class="line-clamp-2" title={choice.problem ?? choice.description}>
													{choice.problem ?? choice.description}
												</span>
											</Field.Description>
										</Field.Content>
									</Field.Field>
								{/each}
							</Field.Group>
						</div>
						{#if error}<Field.Error>{error}</Field.Error>{/if}
					</Field.Set>
				{/if}
			{/if}
		</form>
		<Dialog.Footer>
			<Button variant="outline" disabled={isLoading} onclick={() => (open = false)}>Cancel</Button>
			<Button type="submit" form="skill-form" {isLoading}>
				{#if skill}
					Replace
				{:else if choices && source === 'link'}
					{picked.length > 1 ? `Add ${picked.length} skills` : 'Add skill'}
				{:else}
					Add skill
				{/if}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
