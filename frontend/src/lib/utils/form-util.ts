import { reactiveState } from '$lib/utils/reactive-state.svelte';
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
	// The values the inputs are compared against and restored to
	// It is a private deep copy, so editing an object or array field can't change what it is compared with
	let baseline: z.infer<T> = deepCopy(initialValues);

	// The inputs are deeply reactive, so field components can update them in place and derived state such as unsaved-changes tracking still re-evaluates
	const inputsStore = writable<FormInputs<z.infer<T>>>(reactiveState(initializeInputs()));
	const errorsStore = writable<z.ZodError<any> | undefined>();

	function initializeInputs(): FormInputs<z.infer<T>> {
		const inputs: FormInputs<z.infer<T>> = {} as FormInputs<z.infer<T>>;

		const shape =
			schema instanceof z.ZodObject ? (schema.shape as Record<string, z.ZodTypeAny>) : {};

		for (const key in baseline) {
			if (Object.prototype.hasOwnProperty.call(baseline, key)) {
				const fieldSchema = shape[key];

				inputs[key as keyof z.infer<T>] = {
					value: deepCopy(baseline[key as keyof z.infer<T>]),
					error: null,
					required: fieldSchema ? isRequired(fieldSchema) : false
				};
			}
		}
		return inputs;
	}

	function validate() {
		const inputs = get(inputsStore);
		const values = Object.fromEntries(
			Object.entries(inputs).map(([key, input]) => [key, trimValue(input.value)])
		);
		const result = schema.safeParse(values);
		errorsStore.set(result.error);

		// Show the first validation message of each field next to it
		for (const input of Object.keys(inputs)) {
			inputs[input as keyof z.infer<T>].error = result.success
				? null
				: (result.error.issues.find((e) => e.path[0] === input)?.message ?? null);
		}

		// Write the parsed values back, so transforms such as trimming are visible in the inputs
		if (result.success) {
			for (const key in result.data) {
				if (Object.prototype.hasOwnProperty.call(inputs, key)) {
					inputs[key as keyof z.infer<T>].value = result.data[key];
				}
			}
		}

		inputsStore.set(inputs);
		return result.success ? result.data : null;
	}

	function data() {
		const inputs = get(inputsStore);

		const values = Object.fromEntries(
			Object.entries(inputs).map(([key, input]) => {
				input.value = trimValue(input.value);
				return [key, input.value];
			})
		) as z.infer<T>;

		return values;
	}

	function isDirty() {
		const inputs = get(inputsStore);
		return Object.keys(inputs).some(
			(key) => !deepEqual(inputs[key as keyof z.infer<T>].value, baseline[key as keyof z.infer<T>])
		);
	}

	// Moves the baseline forward, e.g. after a successful save whose data isn't reflected back into a prop that would rebuild the form
	function commit(values: z.infer<T>) {
		baseline = { ...baseline, ...deepCopy(values) };

		// Notify subscribers, so anything comparing the inputs against the baseline is re-evaluated
		inputsStore.update((inputs) => inputs);
	}

	// A copy of the values the inputs are compared against, e.g. to send only the fields the user changed
	function getBaseline(): z.infer<T> {
		return deepCopy(baseline);
	}

	function clearErrors() {
		errorsStore.set(undefined);
		inputsStore.update((inputs) => {
			for (const input of Object.values(inputs) as FormInput<unknown>[]) input.error = null;
			return inputs;
		});
	}

	function reset() {
		inputsStore.update((inputs) => {
			for (const input of Object.keys(inputs)) {
				const current = inputs[input as keyof z.infer<T>];
				inputs[input as keyof z.infer<T>] = {
					...current,
					value: deepCopy(baseline[input as keyof z.infer<T>]),
					error: null
				};
			}
			return inputs;
		});
	}

	function setValue(key: keyof z.infer<T>, value: z.infer<T>[keyof z.infer<T>]) {
		inputsStore.update((inputs) => {
			inputs[key].value = value;
			return inputs;
		});
	}

	// Shows server-side validation errors (apperror field errors) next to the matching inputs
	// Huma reports locations like `body.defaultLimits.maxTurns`, so the full path is tried first and the last segment second, which matches forms that flatten nested objects
	function setErrors(fieldErrors: { field: string; message: string }[]) {
		inputsStore.update((inputs) => {
			for (const fieldError of fieldErrors) {
				const path = fieldError.field.replace(/^body\./, '');
				const key = (path in inputs ? path : path.split('.').pop()) as keyof z.infer<T>;
				if (key && inputs[key]) inputs[key].error = fieldError.message;
			}
			return inputs;
		});
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
				return o.def.type == 'optional' || (o instanceof z.ZodLiteral && o.def.values.includes(''));
			});
		}

		// Pipes such as emptyToUndefined are required when their output is
		if (fieldSchema instanceof z.ZodPipe) {
			return isRequired(fieldSchema.def.out as z.ZodTypeAny);
		}

		if (fieldSchema instanceof z.ZodOptional || fieldSchema instanceof z.ZodDefault) {
			return false;
		}

		return true;
	}

	return {
		schema,
		inputs: inputsStore,
		errors: errorsStore,
		data,
		validate,
		setValue,
		setErrors,
		reset,
		clearErrors,
		isDirty,
		commit,
		getBaseline
	};
}

// Picks the properties declared by `schema` out of a larger object, e.g. to build a form for one section of the settings
export function pickSchemaValues<T extends z.ZodObject>(schema: T, values: z.infer<T>): z.infer<T> {
	return Object.fromEntries(
		Object.keys(schema.shape).map((key) => [key, (values as Record<string, unknown>)[key]])
	) as z.infer<T>;
}

// Copies a value including its nested objects and arrays
export function deepCopy<T>(value: T): T {
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
export function deepEqual(a: unknown, b: unknown): boolean {
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
