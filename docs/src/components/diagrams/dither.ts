// The Signal mark's ordered dither, shared by the drawings that show a job settling from exploring into a script
export const bayer = [
	[0, 8, 2, 10],
	[12, 4, 14, 6],
	[3, 11, 1, 9],
	[15, 7, 13, 5]
];

export type Cell = { x: number; y: number; bar: boolean };

// Cells of a w by h field at the given density, where a density of 1 or more is the solid lime bar
export function field(w: number, h: number, density: number): Cell[] {
	const cells: Cell[] = [];
	for (let y = 0; y < h; y++) {
		for (let x = 0; x < w; x++) {
			if (density >= 1) cells.push({ x, y, bar: true });
			else if (bayer[y % 4][x % 4] < density * 16) cells.push({ x, y, bar: false });
		}
	}
	return cells;
}
