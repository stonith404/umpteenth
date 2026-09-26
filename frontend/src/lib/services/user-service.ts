import APIService from './api-service';

export default class UserService extends APIService {
	me = () => this.unwrap(this.api.GET('/api/users/me'));

	logout = () => this.unwrap(this.api.POST('/api/auth/logout'));
}
