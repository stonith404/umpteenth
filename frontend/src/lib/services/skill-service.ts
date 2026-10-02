import type { QueryOf } from '#lib/api/types.js';
import APIService from './api-service';

// The largest zip the backend takes, so the upload dialog can refuse a bigger one before sending it
export const MAX_SKILL_UPLOAD_BYTES = 8 << 20;

export default class SkillService extends APIService {
	list = (query?: QueryOf<'list-skills'>) =>
		this.unwrap(this.api.GET('/api/skills', { params: { query } }));

	listAll = () => this.listAllPages((page, pageSize) => this.list({ page, pageSize }));

	get = (id: string) => this.unwrap(this.api.GET('/api/skills/{id}', { params: { path: { id } } }));

	// The zip is sent as the raw body, with the type set by hand since browsers give .skill files none
	create = (file: File) =>
		this.unwrap(
			this.api.POST('/api/skills', {
				body: file as unknown as string,
				bodySerializer: (body) => body,
				headers: { 'Content-Type': 'application/zip' }
			})
		);

	// A new version keeps the skill's name, which jobs and their scripts refer to
	replace = (id: string, file: File) =>
		this.unwrap(
			this.api.PUT('/api/skills/{id}', {
				params: { path: { id } },
				body: file as unknown as string,
				bodySerializer: (body) => body,
				headers: { 'Content-Type': 'application/zip' }
			})
		);

	// Downloads a skill from a GitHub folder or a zip link, or the picked skills of a GitHub folder of several, which takes a while for a large repository
	importFromLink = async (url: string, paths?: string[]) =>
		(await this.unwrap(this.api.POST('/api/skills/import', { body: { url, paths } }))).skills ?? [];

	// Lists the skills in a GitHub folder, so some of them can be picked
	previewLink = async (url: string) =>
		(await this.unwrap(this.api.POST('/api/skills/import/preview', { body: { url } }))).skills ??
		[];

	// Takes a new version from a link, by default the one the skill was imported from
	reimport = (id: string, url?: string) =>
		this.unwrap(
			this.api.POST('/api/skills/{id}/import', { params: { path: { id } }, body: { url } })
		);

	// Deletes several skills at once, reporting the ones that were already gone as skipped
	deleteMany = (ids: string[]) =>
		this.unwrap(this.api.POST('/api/skills/delete', { body: { ids } }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/skills/{id}', { params: { path: { id } } }));

	// Reads one file of a skill as text, for showing it in the detail sheet
	readFile = (id: string, path: string) =>
		this.unwrap(
			this.api.GET('/api/skills/{id}/file', {
				params: { path: { id }, query: { path } },
				parseAs: 'text'
			})
		) as Promise<string>;

	// Download links, used directly as an `href` since the endpoints stream the file
	fileUrl = (id: string, path: string) =>
		`/api/skills/${encodeURIComponent(id)}/file?path=${encodeURIComponent(path)}`;

	downloadUrl = (id: string) => `/api/skills/${encodeURIComponent(id)}/download`;
}
