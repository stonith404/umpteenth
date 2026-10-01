import {
	WebAuthnError,
	startAuthentication,
	startRegistration,
	type PublicKeyCredentialCreationOptionsJSON,
	type PublicKeyCredentialRequestOptionsJSON
} from '@simplewebauthn/browser';
import { z } from 'zod/v4';

// Asks the browser for a new passkey, or returns null when the person closes the prompt
// The backend hands out the options in their JSON form, which the API schema leaves untyped
export async function createPasskey(options: unknown) {
	try {
		return await startRegistration({
			optionsJSON: options as PublicKeyCredentialCreationOptionsJSON
		});
	} catch (e) {
		if (isCancelled(e)) return null;
		throw new PasskeyError(e);
	}
}

// Asks the browser to sign in with one of its passkeys, or returns null when the person closes the prompt
export async function usePasskey(options: unknown) {
	try {
		return await startAuthentication({
			optionsJSON: options as PublicKeyCredentialRequestOptionsJSON
		});
	} catch (e) {
		if (isCancelled(e)) return null;
		throw new PasskeyError(e);
	}
}

// Browsers report a closed prompt, a timeout and a refused permission all as NotAllowedError, and none of them needs an error message
function isCancelled(e: unknown) {
	const name =
		e instanceof WebAuthnError ? (e.cause as Error | undefined)?.name : (e as Error)?.name;
	return name === 'NotAllowedError' || name === 'AbortError';
}

// A failure of the browser's passkey prompt, such as an authenticator that holds a passkey of the account already
export class PasskeyError extends Error {
	constructor(cause: unknown) {
		super(passkeyErrorMessage(cause), { cause });
		this.name = 'PasskeyError';
	}
}

function passkeyErrorMessage(e: unknown) {
	if (e instanceof WebAuthnError) {
		switch (e.code) {
			case 'ERROR_AUTHENTICATOR_PREVIOUSLY_REGISTERED':
				return 'This device already holds a passkey for your account';
			case 'ERROR_INVALID_DOMAIN':
			case 'ERROR_INVALID_RP_ID':
				return "Passkeys don't work at this address, open Umpteenth at its configured URL";
		}
		return e.message;
	}
	if (e instanceof Error && e.name === 'SecurityError') {
		return "Passkeys don't work at this address, open Umpteenth at its configured URL";
	}
	return 'Your browser could not use the passkey';
}

// The name and optional email address of a passkey account, which no sign-in provider fills in
export const passkeyAccountSchema = z.object({
	name: z.string().trim().min(1).max(100),
	email: z.union([z.literal(''), z.email('Must be an email address').max(254)])
});
