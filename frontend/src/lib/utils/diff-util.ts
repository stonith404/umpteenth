export type DiffLine = {
	type: 'same' | 'added' | 'removed';
	text: string;
	// Line numbers in the old and new text, missing on the side the line does not exist in
	oldNumber?: number;
	newNumber?: number;
};

// A run of unchanged lines hidden between changes, shown as "N unchanged lines"
export type DiffGap = { type: 'gap'; count: number };

export type DiffHunkLine = DiffLine | DiffGap;

// Above this many compared line pairs the diff gives up on alignment and shows a full replacement, which keeps huge inputs from freezing the page
const MAX_CELLS = 4_000_000;

// Unchanged lines shown around each change, the rest collapse into a gap marker
const CONTEXT_LINES = 3;

// Computes a line diff with a longest-common-subsequence table after trimming the common prefix and suffix
export function diffLines(oldText: string, newText: string): DiffLine[] {
	const a = splitLines(oldText);
	const b = splitLines(newText);

	// Common prefix and suffix are unchanged, so only the middle needs the quadratic table
	let start = 0;
	while (start < a.length && start < b.length && a[start] === b[start]) start++;
	let endA = a.length;
	let endB = b.length;
	while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
		endA--;
		endB--;
	}

	const lines: DiffLine[] = [];
	for (let i = 0; i < start; i++) {
		lines.push({ type: 'same', text: a[i], oldNumber: i + 1, newNumber: i + 1 });
	}

	const midA = a.slice(start, endA);
	const midB = b.slice(start, endB);
	for (const line of diffMiddle(midA, midB)) {
		lines.push({
			...line,
			oldNumber: line.oldNumber !== undefined ? line.oldNumber + start : undefined,
			newNumber: line.newNumber !== undefined ? line.newNumber + start : undefined
		});
	}

	for (let i = 0; i < a.length - endA; i++) {
		lines.push({
			type: 'same',
			text: a[endA + i],
			oldNumber: endA + i + 1,
			newNumber: endB + i + 1
		});
	}
	return lines;
}

// Collapses unchanged lines that are further than `CONTEXT_LINES` lines away from a change
export function collapseUnchanged(lines: DiffLine[]): DiffHunkLine[] {
	const keep = new Array<boolean>(lines.length).fill(false);
	lines.forEach((line, i) => {
		if (line.type === 'same') return;
		for (
			let j = Math.max(0, i - CONTEXT_LINES);
			j <= Math.min(lines.length - 1, i + CONTEXT_LINES);
			j++
		) {
			keep[j] = true;
		}
	});

	const out: DiffHunkLine[] = [];
	let hidden = 0;
	lines.forEach((line, i) => {
		if (keep[i]) {
			if (hidden > 0) out.push({ type: 'gap', count: hidden });
			hidden = 0;
			out.push(line);
		} else {
			hidden++;
		}
	});
	if (hidden > 0) out.push({ type: 'gap', count: hidden });
	return out;
}

// Counts added and removed lines, e.g. for a "+3 −1" summary
export function diffStats(lines: DiffLine[]) {
	let added = 0;
	let removed = 0;
	for (const line of lines) {
		if (line.type === 'added') added++;
		else if (line.type === 'removed') removed++;
	}
	return { added, removed };
}

function diffMiddle(a: string[], b: string[]): DiffLine[] {
	if (a.length === 0) return b.map((text, j) => ({ type: 'added', text, newNumber: j + 1 }));
	if (b.length === 0) return a.map((text, i) => ({ type: 'removed', text, oldNumber: i + 1 }));
	if (a.length * b.length > MAX_CELLS) {
		return [
			...a.map((text, i): DiffLine => ({ type: 'removed', text, oldNumber: i + 1 })),
			...b.map((text, j): DiffLine => ({ type: 'added', text, newNumber: j + 1 }))
		];
	}

	// lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:]
	const cols = b.length + 1;
	const lcs = new Uint32Array((a.length + 1) * cols);
	for (let i = a.length - 1; i >= 0; i--) {
		for (let j = b.length - 1; j >= 0; j--) {
			lcs[i * cols + j] =
				a[i] === b[j]
					? lcs[(i + 1) * cols + j + 1] + 1
					: Math.max(lcs[(i + 1) * cols + j], lcs[i * cols + j + 1]);
		}
	}

	// Walk the table, preferring removals before additions so replaced lines read top to bottom
	const out: DiffLine[] = [];
	let i = 0;
	let j = 0;
	while (i < a.length && j < b.length) {
		if (a[i] === b[j]) {
			out.push({ type: 'same', text: a[i], oldNumber: i + 1, newNumber: j + 1 });
			i++;
			j++;
		} else if (lcs[(i + 1) * cols + j] >= lcs[i * cols + j + 1]) {
			out.push({ type: 'removed', text: a[i], oldNumber: i + 1 });
			i++;
		} else {
			out.push({ type: 'added', text: b[j], newNumber: j + 1 });
			j++;
		}
	}
	for (; i < a.length; i++) out.push({ type: 'removed', text: a[i], oldNumber: i + 1 });
	for (; j < b.length; j++) out.push({ type: 'added', text: b[j], newNumber: j + 1 });
	return out;
}

function splitLines(text: string): string[] {
	if (text === '') return [];
	return text.replace(/\r\n/g, '\n').replace(/\n$/, '').split('\n');
}
