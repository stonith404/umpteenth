// Captures the docs screenshots from the seeded stack, in light and dark
import fs from 'node:fs';
import { open, settle, unionBox, out, content, state } from './cap.mjs';

const runOf = (prefix, n) => state.runs.find((r) => r.label.startsWith(prefix) && r.number === n).id;
const jobs = state.ids.jobs;

// Waits until code editors measured their lines with the loaded fonts
async function fixEditors(page) {
	await page.evaluate(async () => {
		await document.fonts.ready;
	});
	// CodeMirror measures its gutter only once an editor has been in view, so each visible editor is scrolled to once
	const n = await page.evaluate(() => document.querySelectorAll('.cm-editor').length);
	for (let i = 0; i < n; i++) {
		await page.evaluate((i) => {
			const ed = document.querySelectorAll('.cm-editor')[i];
			if (ed.getBoundingClientRect().height > 0) ed.scrollIntoView({ block: 'center' });
		}, i);
		await page.waitForTimeout(400);
	}
	await page.evaluate(() => window.scrollTo(0, 0));
	await page.waitForTimeout(600);
}

async function mainBox(page) {
	return page.locator(content).boundingBox();
}

// Clips the content column from its top down to the bottom of `last`, keeping the column's padding
async function columnClip(page, last, bottomPad = 32) {
	const main = await mainBox(page);
	const box = await page.locator(last).last().boundingBox();
	return { x: main.x, y: main.y, width: main.width, height: box.y + box.height - main.y + bottomPad };
}

const shots = {
	dashboard: {
		url: '/',
		ready: (page) => page.getByText('Getting cheaper').waitFor(),
		clip: async (page) => {
			const main = await mainBox(page);
			return { x: main.x, y: main.y, width: main.width, height: main.height };
		}
	},
	'job-overview': {
		url: `/jobs/${jobs.hn}`,
		ready: (page) => page.getByText('Graduation').first().waitFor(),
		clip: (page) => columnClip(page, '[data-slot="card"]:has-text("Cost and duration of every run")', 12)
	},
	playbook: {
		url: `/jobs/${jobs.hn}/playbook`,
		dpr: 1.5,
		ready: (page) => page.getByText('Every change is a version').waitFor(),
		clip: async (page) => {
			const main = await mainBox(page);
			return { x: main.x, y: main.y, width: main.width, height: main.height };
		}
	},
	'run-timeline': {
		url: `/runs/${runOf('hn-', 3)}`,
		dpr: 1.5,
		ready: (page) => page.getByText('Sandbox destroyed').waitFor(),
		// The digest fetches the live Hacker News front page, and public docs must not show real third-party headlines, so fixed fictional stories replace them
		act: async (page) => {
			await page.evaluate(() => {
				const stories = [
					['Show HN: A 400-line RSS reader for a Raspberry Pi', 'https://blog.example.dev/rss-pi'],
					['SQLite as a job queue, two years in', 'https://notes.example.org/sqlite-queue'],
					['The quiet joy of cron', 'https://example.net/cron'],
					['Reading your own flame graphs', 'https://example.com/flame-graphs'],
					['A field guide to retry budgets', 'https://example.org/retry-budgets']
				];
				const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
				const nodes = [];
				while (walker.nextNode()) nodes.push(walker.currentNode);
				for (const node of nodes) {
					const text = node.nodeValue
						.replace(/^(\d+)\. \[[^\]]*\]\([^)]*\)/gm, (m, n) => {
							const [title, url] = stories[(Number(n) - 1) % stories.length];
							return `${n}. [${title}](${url})`;
						})
						.replace(/The top story is "[^"]*"/, `The top story is "${stories[0][0]}"`);
					if (text !== node.nodeValue) node.nodeValue = text;
				}
			});
		},
		clip: async (page) => {
			const main = await mainBox(page);
			return { x: main.x, y: main.y, width: main.width, height: main.height };
		}
	},
	'run-learned': {
		url: `/runs/${runOf('hn-', 1)}?tab=learned`,
		ready: (page) => page.getByText('What this run taught the job').waitFor(),
		clip: async (page) => {
			const main = await mainBox(page);
			const script = await page.getByText('Script fetch_front_page', { exact: true }).first().boundingBox();
			return { x: main.x, y: main.y, width: main.width, height: script.y - main.y - 14 };
		}
	},
	'settings-providers': {
		url: '/settings/providers?models_status=enabled',
		ready: (page) => page.getByText('Claude Sonnet 5').waitFor(),
		clip: async (page) => {
			const main = await mainBox(page);
			return { x: main.x, y: main.y, width: main.width, height: main.height };
		}
	},
	'settings-general': {
		url: '/settings/general',
		ready: (page) => page.getByText('Show usage as').waitFor(),
		// Models and costs shows the cards it covers, from the default models to the usage unit
		clip: async (page) => {
			const main = await mainBox(page);
			const box = await unionBox(page, [
				page.locator('[data-slot="card"]', { hasText: 'Default models' }).first(),
				page.locator('[data-slot="card"]', { hasText: 'Show usage as' }).last()
			]);
			return { x: main.x, y: box.y - 18, width: main.width, height: box.height + 36 };
		}
	},
	'mcp-servers': {
		url: '/mcp',
		ready: (page) => page.getByText('24 tools').waitFor(),
		// A list that fits one page has no pagination row, so the crop ends at the table
		clip: (page) => columnClip(page, `${content} table`, 24)
	},
	'webhook-token': {
		viewportOnly: true,
		url: `/jobs/${jobs.deps}/settings`,
		ready: (page) => page.getByText('Delete job').first().waitFor(),
		act: async (page) => {
			const generate = page.getByRole('button', { name: 'Generate token' });
			if (await generate.count()) {
				await generate.click();
			} else {
				await page.getByRole('button', { name: 'Rotate token' }).click();
				await page.getByRole('alertdialog').getByRole('button', { name: 'Rotate' }).click();
			}
			await page.getByRole('dialog').getByText('Webhook token').waitFor();
			// The example curl scrolls sideways in the narrow dialog, so the dialog gets room for the whole command, and the page behind it is left out
			await page.evaluate(() => {
				const dialog = document.querySelector('[role="dialog"]');
				dialog.style.maxWidth = '820px';
				for (const el of document.querySelectorAll('[data-slot="sidebar-inset"], [data-slot="sidebar"], [data-slot="sidebar-container"]')) el.style.visibility = 'hidden';
			});
			await page.waitForTimeout(300);
		},
		clip: async (page) => {
			const box = await page.getByRole('dialog').boundingBox();
			return { x: box.x - 32, y: box.y - 32, width: box.width + 64, height: box.height + 64 };
		}
	}
};

const only = process.argv.slice(2);
for (const [name, shot] of Object.entries(shots)) {
	if (only.length && !only.includes(name)) continue;
	for (const scheme of ['light', 'dark']) {
		const { browser, page } = await open(scheme, { dpr: shot.dpr ?? 2 });
		await page.goto(shot.url);
		await shot.ready(page);
		await fixEditors(page);
		if (shot.act) await shot.act(page);
		await settle(page, { wait: 1200 });
		const clip = await shot.clip(page);
		const file = out(name, scheme);
		await page.screenshot({ path: file, clip, fullPage: !shot.viewportOnly });
		console.log(name, scheme, Math.round(fs.statSync(file).size / 1024) + ' KB', JSON.stringify(clip));
		await browser.close();
	}
}
