import type { ClientInit, HandleClientError } from '@sveltejs/kit/hooks';
import { ApiError } from '#lib/api/api-error.js';
import WorkspaceService from '#lib/services/workspace-service.js';
import { getErrorMessage, isSessionError } from '#lib/utils/error-util.js';
import { tryCatch } from '#lib/utils/try-catch-util.js';
import { configureZodMessages } from '#lib/utils/zod-util.js';

export const init: ClientInit = async () => {
	configureZodMessages();
	await followWorkspaceLink();
};

// Links from notifications name the workspace of the run they point at, and the session moves there before anything loads
// A workspace the user can't open leaves the session where it is, and the page then says the run wasn't found
async function followWorkspaceLink() {
	const url = new URL(window.location.href);
	const workspaceId = url.searchParams.get('workspace');
	if (workspaceId === null) return;

	// Without a session the link keeps its workspace, so the login redirect brings it back and the switch happens after signing in
	const result = await tryCatch(new WorkspaceService().switchTo(workspaceId));
	if (isSessionError(result.error)) return;

	url.searchParams.delete('workspace');
	history.replaceState(history.state, '', url.pathname + url.search + url.hash);
}

// Errors thrown by load functions end up on the error page, so API errors keep their status, code and a readable message
// The code lets the error page tell a lost session, which goes to the login page, from a real failure
export const handleError: HandleClientError = ({ kind, error }) => {
	// Errors raised with `error(...)` and SvelteKit's own, such as a 404, are already safe to show as they are
	if (kind !== 'unknown') return;

	if (error instanceof ApiError) {
		console.error(`API error ${error.status} ${error.code}: ${error.message}`, {
			requestId: error.requestId
		});
		return {
			message: getErrorMessage(error),
			// Status 0 marks a request that never got a response, which the error page treats like a server error
			status: error.status || 500,
			code: error.code,
			requestId: error.requestId
		};
	}

	console.error(error);
};
