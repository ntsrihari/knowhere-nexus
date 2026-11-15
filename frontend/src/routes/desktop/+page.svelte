<script lang="ts">
	import { onMount } from 'svelte';
	import { getJSON, putJSON } from '#lib/api.ts';
	import Widgets from '#lib/components/Widgets.svelte';
	import Launcher from '#lib/components/Launcher.svelte';
	import Sessions from '#lib/components/Sessions.svelte';
	import ThemeEditor from '#lib/components/ThemeEditor.svelte';

	type Win = { app: string; x: number; y: number; w: number; h: number; z: number; minimized: boolean };
	const apps: Record<string, string> = { launcher: 'Apps', widgets: 'Status', sessions: 'Sessions', theme: 'Theme' };
	let windows = $state<Win[]>([]);
	let saveTimer: ReturnType<typeof setTimeout> | undefined;

	onMount(async () => {
		const r = await getJSON<{ windows: Win[] }>('/api/v1/me/desktop-layout');
		windows = r.windows.filter((w) => w.app in apps);
		if (!windows.length) open('widgets');
	});

	function persist() {
		clearTimeout(saveTimer);
		saveTimer = setTimeout(() => putJSON('/api/v1/me/desktop-layout', { windows: $state.snapshot(windows) }), 500);
	}
	const top = () => Math.max(0, ...windows.map((w) => w.z)) + 1;
	function open(app: string) {
		const existing = windows.find((w) => w.app === app);
		if (existing) {
			existing.minimized = false;
			existing.z = top();
		} else {
			const n = windows.length;
			windows.push({ app, x: 40 + n * 30, y: 40 + n * 30, w: 520, h: 420, z: top(), minimized: false });
		}
		persist();
	}
	function close(w: Win) {
		windows = windows.filter((x) => x !== w);
		persist();
	}
	function drag(e: PointerEvent, w: Win, mode: 'move' | 'resize') {
		const sx = e.clientX, sy = e.clientY, ox = mode === 'move' ? w.x : w.w, oy = mode === 'move' ? w.y : w.h;
		w.z = top();
		const target = e.currentTarget as HTMLElement;
		target.setPointerCapture(e.pointerId);
		const move = (ev: PointerEvent) => {
			if (mode === 'move') {
				w.x = Math.max(0, ox + ev.clientX - sx);
				w.y = Math.max(0, oy + ev.clientY - sy);
			} else {
				w.w = Math.max(280, ox + ev.clientX - sx);
				w.h = Math.max(200, oy + ev.clientY - sy);
			}
		};
		const up = () => {
			target.removeEventListener('pointermove', move);
			target.removeEventListener('pointerup', up);
			persist();
		};
		target.addEventListener('pointermove', move);
		target.addEventListener('pointerup', up);
	}
</script>

<div class="desktop">
	{#each windows as w (w.app)}
		{#if !w.minimized}
			<div class="window" role="dialog" aria-label={apps[w.app]} style:left={`${w.x}px`} style:top={`${w.y}px`} style:width={`${w.w}px`} style:height={`${w.h}px`} style:z-index={w.z}>
				<div class="titlebar" role="toolbar" tabindex="-1" onpointerdown={(e) => drag(e, w, 'move')}>
					<span>{apps[w.app]}</span>
					<span class="row">
						<button class="ghost" aria-label="Minimise" onpointerdown={(e) => e.stopPropagation()} onclick={() => { w.minimized = true; persist(); }}>–</button>
						<button class="ghost" aria-label="Close" onpointerdown={(e) => e.stopPropagation()} onclick={() => close(w)}>×</button>
					</span>
				</div>
				<div class="content">
					{#if w.app === 'widgets'}<Widgets />{:else if w.app === 'launcher'}<Launcher />{:else if w.app === 'sessions'}<Sessions />{:else}<ThemeEditor />{/if}
				</div>
				<span class="grip" role="presentation" onpointerdown={(e) => drag(e, w, 'resize')}></span>
			</div>
		{/if}
	{/each}
	<nav class="dock" aria-label="Dock">
		{#each Object.entries(apps) as [id, label] (id)}
			<button class:open={windows.some((w) => w.app === id && !w.minimized)} onclick={() => open(id)}>{label}</button>
		{/each}
	</nav>
</div>
