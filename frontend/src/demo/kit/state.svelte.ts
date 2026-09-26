// Stands in for SvelteKit's `$app/state` in the demo, which mounts the app's routes without the router
// The demo sets the URL and the data itself when it shows another run

// The origin the app believes it runs on, so links and breadcrumbs read like a real instance
export const DEMO_ORIGIN = 'https://umpteenth.example.com';

class DemoPage {
	url = $state.raw(new URL(DEMO_ORIGIN));
	params = $state.raw<Record<string, string>>({});
	route = $state.raw<{ id: string | null }>({ id: null });
	data = $state.raw<Record<string, unknown>>({});
	status = 200;
	error = null;
	form = null;
	state = {};
}

export const page = new DemoPage();

// The demo never navigates through the router, so there is never a navigation in progress
export const navigating = {
	from: null,
	to: null,
	type: null,
	willUnload: null,
	delta: null,
	complete: null
};

export const updated = {
	current: false,
	check: async () => false
};
