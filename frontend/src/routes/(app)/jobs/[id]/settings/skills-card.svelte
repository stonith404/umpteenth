<script lang="ts">
	import type { JobSkill, Skill } from '#lib/api/types.js';
	import FormCard from '#lib/components/form/form-card.svelte';
	import AttachedSkillList from '#lib/components/skills/attached-skill-list.svelte';
	import SkillAttachMenu from '#lib/components/skills/skill-attach-menu.svelte';
	import JobService from '#lib/services/job-service.js';
	import { createForm } from '#lib/utils/form-util.js';
	import { mergeListChanges } from '#lib/utils/job-util.js';
	import { z } from 'zod/v4';
	import LoadError from './load-error.svelte';

	let {
		jobId,
		attached: initial,
		skills,
		skillsError = null
	}: {
		jobId: string;
		attached: JobSkill[];
		// Every skill of the workspace, or null when they could not be listed
		skills: Skill[] | null;
		skillsError?: unknown;
	} = $props();

	const jobService = new JobService();

	const pick = ({ skillId }: JobSkill) => ({ skillId });

	const form = createForm(z.object({ attached: z.array(z.object({ skillId: z.string() })) }), {
		attached: initial.map(pick)
	});
	const inputs = form.inputs;
	const attached = $derived($inputs.attached.value);

	const skillById = $derived(new Map((skills ?? []).map((s) => [s.id, s])));
	const available = $derived(
		(skills ?? []).filter((s) => !attached.some((a) => a.skillId === s.id))
	);

	// The attachments carry their skill's name, which keeps them readable when the workspace's skills failed to load
	const attachedNames = new Map(initial.map((s) => [s.skillId, s.skillName]));

	const attachedItems = $derived(
		attached.map((item) => {
			const skill = skillById.get(item.skillId);
			return {
				id: item.skillId,
				name: skill?.name ?? attachedNames.get(item.skillId) ?? 'Unknown skill',
				description: skill?.description
			};
		})
	);

	function add(skillId: string) {
		$inputs.attached.value = [...attached, { skillId }];
	}

	function remove(skillId: string) {
		$inputs.attached.value = attached.filter((a) => a.skillId !== skillId);
	}

	// The attachments as the card last loaded or saved them, which tells the card's own changes apart from ones saved elsewhere meanwhile
	let saved = initial.map(pick);

	// Saving replaces every attachment, so the card's changes are applied to the stored attachments rather than to the ones it loaded
	async function save(values: { attached: JobSkill[] }) {
		const stored = (await jobService.getSkills(jobId)).map(pick);
		await jobService.setSkills(
			jobId,
			mergeListChanges(saved, values.attached, stored, (a) => a.skillId)
		);
		saved = values.attached;
	}
</script>

<FormCard
	title="Skills"
	description="Every run gets the attached skills in /ump/skills, and the agent reads one when the task calls for it."
	dirty={form.isDirty()}
	saving={form.saving}
	onsubmit={() => form.submit(save)}
>
	<div class="flex flex-col gap-4">
		{#if skillsError}
			<LoadError title="Couldn't load your skills" error={skillsError} />
		{/if}

		{#if attached.length > 0}
			<AttachedSkillList items={attachedItems} onRemove={remove} />
		{:else if skills === null || skills.length > 0}
			<p class="text-muted-foreground text-sm">No skills attached</p>
		{:else}
			<p class="text-muted-foreground text-sm">
				No skills are uploaded yet. <a href="/skills" class="underline underline-offset-3"
					>Upload one</a
				> first.
			</p>
		{/if}
	</div>

	{#snippet footer()}
		{#if available.length > 0}
			<SkillAttachMenu skills={available} onAttach={(skill) => add(skill.id)} />
		{/if}
	{/snippet}
</FormCard>
