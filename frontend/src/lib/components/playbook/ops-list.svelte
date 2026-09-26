<script lang="ts">
	import type { PlaybookAppliedOp } from '$lib/api/types';
	import CodeEditor from '$lib/components/code/code-editor.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import { opLabel, opLanguage, opStatusLabel } from '$lib/utils/playbook-util';
	import { cn } from '$lib/utils/style';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import CircleDashedIcon from '@lucide/svelte/icons/circle-dashed';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';

	let { ops }: { ops: PlaybookAppliedOp[] } = $props();
</script>

<ul class="flex flex-col divide-y">
	{#each ops as op, i (i)}
		<li class="flex flex-col gap-1.5 py-3 first:pt-0 last:pb-0">
			<div class="flex flex-wrap items-center gap-2">
				<span
					class={cn('text-sm font-medium', op.status === 'rejected' && 'text-muted-foreground')}
				>
					{opLabel(op)}
				</span>
				<Badge
					variant={op.status === 'applied'
						? 'secondary'
						: op.status === 'held'
							? 'destructive'
							: 'outline'}
				>
					{opStatusLabel(op.status)}
				</Badge>
			</div>
			<p class="text-muted-foreground text-sm">{op.rationale}</p>
			{#if op.text}
				<p class="text-sm">{op.text}</p>
			{/if}
			{#if op.status === 'rejected' && op.reason}
				<p class="text-muted-foreground text-xs">Not applied: {op.reason}</p>
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
			{#if op.test?.output}
				<Collapsible.Root>
					<Collapsible.Trigger
						class="text-muted-foreground group inline-flex items-center gap-1 text-xs hover:underline"
					>
						<ChevronRightIcon
							class="size-3.5 transition-transform group-data-[state=open]:rotate-90"
						/>
						Show the shadow run's output
					</Collapsible.Trigger>
					<Collapsible.Content class="pt-2">
						<pre
							class="bg-muted max-h-80 overflow-auto rounded-md p-3 font-mono text-xs whitespace-pre-wrap">{op
								.test.output}</pre>
					</Collapsible.Content>
				</Collapsible.Root>
			{/if}
			{#each op.flags ?? [] as flag (flag)}
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
			{#if op.content}
				<Collapsible.Root>
					<Collapsible.Trigger
						class="text-muted-foreground group inline-flex items-center gap-1 text-xs hover:underline"
					>
						<ChevronRightIcon
							class="size-3.5 transition-transform group-data-[state=open]:rotate-90"
						/>
						Show content
					</Collapsible.Trigger>
					<Collapsible.Content class="pt-2">
						<CodeEditor
							value={op.content}
							language={opLanguage(op)}
							readonly
							label="Content of {opLabel(op)}"
							class="h-auto max-h-80"
						/>
					</Collapsible.Content>
				</Collapsible.Root>
			{/if}
		</li>
	{/each}
</ul>
