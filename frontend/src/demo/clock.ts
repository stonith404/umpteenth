// The demo's own time, which the app reads through Date.now like the real clock
// A recorded run replays faster or slower than it took, and durations, relative times and timeline offsets all follow, since they are computed from Date.now
// It stands still while the demo is off screen, so a reader never scrolls back to a run that finished without them

const realNow = Date.now.bind(Date);

let anchorReal = realNow();
let anchorVirtual = anchorReal;
let rate = 1;
let paused = false;

export const clock = {
	now(): number {
		return paused ? anchorVirtual : anchorVirtual + (realNow() - anchorReal) * rate;
	},

	// Re-anchors before any change, so time never jumps when the speed or the pause changes
	setRate(next: number) {
		reanchor();
		rate = next;
	},

	setPaused(next: boolean) {
		reanchor();
		paused = next;
	}
};

function reanchor() {
	anchorVirtual = clock.now();
	anchorReal = realNow();
}

// Everything the app computes from the current time goes through Date.now, so replacing it moves the whole page onto the demo's time
export function installClock() {
	Date.now = () => Math.round(clock.now());
}
