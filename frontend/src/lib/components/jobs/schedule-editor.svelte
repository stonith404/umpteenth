<script lang="ts">
	import TimezonePicker from '$lib/components/form/timezone-picker.svelte';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import * as Field from '$lib/components/ui/field';
	import * as InputGroup from '$lib/components/ui/input-group';
	import { Switch } from '$lib/components/ui/switch';
	import {
		CRON_PRESETS,
		SCHEDULE_EDITOR_PRESETS,
		describeCron,
		isValidCron
	} from '$lib/utils/cron-util';
	import { localTimezone } from '$lib/utils/job-util';
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

	// The switch keeps its own state, so clearing the field while typing doesn't hide it
	let scheduled = $state(untrack(() => cron.trim() !== ''));

	// The expression as the editor last set it, which tells a value replaced from outside apart from one being typed
	let ownCron = untrack(() => cron);

	// Turning the switch off and on again brings back the schedule it had, so undoing the toggle leaves the form as it was
	let lastCron = untrack(() => cron);
	let lastTimezone = '';

	// The timezone as it was when the switch was turned on, which turning it off restores, so a job without a schedule doesn't keep a zone it never had
	// Null while the switch hasn't been turned on here, e.g. for a job that already had a schedule, whose zone then stays as it is
	let timezoneBeforeOn: string | null = null;

	// Values replaced from outside, e.g. the schedule a compiled job brings, move the switch along, including back off for a job without a schedule
	$effect(() => {
		const next = cron;
		untrack(() => {
			if (next === ownCron) return;
			ownCron = next;
			scheduled = next.trim() !== '';

			// The toggle's memory belongs to the replaced value, so the switch starts over from the new one
			lastCron = next;
			lastTimezone = '';
			timezoneBeforeOn = null;
		});
	});

	function setCron(value: string) {
		ownCron = value;
		cron = value;
	}

	const description = $derived(describeCron(cron));
	const invalid = $derived(cron.trim() !== '' && !isValidCron(cron));

	function onScheduledChange(checked: boolean) {
		scheduled = checked;
		if (checked) {
			// The schedule the switch last turned off comes back, and a first one starts from a common preset in the viewer's own timezone
			timezoneBeforeOn = timezone;
			if (!cron.trim()) setCron(lastCron.trim() ? lastCron : CRON_PRESETS[1].cron);
			if (!timezone) timezone = lastTimezone || localTimezone();
			return;
		}

		// Both values are kept for turning the switch back on
		lastCron = cron;
		lastTimezone = timezone;
		setCron('');
		if (timezoneBeforeOn !== null) timezone = timezoneBeforeOn;
		timezoneBeforeOn = null;
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
		<!-- The two fields sit side by side once the card, not the window, has room for them -->
		<div class="@container">
			<div class="grid grid-cols-1 gap-x-6 gap-y-5 @xl:grid-cols-2">
				<Field.Field data-invalid={!!cronError}>
					<Field.Label for="{id}-cron">Cron expression</Field.Label>
					<InputGroup.Root>
						<InputGroup.Input
							id="{id}-cron"
							bind:value={() => cron, setCron}
							class="font-mono"
							placeholder="0 8 * * 1-5"
							aria-invalid={!!cronError}
							aria-describedby="{id}-cron-summary"
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
									<!-- The preset matching the current expression carries the check, like a picked option -->
									<DropdownMenu.RadioGroup value={cron.trim()} onValueChange={setCron}>
										{#each SCHEDULE_EDITOR_PRESETS as preset (preset.cron)}
											<DropdownMenu.RadioItem value={preset.cron}>
												{preset.label}
												<span class="text-muted-foreground ml-auto pl-4 font-mono text-xs">
													{preset.cron}
												</span>
											</DropdownMenu.RadioItem>
										{/each}
									</DropdownMenu.RadioGroup>
								</DropdownMenu.Content>
							</DropdownMenu.Root>
						</InputGroup.Addon>
					</InputGroup.Root>
					<!-- The line under the field reads the expression back in plain words, or says what is wrong with it -->
					{#if cronError}
						<Field.Error id="{id}-cron-summary">{cronError}</Field.Error>
					{:else if invalid}
						<Field.Description id="{id}-cron-summary" class="text-warning-foreground">
							Not a valid cron expression, e.g. <code class="font-mono">0 9 * * 1-5</code>
						</Field.Description>
					{:else if description}
						<Field.Description id="{id}-cron-summary" class="text-foreground font-medium">
							{description}
						</Field.Description>
					{:else}
						<Field.Description id="{id}-cron-summary">
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
		</div>
	{/if}
</div>
