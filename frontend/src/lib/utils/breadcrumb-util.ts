import { segmentLabels } from '$lib/navigation';

export type Breadcrumb = {
	label: string;
	// Missing for the current page, which is not a link
	href?: string;
};

// Builds the breadcrumb trail of a path, e.g. `/settings/tokens` becomes Settings › API tokens
export function buildBreadcrumbs(
	pathname: string,
	labels: Record<string, string> = {}
): Breadcrumb[] {
	const segments = pathname.split('/').filter(Boolean);
	if (segments.length === 0) return [{ label: 'Dashboard' }];

	return segments.map((segment, i) => {
		const isLast = i === segments.length - 1;
		return {
			label: labels[segment] ?? segmentLabels[segment] ?? decodeURIComponent(segment),
			href: isLast ? undefined : `/${segments.slice(0, i + 1).join('/')}`
		};
	});
}
