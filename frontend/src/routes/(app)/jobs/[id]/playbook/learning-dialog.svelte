<script lang="ts">
	import type { PlaybookLearning } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import * as Field from '$lib/components/ui/field';
	import { Input } from '$lib/components/ui/input';
	import * as Select from '$lib/components/ui/select';
	import { Textarea } from '$lib/components/ui/textarea';
	import { preventDefault } from '$lib/utils/event-util';
	import { learningKindLabel } from '$lib/utils/playbook-util';
	import { untrack } from 'svelte';

	let {
		learning = $bindable(null),
		onSave
	}: {
		// The learning being edited, the dialog is open while it is set
		learning: PlaybookLearning | null;
		// Resolves to whether the save succeeded, the dialog stays open after a failure
		onSave: (updated: PlaybookLearning) => Promise<boolean>;
	} = $props();

	let text = $state('');
	let when = $state('');
	let kind = $state('');
	let isLoading = $state(false);

	// The kinds reflection uses most, plus the learning's own kind when reflection picked another word for it
	const KINDS = ['fact', 'preference', 'edge_case', 'workaround', 'environment'];
	const kinds = $derived(
		learning && !KINDS.includes(learning.kind) ? [...KINDS, learning.kind] : KINDS
	);

	// Opening another learning copies its fields, while closing keeps them so the dialog doesn't blank out as it fades
	$effect(() => {
		const current = learning;
		if (!current) return;
		untrack(() => {
			text = current.text;
			when = current.when ?? '';
			kind = current.kind;
		});
	});

	async function onSubmit() {
		if (!learning || !text.trim()) return;
		isLoading = true;
		try {
			const saved = await onSave({
				...learning,
				text: text.trim(),
				when: when.trim() || undefined,
				kind: kind.trim() || learning.kind
			});
			if (saved) learning = null;
		} finally {
			isLoading = false;
		}
	}
</script>

<Dialog.Root open={learning !== null} onOpenChange={(open) => !open && (learning = null)}>
	<Dialog.Content class="sm:max-w-lg">
		<Dialog.Header>
			<Dialog.Title>Edit learning {learning?.id}</Dialog.Title>
			<Dialog.Description>Saving creates a new playbook version.</Dialog.Description>
		</Dialog.Header>
		<form novalidate id="learning-form" onsubmit={preventDefault(onSubmit)}>
			<Field.Group>
				<Field.Field>
					<Field.Label for="learning-text">Text</Field.Label>
					<Textarea id="learning-text" bind:value={text} class="min-h-24" />
				</Field.Field>
				<!-- Stacked on phones, the two fields keep the group's spacing between fields -->
				<div class="grid gap-x-4 gap-y-7 sm:grid-cols-2">
					<Field.Field>
						<Field.Label for="learning-when" optional>When</Field.Label>
						<Input id="learning-when" bind:value={when} placeholder="e.g. listing PRs" />
					</Field.Field>
					<Field.Field>
						<Field.Label for="learning-kind">Kind</Field.Label>
						<Select.Root type="single" bind:value={kind}>
							<Select.Trigger id="learning-kind" class="w-full">
								{learningKindLabel(kind)}
							</Select.Trigger>
							<Select.Content>
								{#each kinds as option (option)}
									<Select.Item value={option} label={learningKindLabel(option)} />
								{/each}
							</Select.Content>
						</Select.Root>
					</Field.Field>
				</div>
			</Field.Group>
		</form>
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (learning = null)}>Cancel</Button>
			<Button type="submit" form="learning-form" {isLoading} disabled={!text.trim()}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
