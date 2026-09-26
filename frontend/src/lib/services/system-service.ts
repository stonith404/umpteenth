import APIService from './api-service';

export default class SystemService extends APIService {
	info = () => this.unwrap(this.api.GET('/api/system/info'));
}
