import { invalidate } from '$app/navigation';
import { navigating } from '$app/state';
import type { WorkspaceEvent } from '#lib/api/types.js';

// Live run changes of the workspace from `GET /api/events`, shared by every table and dashboard on the page
// One EventSource serves all subscribers, so a page with several live views still holds a single connection

export type WorkspaceEventListener = {
	// A run was created or changed status
	onRun?: (event: WorkspaceEvent) => void;
	// Reflection on a run started or finished, which may have changed the job's playbook
	onReflection?: (event: WorkspaceEvent) => void;
	// The connection came back after a drop, so events may have been missed and views should reload
	onReconnect?: () => void;
};

const EVENTS_URL = '/api/events';
const RETRY_MS = 5_000;

const listeners = new Set<WorkspaceEventListener>();
let source: EventSource | null = null;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let dropped = false;

function connect() {
	source = new EventSource(EVENTS_URL);

	source.addEventListener('run', (message) => {
		let event: WorkspaceEvent;
		try {
			event = JSON.parse(message.data);
		} catch {
			return;
		}
		// Every workspace event arrives as a "run" message, and its kind says what changed
		for (const listener of listeners) {
			if (event.kind === 'reflection') listener.onReflection?.(event);
			else listener.onRun?.(event);
		}
	});

	source.addEventListener('open', () => {
		if (!dropped) return;
		dropped = false;
		for (const listener of listeners) listener.onReconnect?.();
	});

	source.addEventListener('error', () => {
		dropped = true;

		// EventSource retries by itself after network errors, but gives up for good on a failed HTTP response such as a proxy error
		if (source?.readyState === EventSource.CLOSED) {
			source = null;
			retryTimer = setTimeout(() => {
				retryTimer = undefined;
				if (listeners.size > 0 && !source) connect();
			}, RETRY_MS);
		}
	});
}

// Subscribes to live run changes and returns the unsubscribe function, the connection closes with the last subscriber
export function subscribeWorkspaceEvents(listener: WorkspaceEventListener): () => void {
	listeners.add(listener);
	if (!source && retryTimer === undefined) connect();

	return () => {
		listeners.delete(listener);
		if (listeners.size > 0) return;
		source?.close();
		source = null;
		clearTimeout(retryTimer);
		retryTimer = undefined;
		dropped = false;
	};
}

// Reconnects the stream after the session moved to another workspace, whose events the open connection doesn't carry
// Views that stay mounted across the switch keep their subscription and get onReconnect to reload
export function resetWorkspaceEvents() {
	source?.close();
	source = null;
	clearTimeout(retryTimer);
	retryTimer = undefined;
	dropped = listeners.size > 0;
	if (listeners.size > 0) connect();
}

// Reloads the load functions that depend on the given key, after any navigation in flight has finished
// SvelteKit cancels a pending navigation when an invalidation starts, so a live event arriving mid-navigation would otherwise strand the user on the old page, e.g. right after Run now redirects to the new run
export async function invalidateAfterNavigation(dependency: string) {
	await navigating.complete?.catch(() => {});
	await invalidate(dependency);
}
