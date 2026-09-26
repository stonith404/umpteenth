import type {
	Model,
	ModelCreate,
	ModelUpdate,
	ProviderCreate,
	ProviderKind,
	ProviderUpdate,
	QueryOf
} from '$lib/api/types';
import APIService from './api-service';

// The largest page the list endpoints return, used where a picker needs every model at once
const MAX_PAGE_SIZE = 100;

export default class ProviderService extends APIService {
	list = (query?: QueryOf<'list-providers'>) =>
		this.unwrap(this.api.GET('/api/providers', { params: { query } }));

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
	listAllModels = async (query?: Pick<QueryOf<'list-models'>, 'provider' | 'status'>) => {
		const models: Model[] = [];
		for (let page = 1; ; page++) {
			const result = await this.listModels({
				...query,
				page,
				pageSize: MAX_PAGE_SIZE,
				sort: 'provider,model'
			});
			models.push(...(result.items ?? []));
			if (models.length >= result.total || !result.items?.length) return models;
		}
	};

	createModel = (body: ModelCreate) => this.unwrap(this.api.POST('/api/models', { body }));

	updateModel = (id: string, body: ModelUpdate) =>
		this.unwrap(this.api.PATCH('/api/models/{id}', { params: { path: { id } }, body }));

	deleteModel = (id: string) =>
		this.unwrap(this.api.DELETE('/api/models/{id}', { params: { path: { id } } }));

	// The bundled models of a provider kind, used to prefill the model form
	catalog = async (kind: Exclude<ProviderKind, 'fake'>) =>
		(await this.unwrap(this.api.GET('/api/catalog/models', { params: { query: { kind } } }))) ?? [];
}
