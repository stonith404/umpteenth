import type { WorkspaceSettingsUpdate } from '$lib/api/types';
import APIService from './api-service';

export default class SettingsService extends APIService {
	get = () => this.unwrap(this.api.GET('/api/settings'));

	update = (body: WorkspaceSettingsUpdate) =>
		this.unwrap(this.api.PATCH('/api/settings', { body }));

	// Sends a sample message to the saved webhook right away
	testNotification = () => this.unwrap(this.api.POST('/api/settings/notifications/test'));
}
