// Generates src/generated/openapi.json from the backend, so the API reference always matches the code it documents
// Runs before every docs build and dev server, see package.json
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const docs = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const backend = path.resolve(docs, '../backend');
const out = path.join(docs, 'src/generated/openapi.json');

// The server binary prints its own spec, and exclude_frontend skips embedding the UI, which the spec doesn't need
let doc;
try {
	const spec = execFileSync('go', ['run', '-tags', 'exclude_frontend', './cmd/umpteenth', 'openapi'], {
		cwd: backend,
		encoding: 'utf8',
		stdio: ['ignore', 'pipe', 'inherit']
	});
	doc = JSON.parse(spec);
} catch (err) {
	// Without Go, a spec from an earlier run still lets the site build, but a release build must never ship a stale one
	if (fs.existsSync(out) && !process.env.CI) {
		console.warn(`openapi: couldn't generate the spec (${err.message.split('\n')[0]}), keeping the existing ${path.relative(docs, out)}`);
		process.exit(0);
	}
	console.error(`openapi: couldn't generate the spec, which needs Go to run the backend: ${err.message}`);
	process.exit(1);
}

// The docs fill in what the backend leaves out, and anything the backend sets later wins
// Operations without a summary would show their raw ID as the heading, so "run-job" becomes "Run job"
for (const item of Object.values(doc.paths ?? {})) {
	for (const op of Object.values(item)) {
		if (op && typeof op === 'object' && op.operationId && !op.summary) {
			const words = op.operationId.replaceAll('-', ' ');
			op.summary = words.charAt(0).toUpperCase() + words.slice(1);
		}
	}
}

// The copy the docs serve at /openapi.json has no instance of its own, so the example instance URL the rest of the docs use keeps clients from resolving paths against the docs site
if (!doc.servers?.length) doc.servers = [{ url: 'https://umpteenth.example.com' }];

// Writing to a temporary file first keeps a half-written spec from ever reaching the build
fs.mkdirSync(path.dirname(out), { recursive: true });
fs.writeFileSync(`${out}.tmp`, JSON.stringify(doc, null, 2));
fs.renameSync(`${out}.tmp`, out);
console.log(`openapi: wrote ${path.relative(docs, out)}`);
