import type { RequestBodyOf } from '$lib/api/types';
import { createPasskey, usePasskey } from '$lib/utils/passkey-util';
import APIService from './api-service';

export default class UserService extends APIService {
	me = () => this.unwrap(this.api.GET('/api/users/me'));

	// The sign-in providers of the login page, primary first, with passkeys last when they are turned on
	loginProviders = async () => (await this.unwrap(this.api.GET('/api/auth/providers'))) ?? [];

	// Whether the instance has no users yet, so the login page creates the first account instead
	setup = () => this.unwrap(this.api.GET('/api/auth/setup'));

	logout = () => this.unwrap(this.api.POST('/api/auth/logout'));

	// Signs in with any passkey of the instance and returns where to continue
	// Resolves to null when the person closes the browser's passkey prompt
	signInWithPasskey = async (redirect: string | null) => {
		const { options } = await this.unwrap(
			this.api.POST('/api/auth/passkey/sign-in/options', {
				body: { redirect: redirect ?? undefined }
			})
		);
		const credential = await usePasskey(options);
		if (!credential) return null;
		return this.unwrap(this.api.POST('/api/auth/passkey/sign-in', { body: { credential } }));
	};

	// Creates a passkey account, which only works for the first account and through an invite link, and returns where to continue
	// Resolves to null when the person closes the browser's passkey prompt
	signUpWithPasskey = async (body: RequestBodyOf<'begin-passkey-sign-up'>) => {
		const { options } = await this.unwrap(
			this.api.POST('/api/auth/passkey/sign-up/options', { body })
		);
		const credential = await createPasskey(options);
		if (!credential) return null;
		return this.unwrap(this.api.POST('/api/auth/passkey/sign-up', { body: { credential } }));
	};

	useSignInLink = (token: string) =>
		this.unwrap(this.api.POST('/api/auth/sign-in-link', { body: { token } }));

	updateProfile = (body: RequestBodyOf<'update-my-profile'>) =>
		this.unwrap(this.api.PATCH('/api/users/me', { body }));

	listPasskeys = async () => (await this.unwrap(this.api.GET('/api/users/me/passkeys'))) ?? [];

	// Adds a passkey to the signed-in account, or resolves to null when the person closes the browser's passkey prompt
	addPasskey = async () => {
		const { options } = await this.unwrap(this.api.POST('/api/users/me/passkeys/options'));
		const credential = await createPasskey(options);
		if (!credential) return null;
		return this.unwrap(this.api.POST('/api/users/me/passkeys', { body: { credential } }));
	};

	renamePasskey = (id: string, name: string) =>
		this.unwrap(
			this.api.PATCH('/api/users/me/passkeys/{id}', { params: { path: { id } }, body: { name } })
		);

	deletePasskey = (id: string) =>
		this.unwrap(this.api.DELETE('/api/users/me/passkeys/{id}', { params: { path: { id } } }));
}
