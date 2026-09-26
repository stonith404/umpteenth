import { clsx, type ClassValue } from 'clsx';
import { extendTailwindMerge } from 'tailwind-merge';

// tailwind-merge only knows Tailwind's own scales, so the named tokens of app.css's @theme block are listed here
// Without them a size like text-2xs reads as a text color and would drop the color beside it, and h-dialog-editor would not replace a component's own height
const twMerge = extendTailwindMerge({
	extend: {
		theme: {
			text: ['2xs', 'figure', 'code-inline'],
			spacing: ['chart'],
			container: ['lede', 'lede-narrow']
		},
		classGroups: {
			h: [{ h: ['dialog-editor'] }],
			'max-h': [{ 'max-h': ['snapshot'] }],
			'max-w': [{ 'max-w': ['answer'] }],
			'grid-cols': [
				{
					'grid-cols': [
						'answer-row',
						'form-rail',
						'io-field',
						'io-field-header',
						'io-field-stacked',
						'key-value',
						'label-figure',
						'label-value',
						'label-value-clipped',
						'legend',
						'outputs',
						'pairs',
						'secret-mapping',
						'secret-mapping-header',
						'sign-in',
						'trailing-action',
						'waterfall',
						'waterfall-wide'
					]
				}
			],
			'grid-rows': [{ 'grid-rows': ['frame'] }]
		}
	}
});

// Joins class names and lets later Tailwind utilities override conflicting earlier ones
export function cn(...inputs: ClassValue[]) {
	return twMerge(clsx(inputs));
}

export type WithoutChild<T> = T extends { child?: any } ? Omit<T, 'child'> : T;
export type WithoutChildren<T> = T extends { children?: any } ? Omit<T, 'children'> : T;
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>;
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & { ref?: U | null };
