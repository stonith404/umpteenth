<script lang="ts">
	import type { Skill } from '$lib/api/types';
	import { Button } from '$lib/components/ui/button';
	import * as Command from '$lib/components/ui/command';
	import * as Popover from '$lib/components/ui/popover';
	import PlusIcon from '@lucide/svelte/icons/plus';

	let {
		skills,
		onAttach
	}: {
		// The skills that can still be attached
		skills: Skill[];
		onAttach: (skill: Skill) => void;
	} = $props();

	let open = $state(false);

	function attach(skill: Skill) {
		onAttach(skill);
		open = false;
	}
</script>

<!-- A searchable list rather than a select, since a workspace can hold many skills and picking one attaches it instead of setting a value -->
<Popover.Root bind:open>
	<Popover.Trigger>
		{#snippet child({ props })}
			<Button {...props} variant="outline" aria-expanded={open}>
				<PlusIcon data-icon="inline-start" />
				Attach skill
			</Button>
		{/snippet}
	</Popover.Trigger>
	<Popover.Content fitScreen padding="none" class="w-96" align="start">
		<Command.Root>
			<Command.Input placeholder="Search skills" />
			<Command.List class="max-h-80">
				<Command.Empty>No skill found</Command.Empty>
				<Command.Group>
					{#each skills as skill (skill.id)}
						<!-- The description counts for the search too, since it says what a skill is for -->
						<Command.Item
							value={skill.name}
							keywords={[skill.description]}
							onSelect={() => attach(skill)}
						>
							<span class="flex min-w-0 flex-1 flex-col">
								<span class="truncate">{skill.name}</span>
								<span class="text-muted-foreground line-clamp-2 text-xs">{skill.description}</span>
							</span>
						</Command.Item>
					{/each}
				</Command.Group>
			</Command.List>
		</Command.Root>
	</Popover.Content>
</Popover.Root>
