import { z } from 'zod/v4';

// A required number input
// Empty number inputs bind to null or undefined, which would otherwise surface zod's generic type error
// Other issues (e.g. from `.min()`) return undefined here, so they keep zod's own message
export const requiredNumber = (message = 'Required') =>
	z.number({
		error: (issue) => {
			if (issue.code !== 'invalid_type') return undefined;
			return issue.input == null ? message : 'Must be a number';
		}
	});

// Replaces zod's technical messages for common bounds (e.g. "Too small: expected number to be >=1") with plain ones
// Returning undefined keeps zod's default message for every other issue
export function configureZodMessages() {
	z.config({
		customError: (issue) => {
			if (issue.code === 'too_small') {
				if (issue.origin === 'number') return `Must be at least ${issue.minimum}`;
				if (issue.origin === 'string') {
					return issue.minimum === 1 ? 'Required' : `Must be at least ${issue.minimum} characters`;
				}
			}
			if (issue.code === 'too_big') {
				if (issue.origin === 'number') return `Must be at most ${issue.maximum}`;
				if (issue.origin === 'string') return `Must be at most ${issue.maximum} characters`;
			}
			return undefined;
		}
	});
}
