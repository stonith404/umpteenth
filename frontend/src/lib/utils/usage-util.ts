import { page } from '$app/state';
import type { UsageUnit, User } from '$lib/api/types';
import { formatMicroCost, formatMicroCostTick, formatTokens } from '$lib/utils/format-util';

// A usage figure as the API reports it: the price in integer micro-USD next to the input and output tokens behind it
export type Usage = { cost: number; tokens: number };

// How usage reads in one unit, so every page shows the workspace's unit with the same words and never both units at once
export type UsageFormat = {
	unit: UsageUnit;
	// The figure in this unit, micro-USD or tokens, e.g. to sum, compare or chart it
	pick: (usage: Usage) => number;
	// A figure of this unit that names the unit, e.g. `$0.14` or `37.7k tokens`
	format: (value: number) => string;
	// A figure of this unit under a label that already names the unit, e.g. `$0.14` or `37.7k`
	formatBare: (value: number) => string;
	// A chart axis tick, given the step between ticks so neighbours share their decimals, e.g. `$0.05` or `20k`
	formatTick: (value: number, step?: number) => string;
	// Token counts are whole, so their axes never step by fractions
	integer: boolean;
	// The runs list's sort key for this figure
	sortKey: 'cost' | 'tokens';
	// A column, stat or tooltip row, e.g. `Cost` or `Tokens`
	label: string;
	// A period's total, e.g. `Spend` or `Tokens`
	totalLabel: string;
	// A mean per run, e.g. `Average cost` or `Average tokens`
	averageLabel: string;
	// The figure inside a sentence, e.g. `their cost, duration and mode`
	noun: string;
	// A sentence's verb for spending it, e.g. `Reflection cost $0.02` or `Reflection used 3.1k tokens`
	verb: string;
	// What a chart shows when nothing was used, e.g. `No spend`
	noneLabel: string;
	// What runs do as a job learns, e.g. `runs cost less` or `runs use fewer tokens`
	fallsPhrase: string;
	// The dashboard list of jobs whose runs got cheaper or leaner
	trendLabel: string;
};

const FORMATS: Record<UsageUnit, UsageFormat> = {
	price: {
		unit: 'price',
		pick: (usage) => usage.cost,
		format: formatMicroCost,
		formatBare: formatMicroCost,
		formatTick: formatMicroCostTick,
		integer: false,
		sortKey: 'cost',
		label: 'Cost',
		totalLabel: 'Spend',
		averageLabel: 'Average cost',
		noun: 'cost',
		verb: 'cost',
		noneLabel: 'No spend',
		fallsPhrase: 'cost less',
		trendLabel: 'Getting cheaper'
	},
	tokens: {
		unit: 'tokens',
		pick: (usage) => usage.tokens,
		format: (value) => `${formatTokenCount(value)} tokens`,
		formatBare: formatTokenCount,
		formatTick: (value) => formatTokenCount(value),
		integer: true,
		sortKey: 'tokens',
		label: 'Tokens',
		totalLabel: 'Tokens',
		averageLabel: 'Average tokens',
		noun: 'token usage',
		verb: 'used',
		noneLabel: 'No token usage',
		fallsPhrase: 'use fewer tokens',
		trendLabel: 'Getting leaner'
	}
};

// Averages can have fractions, which a token count never shows
function formatTokenCount(value: number) {
	return formatTokens(Math.round(value));
}

// The unit the workspace shows usage in, read from the session's workspace that every page already has
// Read inside a component's template or `$derived`, it follows the setting as soon as the session reloads
function usageUnit(): UsageUnit {
	return (page.data.user as User | null | undefined)?.workspace.usageUnit ?? 'price';
}

// The formatters and labels of the workspace's unit
export function usageFormat(): UsageFormat {
	return FORMATS[usageUnit()] ?? FORMATS.price;
}
