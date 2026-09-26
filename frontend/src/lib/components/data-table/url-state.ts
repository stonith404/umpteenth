import { MAX_PAGE_SIZE } from '$lib/services/api-service';
import type { SortingState } from '@tanstack/table-core';

// Everything a table keeps in the URL, so filtered views are linkable and survive reload and back/forward
export type TableUrlState = {
	page: number;
	pageSize: number;
	// The server sort syntax (e.g. `-createdAt,name`), or null for the table's default sort
	sort: string | null;
	search: string;
	filters: Record<string, string[]>;
};

export type TableUrlConfig = {
	// Namespaces the parameters (e.g. `runs_page`), so several tables can share one page
	prefix?: string;
	defaultPageSize: number;
	filterKeys: string[];
};

export function paramName(key: string, prefix?: string) {
	return prefix ? `${prefix}_${key}` : key;
}

export function readTableUrlState(params: URLSearchParams, config: TableUrlConfig): TableUrlState {
	const get = (key: string) => params.get(paramName(key, config.prefix));

	const filters: Record<string, string[]> = {};
	for (const key of config.filterKeys) {
		const values = splitList(get(key));
		if (values.length > 0) filters[key] = values;
	}

	return {
		page: positiveInt(get('page')) ?? 1,
		pageSize: Math.min(positiveInt(get('pageSize')) ?? config.defaultPageSize, MAX_PAGE_SIZE),
		sort: get('sort') || null,
		search: get('search') ?? '',
		filters
	};
}

// Writes the state into `params`, leaving parameters of other tables untouched and omitting defaults to keep URLs short
export function writeTableUrlState(
	params: URLSearchParams,
	state: TableUrlState,
	config: TableUrlConfig
) {
	const set = (key: string, value: string | null) => {
		const name = paramName(key, config.prefix);
		if (value === null || value === '') params.delete(name);
		else params.set(name, value);
	};

	set('page', state.page > 1 ? String(state.page) : null);
	set('pageSize', state.pageSize !== config.defaultPageSize ? String(state.pageSize) : null);
	set('sort', state.sort);
	set('search', state.search.trim());
	for (const key of config.filterKeys) {
		set(key, (state.filters[key] ?? []).join(','));
	}
}

// Parses the server sort syntax into TanStack sorting state, dropping keys no column declares
export function parseSort(sort: string, columnIdBySortKey: Map<string, string>): SortingState {
	return sort
		.split(',')
		.map((part) => part.trim())
		.filter(Boolean)
		.flatMap((part) => {
			const desc = part.startsWith('-');
			const id = columnIdBySortKey.get(desc ? part.slice(1) : part);
			return id ? [{ id, desc }] : [];
		});
}

// Formats TanStack sorting state in the server sort syntax, e.g. `-createdAt,name`
export function formatSort(sorting: SortingState, sortKeyByColumnId: Map<string, string>): string {
	return sorting
		.flatMap(({ id, desc }) => {
			const key = sortKeyByColumnId.get(id);
			return key ? [`${desc ? '-' : ''}${key}`] : [];
		})
		.join(',');
}

function positiveInt(value: string | null): number | null {
	if (!value) return null;
	const n = Number.parseInt(value, 10);
	return Number.isFinite(n) && n > 0 ? n : null;
}

function splitList(value: string | null): string[] {
	if (!value) return [];
	return value
		.split(',')
		.map((v) => v.trim())
		.filter(Boolean);
}
