import type { QueryOf } from '$lib/api/types';
import APIService from './api-service';

// Secret values are write-only: the API accepts them but never returns them
export default class SecretService extends APIService {
	list = (query?: QueryOf<'list-secrets'>) =>
		this.unwrap(this.api.GET('/api/secrets', { params: { query } }));

	listAll = () => this.listAllPages((page, pageSize) => this.list({ page, pageSize }));

	create = (name: string, value: string) =>
		this.unwrap(this.api.POST('/api/secrets', { body: { name, value } }));

	update = (id: string, value: string) =>
		this.unwrap(this.api.PUT('/api/secrets/{id}', { params: { path: { id } }, body: { value } }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/secrets/{id}', { params: { path: { id } } }));
}
