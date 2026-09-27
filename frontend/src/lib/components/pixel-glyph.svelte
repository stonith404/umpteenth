<script lang="ts" module>
	// Each glyph is drawn row by row on the Signal mark's grid, where # is a filled cell, and trimmed to its own size so it centres on whole cells
	const rows = {
		bot: ['..##..', '######', '######', '#.##.#', '######', '##..##', '######'],
		prompt: ['#.......', '.#......', '..#.....', '.#......', '#...####'],
		list: ['##.#####', '........', '##.#####', '........', '##.####.'],
		// The ump CLI is ours, so its glyph is a small Signal mark
		ump: ['#.#.###', '.#.####', '#.#.###', '.#.####', '#.#.###', '.#.####'],
		plug: ['.#..#.', '.#..#.', '######', '######', '.####.', '..##..', '..##..'],
		wrench: ['....#.#', '....###', '...##..', '..##...', '.##....', '##.....'],
		file: ['####..', '#..##.', '#...##', '#.##.#', '#....#', '#.##.#', '######'],
		'file-code': ['####..', '#..##.', '#...##', '#..#.#', '#.#..#', '#..#.#', '######'],
		crate: ['########', '#......#', '########', '.#....#.', '.#.##.#.', '.#....#.', '.######.'],
		flag: ['#.#.#.#', '##.#.#.', '#.#.#.#', '##.#.#.', '#......', '#......', '#......'],
		play: ['##....', '####..', '######', '####..', '##....'],
		shield: ['#######', '#####.#', '####.##', '#.#.###', '.#.###.', '..###..', '...#...'],
		rewind: ['..#....', '.##....', '######.', '.##...#', '..#...#', '.....#.', '..###..'],
		fold: ['...#...', '.#.#.#.', '..###..', '#######', '..###..', '.#.#.#.', '...#...'],
		alert: ['##', '##', '##', '##', '..', '##'],
		// The sand sits on top while waiting and has run out once time is up
		hourglass: ['######', '.####.', '..##..', '..##..', '.#..#.', '######'],
		timeout: ['######', '.#..#.', '..##..', '..##..', '.####.', '######'],
		check: ['......##', '.....##.', '##..##..', '.####...', '..##....'],
		cross: ['##..##', '.####.', '..##..', '.####.', '##..##'],
		dot: ['##', '##'],
		ring: ['####', '#..#', '#..#', '####'],
		minus: ['######', '######']
	} satisfies Record<string, string[]>;

	export type GlyphName = keyof typeof rows | 'dissolve';

	type Glyph = { width: number; height: number; d: string };

	function toGlyph(cells: string[]): Glyph {
		let d = '';
		cells.forEach((row, y) => {
			[...row].forEach((cell, x) => {
				if (cell === '#') d += `M${x} ${y}h1v1h-1z`;
			});
		});
		return { width: Math.max(...cells.map((row) => row.length)), height: cells.length, d };
	}

	// A destroyed sandbox is the crate thinned through a checkerboard, so it reads as dissolving into the mark's dither
	const dissolve = rows.crate.map((row, y) =>
		[...row].map((cell, x) => (cell === '#' && (x + y) % 2 === 0 ? '#' : '.')).join('')
	);

	// The paths only depend on the table, so they are built once for every glyph on the page
	const glyphs = Object.fromEntries(
		[...Object.entries(rows), ['dissolve', dissolve] as const].map(([name, cells]) => [
			name,
			toGlyph(cells)
		])
	) as Record<GlyphName, Glyph>;
</script>

<!--
@component
A pixel icon in the style of the Signal mark, drawn in the current text colour.
Each cell is 2 px, so a glyph stays crisp when its box centres it, as a square with an even size does.
Example: `<PixelGlyph name="bot" />`
-->
<script lang="ts">
	let { name, class: className }: { name: GlyphName; class?: string } = $props();

	const glyph = $derived(glyphs[name]);
</script>

<svg
	width={glyph.width * 2}
	height={glyph.height * 2}
	viewBox="0 0 {glyph.width} {glyph.height}"
	shape-rendering="crispEdges"
	class={className}
	aria-hidden="true"
>
	<path d={glyph.d} fill="currentColor" />
</svg>
