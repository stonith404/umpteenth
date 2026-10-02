import { expect, type APIRequestContext } from '@playwright/test';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { crc32, gzipSync } from 'node:zlib';
import { receiverHost } from './receiver.util';

// One file of a test zip, executable when mode says so
export type ZipEntry = { path: string; content: string; mode?: number };

// The fields of GET /api/skills/{id} that specs check
export type Skill = {
	id: string;
	name: string;
	description: string;
	contentHash: string;
	files: { path: string; size: number; executable: boolean }[];
	fileCount: number;
	jobCount: number;
};

// Builds an uncompressed zip with Unix modes, which is all a skill upload needs
export function makeZip(entries: ZipEntry[]): Buffer {
	const locals: Buffer[] = [];
	const centrals: Buffer[] = [];
	let offset = 0;
	for (const entry of entries) {
		const name = Buffer.from(entry.path);
		const data = Buffer.from(entry.content);
		const crc = crc32(data);

		// The local header, followed by the name and the stored data
		const local = Buffer.alloc(30);
		local.writeUInt32LE(0x04034b50, 0);
		local.writeUInt16LE(20, 4);
		local.writeUInt16LE(0x21, 12);
		local.writeUInt32LE(crc, 14);
		local.writeUInt32LE(data.length, 18);
		local.writeUInt32LE(data.length, 22);
		local.writeUInt16LE(name.length, 26);
		locals.push(local, name, data);

		// The central directory entry, made on Unix so its external attributes carry the mode
		const central = Buffer.alloc(46);
		central.writeUInt32LE(0x02014b50, 0);
		central.writeUInt16LE((3 << 8) | 20, 4);
		central.writeUInt16LE(20, 6);
		central.writeUInt16LE(0x21, 14);
		central.writeUInt32LE(crc, 16);
		central.writeUInt32LE(data.length, 20);
		central.writeUInt32LE(data.length, 24);
		central.writeUInt16LE(name.length, 28);
		central.writeUInt32LE(((0o100000 | (entry.mode ?? 0o644)) << 16) >>> 0, 38);
		central.writeUInt32LE(offset, 42);
		centrals.push(central, name);
		offset += local.length + name.length + data.length;
	}

	const directory = Buffer.concat(centrals);
	const end = Buffer.alloc(22);
	end.writeUInt32LE(0x06054b50, 0);
	end.writeUInt16LE(entries.length, 8);
	end.writeUInt16LE(entries.length, 10);
	end.writeUInt32LE(directory.length, 12);
	end.writeUInt32LE(offset, 16);
	return Buffer.concat([...locals, directory, end]);
}

// A skill folder as people zip it, with SKILL.md, a script and a reference inside one top-level folder
export function skillZip(name: string, description: string, body = 'Run scripts/hello.sh.') {
	return makeZip([
		{
			path: `${name}/SKILL.md`,
			content: `---\nname: ${name}\ndescription: ${description}\n---\n# ${name}\n\n${body}\n`
		},
		{ path: `${name}/scripts/hello.sh`, content: '#!/bin/sh\necho "Hello, $1!"\n', mode: 0o755 },
		{ path: `${name}/reference.md`, content: '# Languages\n\n- German: Hallo\n' }
	]);
}

// Uploads a skill through the API
export async function uploadSkill(request: APIRequestContext, zip: Buffer) {
	const response = await request.post('/api/skills', {
		headers: { 'Content-Type': 'application/zip' },
		data: zip
	});
	expect(response.ok(), await response.text()).toBeTruthy();
	return (await response.json()) as Skill;
}

// Attaches skills to a job, replacing the ones it had
export async function attachSkills(request: APIRequestContext, jobId: string, skillIds: string[]) {
	const response = await request.put(`/api/jobs/${jobId}/skills`, {
		data: skillIds.map((skillId) => ({ skillId }))
	});
	expect(response.ok()).toBeTruthy();
}

// The names of the job's skills
export async function jobSkills(request: APIRequestContext, jobId: string) {
	const response = await request.get(`/api/jobs/${jobId}/skills`);
	expect(response.ok()).toBeTruthy();
	return ((await response.json()) as { skillName: string }[]).map((s) => s.skillName);
}

// Builds a repository tarball the way GitHub serves it, with every path inside one folder named after the repository and ref
export function repoTarball(entries: ZipEntry[]): Buffer {
	const blocks: Buffer[] = [];
	for (const entry of entries) {
		const data = Buffer.from(entry.content);
		const header = Buffer.alloc(512);
		const field = (value: string, offset: number, length: number) =>
			header.write(value, offset, length);
		const octal = (value: number, length: number) =>
			value.toString(8).padStart(length - 1, '0') + '\0';
		field(`skills-HEAD/${entry.path}`, 0, 100);
		field(octal(entry.mode ?? 0o644, 8), 100, 8);
		field(octal(0, 8), 108, 8);
		field(octal(0, 8), 116, 8);
		field(octal(data.length, 12), 124, 12);
		field(octal(0, 12), 136, 12);
		field('0', 156, 1);
		field('ustar\0', 257, 6);
		field('00', 263, 2);

		// The checksum counts its own field as spaces
		field('        ', 148, 8);
		let sum = 0;
		for (const byte of header) sum += byte;
		field(sum.toString(8).padStart(6, '0') + '\0 ', 148, 8);

		blocks.push(header, data, Buffer.alloc((512 - (data.length % 512)) % 512));
	}
	blocks.push(Buffer.alloc(1024));
	return gzipSync(Buffer.concat(blocks));
}

// Serves one repository's tarball like GitHub's archive host, at /<owner>/<repo>/tar.gz/HEAD, and 404 for anything else
export async function startFakeGitHub(
	request: APIRequestContext,
	owner: string,
	repo: string,
	tarball: Buffer
) {
	const server = http.createServer((req, res) => {
		if (req.url !== `/${owner}/${repo}/tar.gz/HEAD`) {
			res.writeHead(404).end();
			return;
		}
		res.writeHead(200, { 'Content-Type': 'application/x-gzip' }).end(tarball);
	});
	await new Promise<void>((resolve) => server.listen(0, resolve));

	// The backend downloads GitHub folders from the fake until the next reset
	const url = `http://${receiverHost}:${(server.address() as AddressInfo).port}`;
	const response = await request.post('/api/test/skills/github-archives', { data: { url } });
	expect(response.ok()).toBeTruthy();
	return { close: () => server.close() };
}
