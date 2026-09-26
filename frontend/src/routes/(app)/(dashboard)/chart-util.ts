// Stats buckets are UTC days, so their labels are formatted in UTC to stay on the right day in every time zone
const dayFormat = new Intl.DateTimeFormat('en-US', {
	month: 'short',
	day: 'numeric',
	timeZone: 'UTC'
});

export function formatDay(value: Date | number): string {
	return dayFormat.format(value);
}

// Picks about `maxLabels` evenly spaced days as x-axis ticks, so 90 daily points don't print 90 overlapping dates
// The ticks are the data's own UTC-midnight dates, because a chart's automatic time ticks fall on local midnights and would be labelled with the wrong UTC day
// Counting from the end keeps the latest day, usually today, labelled
export function sparseDayTicks(days: Date[], maxLabels = 8): Date[] {
	const step = Math.max(1, Math.ceil(days.length / maxLabels));
	const last = days.length - 1;
	return days.filter((_, i) => (last - i) % step === 0);
}
