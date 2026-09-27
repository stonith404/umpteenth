import { getContext, setContext } from 'svelte';
import { MediaQuery } from 'svelte/reactivity';
import { IsMobile } from '$lib/hooks/is-mobile.svelte.js';
import { SIDEBAR_KEYBOARD_SHORTCUT, SIDEBAR_WIDE_BREAKPOINT } from './constants.js';

type Getter<T> = () => T;

export type SidebarStateProps = {
	/**
	 * A getter function that returns the current open state of the sidebar.
	 * We use a getter function here to support `bind:open` on the `Sidebar.Provider`
	 * component.
	 */
	open: Getter<boolean>;

	/**
	 * A function that sets the open state of the sidebar. To support `bind:open`, we need
	 * a source of truth for changing the open state to ensure it will be synced throughout
	 * the sub-components and any `bind:` references.
	 */
	setOpen: (open: boolean) => void;
};

class SidebarState {
	readonly props: SidebarStateProps;
	#isMobile = new IsMobile();
	#isWide = new MediaQuery(`min-width: ${SIDEBAR_WIDE_BREAKPOINT}px`);
	// Tablets start on the icon rail and can expand it for a while, without touching the saved desktop preference
	#openNarrow = $state(false);
	// The saved preference applies on wide screens only, anywhere narrower the rail is the default
	open = $derived.by(() => (this.#isWide.current ? this.props.open() : this.#openNarrow));
	openMobile = $state(false);
	state = $derived.by(() => (this.open ? 'expanded' : 'collapsed'));
	// On tablets the expanded sidebar floats over the page, since making room for it would squeeze the page into a phone's width at a tablet's breakpoints
	overlay = $derived.by(() => this.open && !this.#isWide.current && !this.#isMobile.current);

	constructor(props: SidebarStateProps) {
		this.props = props;

		// Leaving the wide layout always lands on the rail, so a tablet never inherits a sidebar someone expanded earlier
		$effect(() => {
			if (!this.#isWide.current) this.#openNarrow = false;
		});
	}

	// Convenience getter for checking if the sidebar is mobile
	// without this, we would need to use `sidebar.isMobile.current` everywhere
	get isMobile() {
		return this.#isMobile.current;
	}

	// Whether the viewport is wide enough for the saved expanded/collapsed preference, below it the sidebar defaults to the icon rail
	get isWide() {
		return this.#isWide.current;
	}

	// Expands or collapses the sidebar, where only wide screens remember the choice for the next visit
	setOpen = (value: boolean) => {
		if (this.#isWide.current) this.props.setOpen(value);
		else this.#openNarrow = value;
	};

	// Event handler to apply to the `<svelte:window>`
	handleShortcutKeydown = (e: KeyboardEvent) => {
		if (e.key === SIDEBAR_KEYBOARD_SHORTCUT && (e.metaKey || e.ctrlKey)) {
			e.preventDefault();
			this.toggle();
		}
	};

	setOpenMobile = (value: boolean) => {
		this.openMobile = value;
	};

	// Puts away the sidebar where it covers the page, i.e. the phone drawer and the tablet overlay, e.g. once a link in it was followed
	// The desktop sidebar sits beside the page, so it stays as the user left it
	dismiss = () => {
		this.openMobile = false;
		if (!this.#isWide.current) this.#openNarrow = false;
	};

	toggle = () => {
		return this.#isMobile.current ? (this.openMobile = !this.openMobile) : this.setOpen(!this.open);
	};
}

const SYMBOL_KEY = 'scn-sidebar';

/**
 * Instantiates a new `SidebarState` instance and sets it in the context.
 *
 * @param props The constructor props for the `SidebarState` class.
 * @returns  The `SidebarState` instance.
 */
export function setSidebar(props: SidebarStateProps): SidebarState {
	return setContext(Symbol.for(SYMBOL_KEY), new SidebarState(props));
}

/**
 * Retrieves the `SidebarState` instance from the context. This is a class instance,
 * so you cannot destructure it.
 * @returns The `SidebarState` instance.
 */
export function useSidebar(): SidebarState {
	return getContext(Symbol.for(SYMBOL_KEY));
}
