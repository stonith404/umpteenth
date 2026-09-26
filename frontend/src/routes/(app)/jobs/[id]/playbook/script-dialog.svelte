<script lang="ts" module>
	// A piece of the playbook that is edited as code: a toolkit script or the main script
	export type CodeTarget = {
		kind: 'script' | 'main';
		name: string;
		title: string;
		lang: string;
		content: string;
	};
</script>

<script lang="ts">
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { languageForScript } from '$lib/components/code/code-languages';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';
	import { untrack } from 'svelte';

	let {
		target = $bindable(null),
		onSave
	}: {
		// What is being edited, the dialog is open while it is set
		target: CodeTarget | null;
		// Resolves to whether the save succeeded, the dialog stays open after a failure
		onSave: (target: CodeTarget, content: string) => Promise<boolean>;
	} = $props();

	let content = $state('');
	let isLoading = $state(false);

	$effect(() => {
		const current = target;
		if (!current) return;
		untrack(() => (content = current.content));
	});

	async function save() {
		if (!target) return;
		isLoading = true;
		try {
			if (await onSave(target, content)) target = null;
		} finally {
			isLoading = false;
		}
	}
</script>

<Dialog.Root open={target !== null} onOpenChange={(open) => !open && (target = null)}>
	<Dialog.Content class="sm:max-w-3xl">
		<Dialog.Header>
			<Dialog.Title>{target?.title}</Dialog.Title>
			<Dialog.Description>
				{target?.kind === 'main'
					? 'The main script does the whole job in scripted runs. Saving creates a new playbook version.'
					: "The ump: header sets the tool's description and arguments. Saving creates a new playbook version."}
			</Dialog.Description>
		</Dialog.Header>
		{#if target}
			<CodeEditor
				bind:value={content}
				language={languageForScript(target.lang)}
				label={target.title}
				class="h-96"
			/>
		{/if}
		<Dialog.Footer>
			<Button variant="outline" onclick={() => (target = null)}>Cancel</Button>
			<Button onclick={save} disabled={isLoading || !content.trim()}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
