import APIService from './api-service';

export default class UserService extends APIService {
	me = () => this.unwrap(this.api.GET('/api/users/me'));

	// The sign-in providers of the login page, primary first
	loginProviders = async () => (await this.unwrap(this.api.GET('/api/auth/providers'))) ?? [];

	logout = () => this.unwrap(this.api.POST('/api/auth/logout'));
}
