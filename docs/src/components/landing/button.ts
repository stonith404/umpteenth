// Kumo controls for the landing page: 8px corners, a top-lit blue gradient for the primary and a hairline control for the secondary
// Every variant sets its own border color, since a shared transparent border color would fight the variant's in Tailwind's ordering
// The background stays under the translucent border
const base =
	'inline-flex items-center gap-2 h-10 px-4 rounded-lg border font-medium text-base leading-none no-underline transition-[background] duration-120 ease-[ease]';

export const button = {
	// The gradient and its borders are mixed from one base color in --e, so the hover only has to lift the top stop
	primary: `${base} [--e:var(--color-blue)] text-white border-[color-mix(in_oklch,var(--e),black_10%)] bg-[linear-gradient(to_bottom,color-mix(in_oklch,var(--e),white_15%),var(--e))] [box-shadow:inset_0_1px_0_0_color-mix(in_oklch,var(--e),white_30%),0_1px_2px_rgba(0,0,0,0.05)] hover:bg-[linear-gradient(to_bottom,color-mix(in_oklch,var(--e),white_30%),var(--e))]`,
	secondary: `${base} text-fg bg-surface border-surface-border [box-shadow:var(--surface-shadow)] hover:bg-gray-7`
};
