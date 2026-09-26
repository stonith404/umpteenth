// Shared Playwright helpers for the docs screenshots of the throwaway stack on :18086
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// The repository root is five levels up, at the top of .claude/skills/update-docs/scripts/screenshots
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../..');
const require = createRequire(path.join(ROOT, 'tests/package.json'));
const { chromium } = require('@playwright/test');

export const BASE = 'http://localhost:18086';
export const OUT = path.join(ROOT, 'docs/src/assets/screens');
export const HERE = path.dirname(fileURLToPath(import.meta.url));
export const state = JSON.parse(fs.readFileSync(path.join(HERE, 'state.json'), 'utf8'));

fs.mkdirSync(OUT, { recursive: true });

export async function open(scheme, { dpr = 2, width = 1440, height = 900 } = {}) {
	const browser = await chromium.launch();
	const context = await browser.newContext({
		baseURL: BASE,
		viewport: { width, height },
		deviceScaleFactor: dpr,
		colorScheme: scheme,
		reducedMotion: 'reduce'
	});
	const res = await context.request.post('/api/test/session');
	if (!res.ok()) throw new Error(`session failed: ${res.status()}`);
	const page = await context.newPage();
	return { browser, context, page };
}

// Replaces the test user's name, initials and email, the local sandbox image and the local origin, in visible text
export async function scrub(page) {
	await page.evaluate(() => {
		const swaps = [
			['E2E User', 'Ada Park'],
			['umpteenth-docs-sandbox:test', 'ghcr.io/stonith404/umpteenth-sandbox:latest'],
			['e2e@example.com', 'ada@acme.dev'],
			['http://localhost:18086', 'https://umpteenth.example.com'],
			['localhost:18086', 'umpteenth.example.com']
		];
		const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
		const nodes = [];
		while (walker.nextNode()) nodes.push(walker.currentNode);
		for (const node of nodes) {
			let text = node.nodeValue;
			for (const [from, to] of swaps) text = text.split(from).join(to);
			if (text !== node.nodeValue) node.nodeValue = text;
		}
		for (const el of document.querySelectorAll('[data-slot="avatar-fallback"]')) {
			if (el.textContent.trim() === 'EU') el.textContent = 'AP';
		}
		for (const el of document.querySelectorAll('[title]')) {
			for (const [from, to] of swaps) el.title = el.title.split(from).join(to);
		}
	});
}

// Leaves the page in a still state: no animation, focus ring, toast or tooltip
export async function settle(page, { wait = 600 } = {}) {
	await page.waitForLoadState('networkidle').catch(() => {});
	await page.waitForTimeout(wait);
	await page.evaluate(() => {
		document.getAnimations().forEach((a) => {
			try {
				a.finish();
			} catch {
				// Infinite animations can't finish
			}
		});
		const active = document.activeElement;
		if (active && 'setSelectionRange' in active) {
			try {
				active.setSelectionRange(0, 0);
			} catch {
				// Some input types have no selection
			}
		}
		if (active && active !== document.body) active.blur();
		document.getSelection()?.removeAllRanges();
		document.querySelectorAll('[data-sonner-toast]').forEach((t) => t.remove());
		document.querySelectorAll('[data-slot="tooltip-content"]').forEach((t) => t.remove());
	});
	await page.mouse.move(1, 899);
	await scrub(page);
	await page.waitForTimeout(150);
}

// A bounding box that covers several elements, for clip screenshots
export async function unionBox(page, selectors, pad = 0) {
	const boxes = [];
	for (const sel of selectors) {
		const loc = typeof sel === 'string' ? page.locator(sel).first() : sel;
		const box = await loc.boundingBox();
		if (!box) throw new Error(`no box for ${sel}`);
		boxes.push(box);
	}
	const x = Math.min(...boxes.map((b) => b.x)) - pad;
	const y = Math.min(...boxes.map((b) => b.y)) - pad;
	const right = Math.max(...boxes.map((b) => b.x + b.width)) + pad;
	const bottom = Math.max(...boxes.map((b) => b.y + b.height)) + pad;
	return { x: Math.max(0, x), y: Math.max(0, y), width: right - Math.max(0, x), height: bottom - Math.max(0, y) };
}

export function out(name, scheme) {
	return path.join(OUT, `${name}-${scheme}.png`);
}

export const content = '[data-slot="sidebar-inset"] > main';
