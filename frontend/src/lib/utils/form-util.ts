import { isApiError } from '$lib/api/api-error';
import { apiErrorToast } from '$lib/utils/error-util';
import { reactiveState } from '$lib/utils/reactive-state.svelte';
import { tryCatch } from '$lib/utils/try-catch-util';
import { toast } from 'svelte-sonner';
import { get, writable } from 'svelte/store';
import { z } from 'zod/v4';

export type FormInput<T> = {
	value: T;
	error: string | null;
	required: boolean;
};

type FormInputs<T> = {
	[K in keyof T]: FormInput<T[K]>;
};

export function createForm<T extends z.ZodType<any, any>>(schema: T, initialValues: z.infer<T>) {
	// The saved values the inputs are compared against, and whether a save is running
	// Both are reactive, so a Save button reading `isDirty()` or `saving` follows edits, saves and resets
	// The baseline is a private deep copy, so editing an object or array field can't change what it is compared with
	const state = reactiveState({ baseline: deepCopy(initialValues), saving: false });

	// The inputs are deeply reactive, so field components can update them in place and derived state still re-evaluates
	const inputsStore = writable<FormInputs<z.infer<T>>>(reactiveState(initializeInputs()));

	function initializeInputs(): FormInputs<z.infer<T>> {
		const inputs = {} as FormInputs<z.infer<T>>;
		const shape =
			schema instanceof z.ZodObject ? (schema.shape as Record<string, z.ZodTypeAny>) : {};

		for (const key of Object.keys(initialValues) as (keyof z.infer<T> & string)[]) {
			const fieldSchema = shape[key];
			inputs[key] = {
				value: deepCopy(initialValues[key]),
				error: null,
				required: fieldSchema ? isRequired(fieldSchema) : false
			};
		}
		return inputs;
	}

	function validate() {
		const inputs = get(inputsStore);
		const values = Object.fromEntries(
			Object.entries(inputs).map(([key, input]) => [key, trimValue(input.value)])
		);
		const result = schema.safeParse(values);

		// Show the first validation message of each field next to it
		for (const input of Object.keys(inputs)) {
			inputs[input as keyof z.infer<T>].error = result.success
				? null
				: (result.error.issues.find((e) => e.path[0] === input)?.message ?? null);
		}

		// Write the parsed values back, so transforms such as trimming are visible in the inputs
		if (result.success) {
			for (const key in result.data) {
				if (Object.hasOwn(inputs, key)) inputs[key as keyof z.infer<T>].value = result.data[key];
			}
		}

		inputsStore.set(inputs);
		return result.success ? result.data : null;
	}

	function isDirty() {
		const inputs = get(inputsStore);
		return Object.keys(inputs).some(
			(key) =>
				!deepEqual(inputs[key as keyof z.infer<T>].value, state.baseline[key as keyof z.infer<T>])
		);
	}

	// Validates and saves the form, then makes the saved values the baseline, so its Save button disables again
	// A failed save keeps the edits and shows the backend's field errors next to their inputs, or the error in a toast
	async function submit(save: (values: z.infer<T>) => Promise<unknown>) {
		const values = validate();
		if (!values) return;

		state.saving = true;
		const result = await tryCatch(save(values));
		state.saving = false;

		if (result.error) {
			const shownInline =
				isApiError(result.error, 'validation_failed') && setErrors(result.error.fields);
			if (!shownInline) apiErrorToast(result.error, 'Failed to save the changes');
			return;
		}
		state.baseline = { ...state.baseline, ...deepCopy(values) };
		toast.success('Changes saved');
	}

	function reset() {
		inputsStore.update((inputs) => {
			for (const input of Object.keys(inputs)) {
				const current = inputs[input as keyof z.infer<T>];
				inputs[input as keyof z.infer<T>] = {
					...current,
					value: deepCopy(state.baseline[input as keyof z.infer<T>]),
					error: null
				};
			}
			return inputs;
		});
	}

	// Shows server-side validation errors (apperror field errors) next to the matching inputs, and reports whether every error found one
	// Huma reports locations like `body.defaultLimits.maxTurns`, so the full path is tried first and the last segment second, which matches forms that flatten nested objects
	// An item of a list, such as `allowedDomains[2]`, belongs to the input that holds the whole list
	function setErrors(fieldErrors: { field: string; message: string }[]) {
		let allShown = fieldErrors.length > 0;
		inputsStore.update((inputs) => {
			for (const fieldError of fieldErrors) {
				const path = fieldError.field.replace(/^body\./, '').replace(/\[\d+\]$/, '');
				const key = (path in inputs ? path : path.split('.').pop()) as keyof z.infer<T>;
				if (key && inputs[key]) inputs[key].error = fieldError.message;
				else allShown = false;
			}
			return inputs;
		});
		return allShown;
	}

	function trimValue(value: any) {
		if (typeof value === 'string') {
			value = value.trim();
		} else if (Array.isArray(value)) {
			value = value.map((item: any) => {
				if (typeof item === 'string') {
					return item.trim();
				}
				return item;
			});
		}
		return value;
	}

	function isRequired(fieldSchema: z.ZodTypeAny): boolean {
		// A string that accepts an empty value is optional
		if (fieldSchema instanceof z.ZodString) {
			return fieldSchema.minLength !== null && fieldSchema.minLength > 0;
		}

		// A union that accepts undefined or an empty string, like `z.url().or(z.literal(''))`, can be left blank
		if (fieldSchema instanceof z.ZodUnion) {
			return !fieldSchema.def.options.some((o: any) => {
				return (
					o.def.type === 'optional' || (o instanceof z.ZodLiteral && o.def.values.includes(''))
				);
			});
		}

		// Pipes such as z.preprocess are required when their output is
		if (fieldSchema instanceof z.ZodPipe) {
			return isRequired(fieldSchema.def.out as z.ZodTypeAny);
		}

		if (fieldSchema instanceof z.ZodOptional || fieldSchema instanceof z.ZodDefault) {
			return false;
		}

		return true;
	}

	return {
		inputs: inputsStore,
		validate,
		setErrors,
		reset,
		isDirty,
		submit,
		get saving() {
			return state.saving;
		}
	};
}

// Copies a value including its nested objects and arrays
// Unlike `structuredClone` it accepts `$state` proxies, which the forms' deeply reactive inputs are
function deepCopy<T>(value: T): T {
	if (Array.isArray(value)) return value.map(deepCopy) as T;
	if (value instanceof Date) return new Date(value) as T;
	if (value !== null && typeof value === 'object') {
		return Object.fromEntries(
			Object.entries(value).map(([key, item]) => [key, deepCopy(item)])
		) as T;
	}
	return value;
}

// Compares two values structurally, including nested objects and arrays
function deepEqual(a: unknown, b: unknown): boolean {
	if (Object.is(a, b)) return true;
	if (a instanceof Date && b instanceof Date) return a.getTime() === b.getTime();
	if (typeof a !== typeof b || a === null || b === null) return false;

	if (Array.isArray(a) || Array.isArray(b)) {
		if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
		return a.every((item, i) => deepEqual(item, b[i]));
	}

	if (typeof a === 'object' && typeof b === 'object') {
		const aKeys = Object.keys(a as object);
		const bKeys = Object.keys(b as object);
		if (aKeys.length !== bKeys.length) return false;
		return aKeys.every((key) =>
			deepEqual((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key])
		);
	}

	return false;
}
