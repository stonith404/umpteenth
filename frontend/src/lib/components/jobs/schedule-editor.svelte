<script lang="ts">
	import TimezonePicker from '$lib/components/form/timezone-picker.svelte';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import * as Field from '$lib/components/ui/field';
	import * as InputGroup from '$lib/components/ui/input-group';
	import { Switch } from '$lib/components/ui/switch';
	import { CRON_PRESETS, describeCron } from '$lib/utils/cron-util';
	import { localTimezone } from '$lib/utils/job-util';
	import CalendarClockIcon from '@lucide/svelte/icons/calendar-clock';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import { untrack } from 'svelte';

	let {
		cron = $bindable(''),
		timezone = $bindable(''),
		cronError,
		timezoneError
	}: {
		// An empty expression means the job has no schedule
		cron?: string;
		timezone?: string;
		cronError?: string | null;
		timezoneError?: string | null;
	} = $props();

	const id = $props.id();

	// The switch keeps its own state, so switching it on can show an empty expression without switching straight back off
	let scheduled = $state(untrack(() => cron.trim() !== ''));

	// Values replaced from outside, e.g. discarding changes, move the switch along
	$effect(() => {
		const hasCron = cron.trim() !== '';
		untrack(() => {
			if (hasCron) scheduled = true;
		});
	});

	const description = $derived(describeCron(cron));

	function onScheduledChange(checked: boolean) {
		scheduled = checked;
		if (checked) {
			if (!cron.trim()) cron = CRON_PRESETS[1].cron;
			if (!timezone) timezone = localTimezone();
		} else {
			cron = '';
		}
	}
</script>

<div class="flex flex-col gap-5">
	<Field.Field orientation="horizontal">
		<Switch id="{id}-scheduled" checked={scheduled} onCheckedChange={onScheduledChange} />
		<Field.Content>
			<Field.Label for="{id}-scheduled">Run on a schedule</Field.Label>
			<Field.Description>
				Without a schedule the job runs on demand, through a webhook or the API.
			</Field.Description>
		</Field.Content>
	</Field.Field>

	{#if scheduled}
		<div class="grid grid-cols-1 gap-x-6 gap-y-5 md:grid-cols-2">
			<Field.Field data-invalid={!!cronError}>
				<Field.Label for="{id}-cron">Cron expression</Field.Label>
				<InputGroup.Root>
					<InputGroup.Input
						id="{id}-cron"
						bind:value={cron}
						class="font-mono"
						placeholder="0 8 * * 1-5"
						aria-invalid={!!cronError}
					/>
					<InputGroup.Addon align="inline-end">
						<DropdownMenu.Root>
							<DropdownMenu.Trigger>
								{#snippet child({ props })}
									<InputGroup.Button {...props} size="xs" aria-label="Schedule presets">
										Presets
										<ChevronDownIcon />
									</InputGroup.Button>
								{/snippet}
							</DropdownMenu.Trigger>
							<DropdownMenu.Content align="end">
								{#each CRON_PRESETS as preset (preset.cron)}
									<DropdownMenu.Item onSelect={() => (cron = preset.cron)}>
										{preset.label}
										<span class="text-muted-foreground ml-auto pl-4 font-mono text-xs">
											{preset.cron}
										</span>
									</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Root>
					</InputGroup.Addon>
				</InputGroup.Root>
				{#if cronError}
					<Field.Error>{cronError}</Field.Error>
				{:else}
					<Field.Description>
						Five fields: minute, hour, day of month, month, day of week.
					</Field.Description>
				{/if}
			</Field.Field>
			<Field.Field data-invalid={!!timezoneError}>
				<Field.Label for="{id}-timezone">Timezone</Field.Label>
				<TimezonePicker id="{id}-timezone" bind:value={timezone} invalid={!!timezoneError} />
				{#if timezoneError}
					<Field.Error>{timezoneError}</Field.Error>
				{:else}
					<Field.Description>The expression is evaluated in this timezone.</Field.Description>
				{/if}
			</Field.Field>
		</div>
		{#if description}
			<p class="text-muted-foreground flex items-center gap-2 text-sm">
				<CalendarClockIcon class="size-4" />
				<span>
					<span class="text-foreground font-medium">{description}</span>
					({timezone || 'UTC'})
				</span>
			</p>
		{/if}
	{/if}
</div>
