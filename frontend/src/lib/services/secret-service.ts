import type { QueryOf } from '$lib/api/types';
import APIService from './api-service';

// The largest page the list endpoints return, used where a picker needs every secret at once
const MAX_PAGE_SIZE = 100;

// Secret values are write-only: the API accepts them but never returns them
export default class SecretService extends APIService {
	list = (query?: QueryOf<'list-secrets'>) =>
		this.unwrap(this.api.GET('/api/secrets', { params: { query } }));

	listAll = async () => (await this.list({ pageSize: MAX_PAGE_SIZE })).items ?? [];

	create = (name: string, value: string) =>
		this.unwrap(this.api.POST('/api/secrets', { body: { name, value } }));

	update = (id: string, value: string) =>
		this.unwrap(this.api.PUT('/api/secrets/{id}', { params: { path: { id } }, body: { value } }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/secrets/{id}', { params: { path: { id } } }));
}
