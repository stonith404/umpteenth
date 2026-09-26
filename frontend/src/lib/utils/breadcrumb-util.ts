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

// Builds the trail of an error page, which keeps the sections it can name and ends in the error's title instead of a raw ID from the URL
// A missing job at `/jobs/<id>` becomes Jobs › Job not found, and an unknown `/does-not-exist` becomes Page not found
export function buildErrorBreadcrumbs(
	pathname: string,
	error: { title: string; status: number },
	labels: Record<string, string> = {}
): Breadcrumb[] {
	const segments = pathname.split('/').filter(Boolean);
	if (segments.length === 0) return buildBreadcrumbs(pathname, labels);

	// On a 404 the last segment names nothing that exists, so it never becomes a crumb of its own
	const candidates = error.status === 404 ? segments.slice(0, -1) : segments;

	// Keep the leading sections the trail has a name for, up to the first segment it could only show raw
	const trail: Breadcrumb[] = [];
	for (const [i, segment] of candidates.entries()) {
		const label = labels[segment] ?? segmentLabels[segment];
		if (!label) break;
		trail.push({ label, href: `/${segments.slice(0, i + 1).join('/')}` });
	}

	// A page whose every segment has a name, e.g. an admin page a member may not open, keeps its usual trail
	if (trail.length === segments.length) {
		return trail.map((crumb, i) => (i === trail.length - 1 ? { label: crumb.label } : crumb));
	}

	return [...trail, { label: error.title }];
}
