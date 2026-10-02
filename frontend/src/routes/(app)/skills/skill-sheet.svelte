<script lang="ts">
	import type { Skill, SkillFile } from '#lib/api/types.js';
	import CodeEditor from '#lib/components/code/code-editor.svelte';
	import type { CodeLanguage } from '#lib/components/code/script-language.js';
	import Markdown from '#lib/components/markdown.svelte';
	import RelativeTime from '#lib/components/relative-time.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Sheet from '#lib/components/ui/sheet/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import SkillService from '#lib/services/skill-service.js';
	import { getErrorMessage } from '#lib/utils/error-util.js';
	import { formatBytes } from '#lib/utils/format-util.js';
	import { tryCatch } from '#lib/utils/try-catch-util.js';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import FileIcon from '@lucide/svelte/icons/file';
	import FileTerminalIcon from '@lucide/svelte/icons/file-terminal';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import UploadIcon from '@lucide/svelte/icons/upload';

	let {
		skill = $bindable(null),
		onReplace,
		onUpdate,
		updating = false
	}: {
		// The skill to show, the sheet is open while it is set
		skill: Skill | null;
		onReplace: (skill: Skill) => void;
		// Takes the current version from the link the skill was imported from
		onUpdate: (skill: Skill) => void;
		updating?: boolean;
	} = $props();

	// Files beyond this size are offered as a download instead of being shown
	const MAX_PREVIEW_BYTES = 256 << 10;

	const skillService = new SkillService();

	let content = $state<HTMLElement | null>(null);
	let selected = $state('SKILL.md');
	let text = $state<string | null>(null);
	let loadError = $state('');
	let loading = $state(false);

	const selectedFile = $derived(skill?.files?.find((f) => f.path === selected));

	// Opening another skill starts at its SKILL.md
	let shownId = '';
	$effect(() => {
		if (skill && skill.id !== shownId) {
			shownId = skill.id;
			selected = 'SKILL.md';
		}
		if (!skill) shownId = '';
	});

	// The selected file is loaded whenever the selection or the skill's version changes
	$effect(() => {
		const current = skill;
		const path = selected;
		if (!current) return;
		void current.contentHash;
		void load(current, path);
	});

	async function load(current: Skill, path: string) {
		const file = current.files?.find((f) => f.path === path);
		text = null;
		loadError = '';
		if (!file || file.size > MAX_PREVIEW_BYTES) return;
		loading = true;
		const result = await tryCatch(skillService.readFile(current.id, path));
		// A newer selection may have started meanwhile, whose result wins
		if (skill?.id !== current.id || selected !== path) return;
		loading = false;
		if (result.error) {
			loadError = getErrorMessage(result.error, 'The file could not be loaded');
			return;
		}
		text = result.data;
	}

	// SKILL.md shows without its frontmatter, whose name and description the header already shows
	function withoutFrontmatter(source: string) {
		return source.replace(/^\uFEFF?---\r?\n[\s\S]*?\r?\n---[^\n]*\n?/, '');
	}

	// Links between a skill's files are relative to the skill folder rather than to the page, so they open that file in the sheet
	function openLinkedFile(event: MouseEvent) {
		const href = (event.target as HTMLElement).closest('a')?.getAttribute('href');
		if (!href || /^([a-z][a-z0-9+.-]*:|\/|#)/i.test(href)) return;
		const path = decodeURI(href.split('#')[0]).replace(/^\.\//, '');
		if (!skill?.files?.some((f) => f.path === path)) return;
		event.preventDefault();
		selected = path;
	}

	function languageOf(path: string): CodeLanguage {
		const ext = path.split('.').pop()?.toLowerCase() ?? '';
		if (ext === 'md' || ext === 'markdown') return 'markdown';
		if (ext === 'py') return 'python';
		if (ext === 'json') return 'json';
		if (['sh', 'bash', 'zsh'].includes(ext)) return 'shell';
		return 'plain';
	}

	// Text files are shown, anything else only offered as a download
	function isText(file: SkillFile) {
		return !/\.(png|jpe?g|gif|webp|ico|pdf|zip|gz|tgz|tar|woff2?|ttf|otf|bin|so|dylib|exe|wasm|docx?|xlsx?|pptx?)$/i.test(
			file.path
		);
	}

	// The first focusable element is often a relative time, whose exact-time tooltip would pop open on its own
	function focusSheet(event: Event) {
		event.preventDefault();
		content?.focus();
	}
</script>

<Sheet.Root open={skill !== null} onOpenChange={(open) => !open && (skill = null)}>
	<Sheet.Content
		bind:ref={content}
		tabindex={-1}
		focusable
		class="data-[side=right]:sm:max-w-3xl"
		onOpenAutoFocus={focusSheet}
	>
		{#if skill}
			<Sheet.Header>
				<Sheet.Title>
					<span class="min-w-0 font-mono wrap-anywhere">{skill.name}</span>
				</Sheet.Title>
				<Sheet.Description>{skill.description}</Sheet.Description>
			</Sheet.Header>
			<Sheet.Body>
				<dl class="grid grid-cols-label-value gap-x-4 gap-y-1.5 text-sm">
					<dt class="text-muted-foreground">Size</dt>
					<dd class="numeric">
						{formatBytes(skill.size)} in {skill.fileCount}
						{skill.fileCount === 1 ? 'file' : 'files'}
					</dd>
					<dt class="text-muted-foreground">Used by</dt>
					<dd class="numeric">{skill.jobCount} {skill.jobCount === 1 ? 'job' : 'jobs'}</dd>
					<dt class="text-muted-foreground">Updated</dt>
					<dd><RelativeTime value={skill.updatedAt} /></dd>
					{#if skill.sourceUrl}
						<dt class="text-muted-foreground">Source</dt>
						<dd class="min-w-0 truncate">
							<a
								href={skill.sourceUrl}
								target="_blank"
								rel="noopener noreferrer"
								class="link-underline"
								title={skill.sourceUrl}>{skill.sourceUrl}</a
							>
						</dd>
					{/if}
					<dt class="text-muted-foreground">In the sandbox</dt>
					<dd class="font-mono text-xs leading-5 break-all">/ump/skills/{skill.name}</dd>
				</dl>
				<section class="flex flex-col gap-2">
					<h3 class="text-sm font-medium">Files</h3>
					<ul class="flex flex-col overflow-hidden rounded-lg border">
						{#each skill.files ?? [] as file (file.path)}
							{@const Icon = file.executable ? FileTerminalIcon : FileIcon}
							<li class="border-b last:border-b-0">
								<button
									type="button"
									class="hover:bg-muted/50 aria-pressed:bg-muted flex w-full items-center gap-2 px-3 py-2 text-left"
									aria-pressed={selected === file.path}
									onclick={() => (selected = file.path)}
								>
									<Icon class="text-muted-foreground size-4 shrink-0" />
									<span class="min-w-0 flex-1 truncate font-mono text-xs">{file.path}</span>
									{#if file.executable}<Badge variant="secondary">Executable</Badge>{/if}
									<span class="text-muted-foreground numeric shrink-0 text-xs">
										{formatBytes(file.size)}
									</span>
								</button>
							</li>
						{/each}
					</ul>
				</section>
				{#if selectedFile}
					<section class="flex min-w-0 flex-col gap-2">
						<div class="flex items-center justify-between gap-2">
							<h3 class="min-w-0 truncate font-mono text-sm font-medium">{selectedFile.path}</h3>
							<Button
								variant="ghost"
								size="sm"
								href={skillService.fileUrl(skill.id, selectedFile.path)}
								download
							>
								<DownloadIcon data-icon="inline-start" />
								Download
							</Button>
						</div>
						{#if selectedFile.size > MAX_PREVIEW_BYTES || !isText(selectedFile)}
							<p class="text-muted-foreground text-sm">
								This file can't be shown here. Download it to look at it.
							</p>
						{:else if loading && text === null}
							<div class="text-muted-foreground flex items-center gap-2 text-sm">
								<Spinner /> Loading
							</div>
						{:else if loadError}
							<p class="text-destructive text-sm">{loadError}</p>
						{:else if text !== null}
							{#if selectedFile.path === 'SKILL.md'}
								<!-- Links are focusable on their own, and pressing Enter on one clicks it, so the handler only catches what bubbles up -->
								<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
								<div class="rounded-lg border px-4 py-3" onclick={openLinkedFile}>
									<Markdown source={withoutFrontmatter(text)} />
								</div>
							{:else}
								<CodeEditor
									value={text}
									language={languageOf(selectedFile.path)}
									readonly
									lineWrapping
									label={selectedFile.path}
									class="h-auto max-h-128 rounded-lg border"
								/>
							{/if}
						{/if}
					</section>
				{/if}
			</Sheet.Body>
			<Sheet.Footer class="flex-row justify-end">
				<Button variant="outline" href={skillService.downloadUrl(skill.id)} download>
					<DownloadIcon data-icon="inline-start" />
					Download zip
				</Button>
				{#if skill.sourceUrl}
					<Button variant="outline" isLoading={updating} onclick={() => skill && onUpdate(skill)}>
						<RefreshCwIcon data-icon="inline-start" />
						Update from link
					</Button>
				{/if}
				<Button onclick={() => skill && onReplace(skill)}>
					<UploadIcon data-icon="inline-start" />
					Replace
				</Button>
			</Sheet.Footer>
		{/if}
	</Sheet.Content>
</Sheet.Root>
