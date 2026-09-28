import type {
	ModelCreate,
	ModelUpdate,
	ProviderCreate,
	ProviderKind,
	ProviderUpdate,
	QueryOf
} from '$lib/api/types';
import APIService from './api-service';

export default class ProviderService extends APIService {
	list = (query?: QueryOf<'list-providers'>) =>
		this.unwrap(this.api.GET('/api/providers', { params: { query } }));

	listAll = () =>
		this.listAllPages((page, pageSize) => this.list({ page, pageSize, sort: 'name' }));

	create = (body: ProviderCreate) => this.unwrap(this.api.POST('/api/providers', { body }));

	update = (id: string, body: ProviderUpdate) =>
		this.unwrap(this.api.PATCH('/api/providers/{id}', { params: { path: { id } }, body }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/providers/{id}', { params: { path: { id } } }));

	// Sends a tiny prompt through one of the provider's models to check credentials and connectivity
	test = (id: string, modelId: string) =>
		this.unwrap(
			this.api.POST('/api/providers/{id}/test', { params: { path: { id } }, body: { modelId } })
		);

	// Reads the provider's model list from its source now, instead of at the next refresh
	sync = (id: string) =>
		this.unwrap(this.api.POST('/api/providers/{id}/sync', { params: { path: { id } } }));

	setModelsEnabled = (id: string, enabled: boolean) =>
		this.unwrap(
			this.api.PUT('/api/providers/{id}/models/enabled', {
				params: { path: { id } },
				body: { enabled }
			})
		);

	listModels = (query?: QueryOf<'list-models'>) =>
		this.unwrap(this.api.GET('/api/models', { params: { query } }));

	// Loads every model for pickers, page by page, since a server such as OpenRouter lists hundreds
	listAllModels = (query?: Pick<QueryOf<'list-models'>, 'provider' | 'status'>) =>
		this.listAllPages((page, pageSize) =>
			this.listModels({ ...query, page, pageSize, sort: 'provider,model' })
		);

	createModel = (body: ModelCreate) => this.unwrap(this.api.POST('/api/models', { body }));

	updateModel = (id: string, body: ModelUpdate) =>
		this.unwrap(this.api.PATCH('/api/models/{id}', { params: { path: { id } }, body }));

	deleteModel = (id: string) =>
		this.unwrap(this.api.DELETE('/api/models/{id}', { params: { path: { id } } }));

	// The bundled models of a provider kind, used to prefill the model form
	catalog = async (kind: Exclude<ProviderKind, 'fake'>) =>
		(await this.unwrap(this.api.GET('/api/catalog/models', { params: { query: { kind } } }))) ?? [];
}
