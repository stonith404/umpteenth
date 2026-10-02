<script lang="ts">
	import CodeEditor from '#lib/components/code/code-editor.svelte';
	import DiffView from '#lib/components/diff-view.svelte';
	import KindIcon from '#lib/components/playbook/kind-icon.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import * as Collapsible from '#lib/components/ui/collapsible/index.js';
	import { diffLines, diffStats } from '#lib/utils/diff-util.js';
	import { opLanguage, opStatusLabel, type PlaybookChange } from '#lib/utils/playbook-util.js';
	import { cn } from '#lib/utils/style.js';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import CircleDashedIcon from '@lucide/svelte/icons/circle-dashed';
	import InfoIcon from '@lucide/svelte/icons/info';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let {
		change,
		first = false
	}: {
		change: PlaybookChange;
		// The first change in the list, whose diff starts open however long it is
		first?: boolean;
	} = $props();

	const op = $derived(change.op);
	const section = $derived(change.section);
	const stats = $derived(section ? diffStats(diffLines(section.before, section.after)) : null);
	const notApplied = $derived(op !== undefined && op.status !== 'applied');

	// Small diffs start open, and so does the first, so a long version still opens with something to read
	// Only the initial value matters, the reader toggles it from there
	// svelte-ignore state_referenced_locally
	let open = $state(first || (stats !== null && stats.added + stats.removed <= 6));
</script>

<article class="bg-layer ring-hairline flex flex-col gap-2 rounded-lg p-4 ring-1">
	<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
		<KindIcon kind={change.kind} />
		<h4 class={cn('text-sm font-medium break-all', notApplied && 'text-muted-foreground')}>
			{change.label}
		</h4>
		{#if stats}
			<span class="numeric text-xs whitespace-nowrap">
				<span class="text-success-foreground">+{stats.added}</span>
				<span class="text-destructive">−{stats.removed}</span>
			</span>
		{/if}
		{#if op}
			<span class="ml-auto">
				<Badge
					variant={op.status === 'applied'
						? 'success'
						: op.status === 'held'
							? 'warning'
							: 'secondary'}
				>
					{opStatusLabel(op.status)}
				</Badge>
			</span>
		{/if}
	</div>

	<div class="flex min-w-0 flex-col gap-2 sm:pl-9">
		{#if op}
			<p class="text-muted-foreground text-sm">{op.rationale}</p>

			<!-- A proposal that changed nothing has no diff, so its learning text is the only place to read it -->
			{#if op.text && !section}
				<p class="border-l-2 pl-3 text-sm">{op.text}</p>
			{/if}

			{#if op.status === 'rejected' && op.reason}
				<p class="text-muted-foreground flex items-start gap-1.5 text-xs">
					<InfoIcon class="mt-px size-3.5 shrink-0" aria-hidden="true" />
					Not applied: {op.reason}
				</p>
			{/if}
			{#if op.test?.status === 'passed'}
				<p class="text-muted-foreground flex items-start gap-1.5 text-xs">
					<CircleCheckIcon class="text-success mt-px size-3.5 shrink-0" aria-hidden="true" />
					{op.test.detail}
				</p>
			{:else if op.test?.status === 'skipped'}
				<p class="text-muted-foreground flex items-start gap-1.5 text-xs">
					<CircleDashedIcon class="mt-px size-3.5 shrink-0" aria-hidden="true" />
					Not tried in a shadow run before it was applied. {op.test.detail}
				</p>
			{/if}
			{#each op.flags ?? [] as flag, index (index)}
				<p class="text-destructive flex items-start gap-1.5 text-xs">
					<TriangleAlertIcon class="mt-px size-3.5 shrink-0" aria-hidden="true" />
					{flag}
				</p>
			{/each}
			{#if op.status === 'held'}
				<p class="text-muted-foreground text-xs">
					Held back for review. If it is safe, apply it by hand in the playbook.
				</p>
			{/if}

			{#if op.test?.output}
				<Collapsible.Root>
					<Collapsible.Trigger variant="link">
						<ChevronRightIcon />
						Show the shadow run's output
					</Collapsible.Trigger>
					<Collapsible.Content>
						<div class="pt-2">
							<pre
								class="bg-muted max-h-80 overflow-auto rounded-md p-3 font-mono text-xs whitespace-pre-wrap">{op
									.test.output}</pre>
						</div>
					</Collapsible.Content>
				</Collapsible.Root>
			{/if}

			<!-- Without a diff, the proposed content is the only way to judge a held or rejected proposal -->
			{#if op.content && !section}
				<Collapsible.Root>
					<Collapsible.Trigger variant="link">
						<ChevronRightIcon />
						Show the proposed content
					</Collapsible.Trigger>
					<Collapsible.Content>
						<div class="pt-2">
							<CodeEditor
								value={op.content}
								language={opLanguage(op)}
								readonly
								label="Proposed content of {change.label}"
								class="h-auto max-h-80"
							/>
						</div>
					</Collapsible.Content>
				</Collapsible.Root>
			{/if}
		{/if}

		{#if section}
			<Collapsible.Root bind:open>
				<Collapsible.Trigger variant="link">
					<ChevronRightIcon />
					{open ? 'Hide changes' : 'Show changes'}
				</Collapsible.Trigger>
				<Collapsible.Content>
					<div class="pt-2">
						<DiffView before={section.before} after={section.after} showStats={false} />
					</div>
				</Collapsible.Content>
			</Collapsible.Root>
		{/if}
	</div>
</article>
