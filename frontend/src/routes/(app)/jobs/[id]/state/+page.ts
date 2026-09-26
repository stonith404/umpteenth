import type { PageLoad } from './$types';

// The table loads its own pages, so the route only needs the job from the layout
export const load: PageLoad = async () => ({});
