import { ApiError } from '$lib/api/api-error';
import { getErrorMessage } from '$lib/utils/error-util';
import { configureZodMessages } from '$lib/utils/zod-util';
import type { HandleClientError } from '@sveltejs/kit';

configureZodMessages();

// Errors thrown by load functions end up on the error page, so API errors keep their status and a readable message
export const handleError: HandleClientError = ({ error, message, status }) => {
	if (error instanceof ApiError) {
		console.error(`API error ${error.status} ${error.code}: ${error.message}`, {
			requestId: error.requestId
		});
		return {
			message: getErrorMessage(error, message),
			status: error.status || status,
			requestId: error.requestId
		};
	}

	console.error(error);
	return { message, status };
};
