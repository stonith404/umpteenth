<script lang="ts">
	import { cn } from '#lib/utils/style.js';
	import CircleNotchIcon from 'phosphor-svelte/lib/CircleNotch';
	import type { ComponentProps } from 'svelte';

	let { class: className, ...restProps }: ComponentProps<typeof CircleNotchIcon> = $props();
</script>

<!-- The icon context hides icons from screen readers, which the spinner opts out of so it announces the wait -->
<CircleNotchIcon
	role="status"
	aria-hidden="false"
	aria-label="Loading"
	class={cn('size-4 animate-spin', className)}
	{...restProps}
/>
