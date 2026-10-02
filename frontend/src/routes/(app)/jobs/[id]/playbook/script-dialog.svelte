<script lang="ts" module>
	// A piece of the playbook that is edited as code: a toolkit script or the main script
	export type CodeTarget = {
		kind: 'script' | 'main';
		name: string;
		title: string;
		lang: string;
		content: string;
		// The playbook version the code was copied from, so saving over a version written meanwhile fails instead of dropping its change
		baseVersion: number;
	};
</script>

<script lang="ts">
	import CodeEditor from '#lib/components/code/code-editor.svelte';
	import { languageForScript } from '#lib/components/code/script-language.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Dialog from '#lib/components/ui/dialog/index.js';

	let {
		target = $bindable(null),
		onSave
	}: {
		// What is being edited, the dialog is open while it is set
		target: CodeTarget | null;
		// Resolves to whether the save succeeded, the dialog stays open after a failure
		onSave: (target: CodeTarget, content: string) => Promise<boolean>;
	} = $props();

	// Starts from the target's code whenever another target opens, and is edited locally from there
	let content = $derived(target?.content ?? '');
	let isLoading = $state(false);

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
				{#if target?.kind === 'main'}
					The main script does the whole job in scripted runs. Saving creates a new playbook
					version.
				{:else}
					The <code class="font-mono text-xs">ump:</code> header sets the tool's description and arguments.
					Saving creates a new playbook version.
				{/if}
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
			<Button onclick={save} {isLoading} disabled={!content.trim()}>Save</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
