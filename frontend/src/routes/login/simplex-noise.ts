// 2D simplex noise after Stefan Gustavson's reference implementation
// The permutation is shuffled from a seed, so the field looks the same on every visit
export function createNoise2D(seed: number): (x: number, y: number) => number {
	const random = mulberry32(seed);
	const perm = Array.from({ length: 256 }, (_, i) => i);
	for (let i = 255; i > 0; i--) {
		const j = Math.floor(random() * (i + 1));
		[perm[i], perm[j]] = [perm[j], perm[i]];
	}
	const p = new Uint8Array(512);
	for (let i = 0; i < 512; i++) p[i] = perm[i & 255];

	const gradients = [
		[1, 1],
		[-1, 1],
		[1, -1],
		[-1, -1],
		[1, 0],
		[-1, 0],
		[0, 1],
		[0, -1]
	];
	const F2 = 0.5 * (Math.sqrt(3) - 1);
	const G2 = (3 - Math.sqrt(3)) / 6;

	// Each corner of the simplex contributes a falloff times the dot product with its gradient
	const corner = (hash: number, x: number, y: number) => {
		let t = 0.5 - x * x - y * y;
		if (t <= 0) return 0;
		const g = gradients[hash & 7];
		t *= t;
		return t * t * (g[0] * x + g[1] * y);
	};

	return (xin, yin) => {
		// Skew into the simplex grid to find the containing triangle
		const s = (xin + yin) * F2;
		const i = Math.floor(xin + s);
		const j = Math.floor(yin + s);
		const t = (i + j) * G2;
		const x0 = xin - (i - t);
		const y0 = yin - (j - t);
		const i1 = x0 > y0 ? 1 : 0;
		const j1 = x0 > y0 ? 0 : 1;

		// Sum the three corners and scale the result to roughly -1 to 1
		const ii = i & 255;
		const jj = j & 255;
		const n =
			corner(p[ii + p[jj]], x0, y0) +
			corner(p[ii + i1 + p[jj + j1]], x0 - i1 + G2, y0 - j1 + G2) +
			corner(p[ii + 1 + p[jj + 1]], x0 - 1 + 2 * G2, y0 - 1 + 2 * G2);
		return 70 * n;
	};
}

function mulberry32(seed: number): () => number {
	return () => {
		seed = (seed + 0x6d2b79f5) | 0;
		let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}
