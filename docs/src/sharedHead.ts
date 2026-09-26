// Head tags every page shares, used by the Starlight config for the docs and by the landing page, which renders its own head
export const sharedHead = (site: URL | string) => [
	// Link previews on chat apps and social sites show the brand card, 1200 by 630 as they expect
	{ tag: 'meta', attrs: { property: 'og:image', content: new URL('/og.png', site).href } },
	{ tag: 'meta', attrs: { property: 'og:image:width', content: '1200' } },
	{ tag: 'meta', attrs: { property: 'og:image:height', content: '630' } },
	{ tag: 'meta', attrs: { property: 'og:image:alt', content: 'Umpteenth: the umpteenth time runs itself.' } },
	// iOS uses the touch icon for bookmarks and home screen shortcuts, since it ignores the SVG favicon
	{ tag: 'link', attrs: { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' } },
	// Every page sets its text in Inter, so fetching it with the HTML keeps the fallback font from swapping in late and shifting the layout
	{ tag: 'link', attrs: { rel: 'preload', href: '/fonts/Inter-latin.woff2', as: 'font', type: 'font/woff2', crossorigin: 'anonymous' } }
];
