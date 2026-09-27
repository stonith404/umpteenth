// Class sets for the parts every diagram shares, themed through the dg colors in src/styles/global.css so one drawing works in both themes
// Each variant is a full set rather than a modifier, because two utilities for the same property would be settled by stylesheet order instead of class order

// Regions such as "the host" or "a sandbox", with a dashed edge for things that are thrown away
const zone = 'fill-dg-zone stroke-1';
const box = 'stroke-1';
const title = 'text-[13px] font-semibold';
const sub = 'text-[11.5px]';
const edge = 'fill-none';

// Edge labels get a halo in the card color so they stay readable where they cross a line
const edgeLabel = 'text-[11px] [paint-order:stroke] stroke-dg-card stroke-4 [stroke-linejoin:round]';

export const dg = {
	zone: `${zone} stroke-dg-edge`,
	zoneDashed: `${zone} stroke-dg-line [stroke-dasharray:5_4]`,
	zoneLabel: 'fill-dg-muted text-[12px] font-semibold',

	// Boxes for components, with lime reserved for the one element a drawing is about
	box: `${box} fill-dg-card stroke-dg-edge`,
	boxLime: 'fill-dg-lime stroke-none',
	boxMuted: `${box} fill-transparent stroke-dg-line [stroke-dasharray:4_3]`,
	boxDanger: `${box} fill-dg-card stroke-dg-danger`,
	title: `${title} fill-dg-ink`,
	titleOnLime: `${title} fill-dg-on-lime`,
	sub: `${sub} fill-dg-muted`,
	subOnLime: `${sub} fill-dg-on-lime opacity-72`,

	// Edges and their arrowheads, in lime for the path a drawing is about and red for one that fails
	edge: `${edge} stroke-dg-line stroke-[1.5]`,
	edgeLime: `${edge} stroke-dg-lime-line stroke-2`,
	edgeLimeDashed: `${edge} stroke-dg-lime-line stroke-2 [stroke-dasharray:5_4]`,
	edgeDanger: `${edge} stroke-dg-danger stroke-[1.5]`,
	arrowhead: 'fill-dg-line',
	arrowheadLime: 'fill-dg-lime-line',
	arrowheadDanger: 'fill-dg-danger',
	edgeLabel: `${edgeLabel} fill-dg-muted`,
	edgeLabelLime: `${edgeLabel} fill-dg-lime-line font-semibold`,
	edgeLabelDanger: `${edgeLabel} fill-dg-danger`,

	// The solid lime bar the brand's dither settles into once a script runs
	bar: 'fill-dg-lime'
};

// The shared box, title and subtitle sets for a box that may be the lime one or a muted one
export const dgBox = (tone?: 'lime' | 'muted' | 'danger') =>
	tone === 'lime' ? dg.boxLime : tone === 'muted' ? dg.boxMuted : tone === 'danger' ? dg.boxDanger : dg.box;
export const dgTitle = (lime?: boolean) => (lime ? dg.titleOnLime : dg.title);
export const dgSub = (lime?: boolean) => (lime ? dg.subOnLime : dg.sub);
