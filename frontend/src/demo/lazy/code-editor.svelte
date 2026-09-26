<!--
@component
The app's code editor, loaded once it first shows, since the demo only needs it when a reader opens a script's content on the Learned tab.
The demo build points `$lib/components/code/code-editor.svelte` here, see vite.demo.config.ts.
-->
<script lang="ts">
	import type { ComponentProps } from 'svelte';
	import type CodeEditorType from '../../lib/components/code/code-editor.svelte';

	let { value = $bindable(''), ...rest }: ComponentProps<typeof CodeEditorType> = $props();

	const editor = import('../../lib/components/code/code-editor.svelte');
</script>

{#await editor then { default: CodeEditor }}
	<CodeEditor bind:value {...rest} />
{/await}
