import DOMPurify from 'dompurify';
import { Marked } from 'marked';

// Run summaries and assistant text are written by the agent, which may be steered by prompt injection
// Only plain text formatting survives, so injected markup can't draw overlays, submit forms with the viewer's session or load remote resources
// Data and ARIA attributes are dropped too, since DOMPurify keeps them by default and the app's own scripts and styles select on some of them
const ALLOWED_TAGS = [
	'a',
	'blockquote',
	'br',
	'code',
	'del',
	'em',
	'h1',
	'h2',
	'h3',
	'h4',
	'h5',
	'h6',
	'hr',
	'li',
	'ol',
	'p',
	'pre',
	'strong',
	'table',
	'tbody',
	'td',
	'th',
	'thead',
	'tr',
	'ul'
];
const ALLOWED_ATTR = ['href', 'title', 'align', 'start'];

function escapeHtml(value: string): string {
	return value
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;')
		.replaceAll('"', '&quot;');
}

// An image would be fetched as soon as the page renders, which lets a run leak data through the viewer's browser even without network access
// Images become links instead, so nothing is requested until the viewer chooses to open one
const marked = new Marked({
	gfm: true,
	renderer: {
		image({ href, text }) {
			return `<a href="${escapeHtml(href)}">${escapeHtml(text || href)}</a>`;
		}
	}
});

let hooksInstalled = false;

// Links in rendered markdown leave the app, so they open in a new tab without access to it
function installHooks() {
	if (hooksInstalled) return;
	hooksInstalled = true;
	DOMPurify.addHook('afterSanitizeAttributes', (node) => {
		if (node.tagName === 'A' && node.getAttribute('href')) {
			node.setAttribute('target', '_blank');
			node.setAttribute('rel', 'noopener noreferrer');
		}
	});
}

// Renders untrusted markdown (instructions, run summaries) to HTML that is safe to insert with {@html}
export function renderMarkdown(source: string): string {
	installHooks();
	const html = marked.parse(source, { async: false });
	return DOMPurify.sanitize(html, {
		ALLOWED_TAGS,
		ALLOWED_ATTR,
		ALLOW_DATA_ATTR: false,
		ALLOW_ARIA_ATTR: false
	});
}
