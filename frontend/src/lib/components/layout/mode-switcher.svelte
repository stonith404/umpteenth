<script lang="ts">
	import { buttonVariants } from '$lib/components/ui/button';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import { cn } from '$lib/utils/style';
	import MonitorIcon from '@lucide/svelte/icons/monitor';
	import MoonIcon from '@lucide/svelte/icons/moon';
	import SunIcon from '@lucide/svelte/icons/sun';
	import { mode, resetMode, setMode, userPrefersMode } from 'mode-watcher';

	const isDark = $derived(mode.current === 'dark');
</script>

<DropdownMenu.Root>
	<DropdownMenu.Trigger
		class={buttonVariants({ variant: 'ghost', size: 'icon-sm' })}
		aria-label="Toggle theme"
	>
		<SunIcon
			class={cn('size-4 transition-all', isDark ? '-rotate-90 scale-0' : 'rotate-0 scale-100')}
		/>
		<MoonIcon
			class={cn(
				'absolute size-4 transition-all',
				isDark ? 'rotate-0 scale-100' : 'rotate-90 scale-0'
			)}
		/>
	</DropdownMenu.Trigger>
	<DropdownMenu.Content align="end">
		<DropdownMenu.RadioGroup
			value={userPrefersMode.current}
			onValueChange={(value) => (value === 'system' ? resetMode() : setMode(value as 'light'))}
		>
			<DropdownMenu.RadioItem value="light"><SunIcon /> Light</DropdownMenu.RadioItem>
			<DropdownMenu.RadioItem value="dark"><MoonIcon /> Dark</DropdownMenu.RadioItem>
			<DropdownMenu.RadioItem value="system"><MonitorIcon /> System</DropdownMenu.RadioItem>
		</DropdownMenu.RadioGroup>
	</DropdownMenu.Content>
</DropdownMenu.Root>
