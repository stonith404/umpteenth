import type { Page } from '@playwright/test';

// Gives the page's browser a passkey authenticator that verifies its user right away, like a password manager unlocked with a fingerprint
// Chrome's virtual authenticator answers navigator.credentials without a prompt, so passkey flows run unattended
export async function addAuthenticator(page: Page) {
	const cdp = await page.context().newCDPSession(page);
	await cdp.send('WebAuthn.enable');
	const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
		options: {
			protocol: 'ctap2',
			transport: 'internal',
			hasResidentKey: true,
			hasUserVerification: true,
			isUserVerified: true,
			automaticPresenceSimulation: true
		}
	});

	// The passkeys the authenticator holds, e.g. to check that a sign-up stored one
	async function credentials() {
		return (await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials;
	}
	return { credentials };
}

// Creates the first account of a fresh instance through the login page, which makes it an instance admin
export async function setUpWithPasskey(page: Page, name = 'Ada Admin', email = 'ada@example.com') {
	await page.goto('/login');
	await page.getByLabel('Name').fill(name);
	await page.getByLabel('Email').fill(email);
	await page.getByRole('button', { name: 'Create account with a passkey' }).click();
	await page.waitForURL('/');
}

// Signs out through the account menu, which ends on the login page
export async function signOut(page: Page) {
	await page.getByRole('button', { name: 'Account menu' }).click();
	await page.getByRole('menuitem', { name: 'Sign out' }).click();
	await page.waitForURL('/login');
}
