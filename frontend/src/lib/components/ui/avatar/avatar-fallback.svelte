<script lang="ts">
	import { Avatar as AvatarPrimitive } from 'bits-ui';
	import { cn } from '#lib/utils/style.js';

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: AvatarPrimitive.FallbackProps = $props();
</script>

<AvatarPrimitive.Fallback
	bind:ref
	data-slot="avatar-fallback"
	class={cn(
		// Initials are set small and in medium weight like Kumo's, so two letters sit comfortably inside every avatar size
		'bg-muted text-muted-foreground rounded-full flex size-full items-center justify-center text-xs font-medium',
		className
	)}
	{...restProps}
/>
