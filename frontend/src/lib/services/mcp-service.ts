import type { McpServerBody, QueryOf } from '#lib/api/types.js';
import APIService from './api-service';

export default class McpService extends APIService {
	list = (query?: QueryOf<'list-mcp-servers'>) =>
		this.unwrap(this.api.GET('/api/mcp-servers', { params: { query } }));

	listAll = () => this.listAllPages((page, pageSize) => this.list({ page, pageSize }));

	get = (id: string) =>
		this.unwrap(this.api.GET('/api/mcp-servers/{id}', { params: { path: { id } } }));

	create = (body: McpServerBody) => this.unwrap(this.api.POST('/api/mcp-servers', { body }));

	update = (id: string, body: McpServerBody) =>
		this.unwrap(this.api.PUT('/api/mcp-servers/{id}', { params: { path: { id } }, body }));

	delete = (id: string) =>
		this.unwrap(this.api.DELETE('/api/mcp-servers/{id}', { params: { path: { id } } }));

	// Connects to the server and caches its tools; stdio servers start a temporary sandbox, which can take up to a minute
	test = (id: string) =>
		this.unwrap(this.api.POST('/api/mcp-servers/{id}/test', { params: { path: { id } } }));

	// Starts an OAuth login and returns the authorization server URL the browser has to open
	// The authorization server sends the browser back to the MCP servers page with the outcome
	login = (id: string) =>
		this.unwrap(this.api.POST('/api/mcp-servers/{id}/oauth/login', { params: { path: { id } } }));

	logout = (id: string) =>
		this.unwrap(this.api.DELETE('/api/mcp-servers/{id}/oauth', { params: { path: { id } } }));
}
