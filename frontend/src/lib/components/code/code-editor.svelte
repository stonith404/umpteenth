<script lang="ts">
	import { cn } from '$lib/utils/style';
	import { defaultHighlightStyle, syntaxHighlighting } from '@codemirror/language';
	import { Compartment, EditorState, type Extension } from '@codemirror/state';
	import { oneDarkHighlightStyle } from '@codemirror/theme-one-dark';
	import { EditorView, placeholder as placeholderExtension } from '@codemirror/view';
	import { basicSetup } from 'codemirror';
	import { mode } from 'mode-watcher';
	import { onMount, untrack } from 'svelte';
	import { languageExtension } from './code-languages';
	import type { CodeLanguage } from './script-language';

	let {
		value = $bindable(''),
		language = 'plain',
		readonly = false,
		placeholder,
		label,
		id,
		invalid = false,
		lineWrapping = false,
		class: className
	}: {
		value?: string;
		language?: CodeLanguage;
		readonly?: boolean;
		placeholder?: string;
		// Accessible name of the editor, since CodeMirror's content element is not a form control a <label> could point at
		label?: string;
		id?: string;
		invalid?: boolean;
		lineWrapping?: boolean;
		// Sizing classes such as `h-80` or `max-h-96`, applied to the scrolling container
		class?: string;
	} = $props();

	let container: HTMLDivElement;
	let view: EditorView | undefined;

	// Compartments let the language, theme and read-only state change without recreating the editor and losing its undo history
	const languageCompartment = new Compartment();
	const themeCompartment = new Compartment();
	const readonlyCompartment = new Compartment();
	const wrapCompartment = new Compartment();

	const isDark = $derived(mode.current === 'dark');

	// The editor chrome uses the app's own tokens, so it matches cards in both themes; only token colors differ per theme
	const baseTheme = EditorView.theme({
		'&': {
			height: '100%',
			// Takes a max height set on the container, so the editor stops growing there and scrolls instead
			maxHeight: 'inherit',
			backgroundColor: 'transparent',
			color: 'var(--foreground)',
			fontSize: '0.8125rem'
		},
		'&.cm-focused': { outline: 'none' },
		'.cm-scroller': {
			fontFamily: 'var(--font-mono)',
			lineHeight: '1.6',
			overflow: 'auto'
		},
		'.cm-gutters': {
			backgroundColor: 'transparent',
			color: 'var(--muted-foreground)',
			border: 'none'
		},
		'.cm-activeLineGutter, .cm-activeLine': {
			backgroundColor: 'color-mix(in oklab, var(--muted) 60%, transparent)'
		},
		'.cm-content': { caretColor: 'var(--foreground)' },
		'.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--foreground)' },
		'&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, ::selection':
			{ backgroundColor: 'color-mix(in oklab, var(--ring) 35%, transparent) !important' },
		'.cm-placeholder': { color: 'var(--muted-foreground)' },
		'.cm-tooltip': {
			backgroundColor: 'var(--popover)',
			color: 'var(--popover-foreground)',
			border: '1px solid var(--border)',
			borderRadius: '0.5rem'
		},
		'.cm-panels': { backgroundColor: 'var(--muted)', color: 'var(--foreground)' },
		'.cm-foldPlaceholder': {
			backgroundColor: 'var(--muted)',
			border: 'none',
			color: 'var(--muted-foreground)'
		}
	});

	// basicSetup already registers the light style as a fallback, so the chosen style must not be a fallback too or the light one wins in dark mode
	function themeExtension(dark: boolean): Extension {
		return syntaxHighlighting(dark ? oneDarkHighlightStyle : defaultHighlightStyle);
	}

	function readonlyExtension(ro: boolean): Extension {
		return [EditorState.readOnly.of(ro), EditorView.editable.of(!ro)];
	}

	onMount(() => {
		const attributes: Record<string, string> = {};
		if (label) attributes['aria-label'] = label;
		if (id) attributes.id = id;

		view = new EditorView({
			parent: container,
			state: EditorState.create({
				doc: untrack(() => value),
				extensions: [
					basicSetup,
					baseTheme,
					EditorView.contentAttributes.of(attributes),
					placeholder ? placeholderExtension(placeholder) : [],
					languageCompartment.of(languageExtension(untrack(() => language))),
					themeCompartment.of(themeExtension(untrack(() => isDark))),
					readonlyCompartment.of(readonlyExtension(untrack(() => readonly))),
					wrapCompartment.of(untrack(() => lineWrapping) ? EditorView.lineWrapping : []),
					EditorView.updateListener.of((update) => {
						if (update.docChanged) value = update.state.doc.toString();
					})
				]
			})
		});

		return () => {
			view?.destroy();
			view = undefined;
		};
	});

	// Values set from outside, e.g. a Dockerfile template or another version, replace the document
	$effect(() => {
		const next = value ?? '';
		untrack(() => {
			if (!view) return;
			const current = view.state.doc.toString();
			if (current !== next) {
				view.dispatch({ changes: { from: 0, to: current.length, insert: next } });
			}
		});
	});

	$effect(() => {
		const extension = languageExtension(language);
		untrack(() => view?.dispatch({ effects: languageCompartment.reconfigure(extension) }));
	});

	$effect(() => {
		const extension = themeExtension(isDark);
		untrack(() => view?.dispatch({ effects: themeCompartment.reconfigure(extension) }));
	});

	$effect(() => {
		const extension = readonlyExtension(readonly);
		untrack(() => view?.dispatch({ effects: readonlyCompartment.reconfigure(extension) }));
	});

	$effect(() => {
		const extension = lineWrapping ? EditorView.lineWrapping : [];
		untrack(() => view?.dispatch({ effects: wrapCompartment.reconfigure(extension) }));
	});
</script>

<div
	bind:this={container}
	data-slot="code-editor"
	data-invalid={invalid || undefined}
	class={cn(
		'bg-card shadow-xs focus-within:border-foreground/35 focus-within:ring-foreground/8 overflow-hidden rounded-lg border border-input py-1 transition-field focus-within:ring-3',
		invalid && 'border-destructive ring-destructive/20 dark:ring-destructive/40 ring-3',
		readonly && 'bg-muted/50 focus-within:border-transparent focus-within:ring-0',
		'h-64',
		className
	)}
></div>
