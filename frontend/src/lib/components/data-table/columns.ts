import type { ColumnDef } from '@tanstack/table-core';
import { createRawSnippet } from 'svelte';
import {
	renderSnippet,
	type RenderComponentConfig,
	type RenderSnippetConfig
} from './render-helpers';

// The actions header is for screen readers only, the buttons below speak for themselves
// It is as wide as the '⋯' button, so the columns keep their places when a search leaves no rows to hold the column open
const actionsHeader = createRawSnippet(() => ({
	render: () =>
		'<span class="inline-block w-8 align-middle"><span class="sr-only">Actions</span></span>'
}));

// The standard row actions column: as narrow as its content, right-aligned, pinned to the right edge and never hidden
// Usage: `actionsColumn<Secret>((secret) => renderSnippet(actionsCell, secret))`, where the snippet renders RowActions
export function actionsColumn<TData>(
	cell: (row: TData) => RenderSnippetConfig<any> | RenderComponentConfig<any>
): ColumnDef<TData> {
	return {
		id: 'actions',
		header: () => renderSnippet(actionsHeader),
		meta: { headerClass: 'w-0', cellClass: 'w-0 text-right' },
		cell: ({ row }) => cell(row.original)
	};
}
