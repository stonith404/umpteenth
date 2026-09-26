import type { Component, ComponentProps, Snippet } from 'svelte';

// Marks a column's `cell` or `header` result as a Svelte component, so FlexRender knows how to render it
export class RenderComponentConfig<TComponent extends Component> {
	component: TComponent;
	props: ComponentProps<TComponent> | Record<string, never>;
	constructor(
		component: TComponent,
		props: ComponentProps<TComponent> | Record<string, never> = {}
	) {
		this.component = component;
		this.props = props;
	}
}

// Marks a column's `cell` or `header` result as a Svelte snippet, so FlexRender knows how to render it
export class RenderSnippetConfig<TProps> {
	snippet: Snippet<[TProps]>;
	params: TProps;
	constructor(snippet: Snippet<[TProps]>, params: TProps) {
		this.snippet = snippet;
		this.params = params;
	}
}

// Renders a Svelte component from a column definition, e.g. `cell: ({ row }) => renderComponent(StatusBadge, { status: row.original.status })`
export function renderComponent<T extends Component<any>, Props extends ComponentProps<T>>(
	component: T,
	props: Props = {} as Props
) {
	return new RenderComponentConfig(component, props);
}

// Renders a snippet that takes exactly one parameter from a column definition, e.g. `cell: ({ row }) => renderSnippet(nameCell, row.original)`
export function renderSnippet<TProps>(snippet: Snippet<[TProps]>, params: TProps = {} as TProps) {
	return new RenderSnippetConfig(snippet, params);
}
