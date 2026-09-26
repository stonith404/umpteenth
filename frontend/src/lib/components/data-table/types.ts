import type { RowData } from '@tanstack/table-core';
import type { Component, Snippet } from 'svelte';

declare module '@tanstack/table-core' {
	// The type parameters must match TanStack's declaration for the merge to apply
	// eslint-disable-next-line @typescript-eslint/no-unused-vars
	interface ColumnMeta<TData extends RowData, TValue> {
		// The key the server sorts this column by (e.g. `createdAt`), columns without one are not sortable
		sortKey?: string;
		headerClass?: string;
		cellClass?: string;
	}
}

// The parameters every list endpoint takes (PLAN §12.1), plus the typed filters of the endpoint as comma-separated values
export type TableQuery = {
	page: number;
	pageSize: number;
	sort?: string;
	search?: string;
	[filter: string]: string | number | undefined;
};

// The part of the `Paginated[T]` envelope the table needs
export type TablePage<TData> = {
	items: TData[] | null;
	total: number;
};

export type TableFilterOption = {
	value: string;
	label: string;
	icon?: Component;
};

// What a custom filter control receives: the filter's values from the URL and a setter that writes them back
export type TableFilterControlProps = {
	selected: string[];
	onChange: (values: string[]) => void;
};

// A faceted filter, sent to the server as `<key>=<value>,<value>`
export type TableFilter = {
	key: string;
	label: string;
	options: TableFilterOption[];
	// Replaces the faceted popover with a custom control, e.g. a date range picker, while URL sync and reset keep working
	control?: Snippet<[TableFilterControlProps]>;
};
