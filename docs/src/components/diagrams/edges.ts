// The labeled arrows of the box-and-arrow diagrams

export type Label = { x: number; y: number; text: string; anchor?: 'start' | 'middle' | 'end' };
export type Edge = { points: [number, number][]; lime?: boolean; labels: Label[] };

// A path through the points that stops 2 units short, so the arrowhead's tip meets the box edge
export function arrowPath(pts: [number, number][]) {
	const [x1, y1] = pts[pts.length - 2];
	const [x2, y2] = pts[pts.length - 1];
	const len = Math.hypot(x2 - x1, y2 - y1);
	const end: [number, number] = [x2 - ((x2 - x1) / len) * 2, y2 - ((y2 - y1) / len) * 2];
	return 'M' + [...pts.slice(0, -1), end].map(([x, y]) => `${x},${y}`).join(' L');
}
