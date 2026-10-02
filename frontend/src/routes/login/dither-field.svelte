<script lang="ts">
	import { cn } from '#lib/utils/style.js';
	import type { Attachment } from 'svelte/attachments';
	import { createNoise2D } from './simplex-noise';

	let { class: className }: { class?: string } = $props();

	// Cell size in CSS pixels, the brand mark's pixel grid scaled up
	const CELL = 7;
	// Noise frequencies per CSS pixel for the broad shapes and the finer detail on top of them
	const BROAD = 0.002;
	const FINE = 0.005625;
	// Drift in CSS pixels per second, slow enough that cells flip one at a time instead of visibly scrolling
	const BROAD_DRIFT = 4;
	const FINE_DRIFT = 2.5;
	// The field barely changes between frames, so a low frame rate looks the same and spares the CPU
	const FRAME_MS = 1000 / 15;
	// The 4 by 4 Bayer matrix behind the mark's ordered dither, as thresholds between 0 and 1
	const BAYER = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5].map((v) => (v + 0.5) / 16);

	// Draws the field into the canvas for as long as it is mounted
	const dither: Attachment<HTMLCanvasElement> = (el) => {
		const ctx = el.getContext('2d');
		if (!ctx) return;

		const noise = createNoise2D(1337);
		const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
		const origin = performance.now();

		// The canvas stays transparent so the ink panel behind it shows through the empty cells
		// The two dither tones are the mark's white at low opacity, and the top tone is the brand lime
		const brand = getComputedStyle(el).getPropertyValue('--brand').trim() || '#c6f135';
		const tones = [
			{ color: '#fafafa', alpha: 2 / 32 },
			{ color: '#fafafa', alpha: 6 / 32 },
			{ color: brand, alpha: 30 / 32 }
		];

		let cell = 0;
		let cellCss = CELL;
		let cols = 0;
		let rows = 0;
		let levels = new Uint8Array(0);
		let frame = 0;
		let last = -Infinity;

		const draw = (t: number) => {
			const broad = (t / 1000) * BROAD_DRIFT;
			const fine = (t / 1000) * FINE_DRIFT;

			// Sort every cell into a tone by comparing the field against its Bayer threshold
			for (let y = 0, i = 0; y < rows; y++) {
				const py = y * cellCss;
				const row = (y & 3) << 2;
				for (let x = 0; x < cols; x++, i++) {
					const px = x * cellCss;
					const n =
						noise((px - broad) * BROAD, (py + broad / 3) * BROAD) * 0.75 +
						noise((px - fine) * FINE, py * FINE + 17) * 0.25;
					// Sparse beside the form and denser toward the right, like the mark's dither settling into its bar
					const v = 0.12 + n * 0.42 + (x / cols) ** 1.6 * 0.6;
					const level = Math.floor(v * 3 + BAYER[row | (x & 3)]);
					levels[i] = level < 0 ? 0 : level > 3 ? 3 : level;
				}
			}

			// Paint each tone in one pass, merging horizontal runs so a row needs only a few rectangles
			ctx.clearRect(0, 0, el.width, el.height);
			tones.forEach((tone, index) => {
				const level = index + 1;
				ctx.fillStyle = tone.color;
				ctx.globalAlpha = tone.alpha;
				for (let y = 0; y < rows; y++) {
					const offset = y * cols;
					for (let x = 0; x < cols; x++) {
						if (levels[offset + x] !== level) continue;
						const start = x;
						while (x + 1 < cols && levels[offset + x + 1] === level) x++;
						ctx.fillRect(start * cell, y * cell, (x - start + 1) * cell, cell);
					}
				}
			});
			ctx.globalAlpha = 1;
		};

		function tick(now: number) {
			frame = requestAnimationFrame(tick);
			if (now - last < FRAME_MS) return;
			last = now;
			draw(now - origin);
		}

		function start() {
			cancelAnimationFrame(frame);
			if (!cols) return;

			// Reduced motion keeps the same field but holds it still
			if (reduceMotion.matches) {
				draw(0);
				return;
			}
			draw(performance.now() - origin);
			frame = requestAnimationFrame(tick);
		}

		function resize() {
			const width = el.clientWidth;
			const height = el.clientHeight;

			// The panel is hidden on small screens, and a zero-sized canvas has nothing to animate
			if (!width || !height) {
				cols = 0;
				cancelAnimationFrame(frame);
				return;
			}

			// Whole device pixels per cell keep every cell hard edged, so the CSS cell size shifts slightly on fractional scales
			const dpr = Math.min(window.devicePixelRatio || 1, 2);
			el.width = Math.round(width * dpr);
			el.height = Math.round(height * dpr);
			cell = Math.max(2, Math.round(CELL * dpr));
			cellCss = cell / dpr;
			cols = Math.ceil(el.width / cell);
			rows = Math.ceil(el.height / cell);
			levels = new Uint8Array(cols * rows);
			start();
		}

		const observer = new ResizeObserver(resize);
		observer.observe(el);
		reduceMotion.addEventListener('change', start);

		return () => {
			cancelAnimationFrame(frame);
			observer.disconnect();
			reduceMotion.removeEventListener('change', start);
		};
	};
</script>

<canvas {@attach dither} class={cn('block size-full', className)} aria-hidden="true"></canvas>
