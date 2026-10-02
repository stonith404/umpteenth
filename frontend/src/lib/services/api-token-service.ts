import type { ApiTokenCreate, QueryOf } from '#lib/api/types.js';
import APIService from './api-service';

export default class ApiTokenService extends APIService {
	list = (query?: QueryOf<'list-api-tokens'>) =>
		this.unwrap(this.api.GET('/api/tokens', { params: { query } }));

	create = (body: ApiTokenCreate) => this.unwrap(this.api.POST('/api/tokens', { body }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/tokens/{id}', { params: { path: { id } } }));
}
