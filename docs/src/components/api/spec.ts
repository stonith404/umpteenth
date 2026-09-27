// Turns the OpenAPI spec that scripts/openapi.mjs generates into the groups, operations and field trees the API reference page renders
import generated from '../../generated/openapi.json';

type Schema = Record<string, any>;

const spec = generated as Schema;

export type Field = {
	name: string;
	type: string;
	required: boolean;
	description?: string;
	notes: string[];
	values?: string[];
	children?: Field[];
};

export type Param = Field & { in: string };

export type Body = { contentType: string; fields?: Field[]; type?: string; description?: string };

export type Event = { name: string; fields?: Field[] };

export type Response = { status: string; description: string; contentType?: string; fields?: Field[]; type?: string; events?: Event[] };

export type Operation = {
	id: string;
	method: string;
	path: string;
	summary: string;
	params: Param[];
	body?: Body;
	responses: Response[];
};

export type Group = { id: string; name: string; operations: Operation[] };

const schemas: Record<string, Schema> = spec.components.schemas;

// Groups follow a reader's path through the product, and a tag the backend adds later lands at the end instead of vanishing
const groupOrder = ['Jobs', 'Runs', 'Playbook', 'Job state', 'Images', 'Webhooks', 'Secrets', 'MCP', 'Providers', 'Settings', 'Stats', 'API tokens', 'Auth', 'System'];
const methodOrder = ['get', 'post', 'put', 'patch', 'delete'];

export const slug = (text: string) => text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

// Follows a $ref to the component it names, so callers always see the schema itself
function resolve(schema: Schema | undefined): Schema {
	if (!schema) return {};
	if (schema.$ref) return schemas[schema.$ref.split('/').pop()] ?? {};
	return schema;
}

function types(schema: Schema): { base?: string; nullable: boolean } {
	const list: string[] = Array.isArray(schema.type) ? schema.type : schema.type ? [schema.type] : [];
	return { base: list.find((t) => t !== 'null'), nullable: list.includes('null') };
}

const isObject = (schema: Schema) => types(schema).base === 'object' || (!schema.type && schema.properties);
const hasFields = (schema: Schema) => isObject(schema) && Object.keys(schema.properties ?? {}).length > 0;

// The plural of a type label, so an array of objects doesn't read as "array of object"
function plural(label: string): string {
	if (label.endsWith(' or null')) return `${plural(label.slice(0, -' or null'.length))} or null`;
	if (label.startsWith('array of ') || label.startsWith('map of ')) return label.replace(/^(array|map)/, '$1s');
	if (label === 'any') return 'values';
	if (label.startsWith('"') || label === 'binary') return label;
	return `${label}s`;
}

// A short type label in the words a reader would use, such as "array of strings" or "integer or null"
export function typeLabel(input: Schema | undefined): string {
	const schema = resolve(input);
	const { base, nullable } = types(schema);
	let label: string;
	if (schema.const !== undefined) label = JSON.stringify(schema.const);
	else if (base === 'array') {
		label = `array of ${plural(typeLabel(schema.items))}`;
	} else if (base === 'object' && !schema.properties && schema.additionalProperties && typeof schema.additionalProperties === 'object') {
		label = `map of ${plural(typeLabel(schema.additionalProperties))}`;
	} else if (base === 'string' && schema.format === 'binary') label = 'binary';
	else if (base === 'string' && schema.format === 'date-time') label = 'date-time string';
	else if (base) label = base;
	else if (schema.properties) label = 'object';
	else label = 'any';
	return nullable ? `${label} or null` : label;
}

// Defaults and limits worth knowing before a call fails validation
function notes(schema: Schema): string[] {
	const out: string[] = [];
	const range = (min: number | undefined, max: number | undefined, unit = '') => {
		if (min !== undefined && max !== undefined) return `${min} to ${max}${unit}`;
		if (min !== undefined) return `at least ${min}${unit}`;
		if (max !== undefined) return `at most ${max}${unit}`;
	};
	const number = range(schema.minimum, schema.maximum);
	if (number) out.push(number);
	const length = range(schema.minLength, schema.maxLength, ' characters');
	if (length) out.push(length);
	const items = range(schema.minItems, schema.maxItems, ' items');
	if (items) out.push(items);
	if (schema.pattern) out.push(`matches \`${schema.pattern}\``);
	if (schema.default !== undefined) out.push(`defaults to \`${JSON.stringify(schema.default)}\``);
	return out;
}

// The object whose fields a property carries, directly or as the items of an array, or nothing for scalars
function fieldsOf(schema: Schema): Schema | undefined {
	const resolved = resolve(schema);
	if (hasFields(resolved)) return resolved;
	if (types(resolved).base === 'array') {
		const item = resolve(resolved.items);
		if (hasFields(item)) return item;
	}
}

// The fields of an object, with nested objects unfolded until a schema repeats inside itself
export function fields(input: Schema | undefined, request = false, seen: string[] = []): Field[] {
	const schema = resolve(input);
	const required = new Set<string>(schema.required ?? []);
	const out: Field[] = [];
	for (const [name, raw] of Object.entries<Schema>(schema.properties ?? {})) {
		const property = resolve(raw);
		if (request && property.readOnly) continue;
		const ref = raw.$ref ?? (property.items?.$ref as string | undefined);
		const nested = fieldsOf(raw);
		out.push({
			name,
			type: typeLabel(raw),
			required: request && required.has(name),
			description: raw.description ?? property.description,
			notes: notes(property),
			values: (property.enum ?? resolve(property.items).enum)?.map(String),
			children: nested && !(ref && seen.includes(ref)) ? fields(nested, request, ref ? [...seen, ref] : seen) : undefined
		});
	}
	// A caller scans a request for what it must send, while an answer keeps the spec's order, since its fields are all there anyway
	return request ? out.sort((a, b) => Number(b.required) - Number(a.required)) : out;
}

function body(content: Record<string, { schema?: Schema }> | undefined, request: boolean): Body | undefined {
	const [contentType, media] = Object.entries(content ?? {})[0] ?? [];
	if (!contentType) return;
	const schema = resolve(media?.schema);
	if (hasFields(schema)) return { contentType, fields: fields(schema, request) };
	const nested = fieldsOf(schema);
	if (nested) return { contentType, type: typeLabel(schema), fields: fields(nested, request) };
	return { contentType, type: typeLabel(schema), description: schema.description };
}

// A Server-Sent Events response lists its events as oneOf variants, each with a constant event name and a data object
function events(schema: Schema): Event[] {
	return (resolve(schema).items?.oneOf ?? []).map((variant: Schema) => ({
		name: String(variant.properties?.event?.const ?? variant.title ?? 'message'),
		fields: fieldsOf(variant.properties?.data ?? {}) ? fields(fieldsOf(variant.properties.data)) : undefined
	}));
}

function operation(path: string, method: string, op: Schema): Operation {
	// Errors share one shape that the REST API page explains, so each operation lists only its successful answers
	const responses: Response[] = Object.entries<Schema>(op.responses ?? {})
		.filter(([status]) => status !== 'default')
		.map(([status, response]) => {
			const [contentType, media] = Object.entries<Schema>(response.content ?? {})[0] ?? [];
			if (contentType === 'text/event-stream') return { status, description: response.description, contentType, events: events(media.schema) };
			const answer = body(response.content, false);
			return { status, description: response.description, ...answer };
		});
	return {
		id: op.operationId,
		method,
		path,
		summary: op.summary ?? op.operationId,
		params: (op.parameters ?? []).map((param: Schema) => ({
			in: param.in,
			name: param.name,
			type: typeLabel(param.schema),
			required: Boolean(param.required),
			description: param.description ?? param.schema?.description,
			notes: notes(resolve(param.schema)),
			values: resolve(param.schema).enum?.map(String)
		})),
		body: body(op.requestBody?.content, true),
		responses
	};
}

// Every operation of the spec, grouped by its first tag in the order above, keeping the spec's path order and the method order within a group
export function groups(): Group[] {
	const byName = new Map<string, Operation[]>();
	for (const [path, item] of Object.entries<Schema>(spec.paths)) {
		for (const method of methodOrder) {
			const op = item[method];
			if (!op?.operationId) continue;
			const name = op.tags?.[0] ?? 'Other';
			byName.set(name, [...(byName.get(name) ?? []), operation(path, method, op)]);
		}
	}
	const rank = (name: string) => (groupOrder.includes(name) ? groupOrder.indexOf(name) : groupOrder.length);
	return [...byName.entries()]
		.sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
		.map(([name, operations]) => ({ id: slug(name), name, operations }));
}
