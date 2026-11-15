<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError, getJSON, putJSON } from '#lib/api.ts';
	import { applyTheme, fontStacks, type KnowhereTheme } from '#lib/theme.ts';
	import { session } from '#lib/session.svelte.ts';

	let theme = $state<KnowhereTheme | null>(null);
	let from = $state('');
	let msg = $state('');
	onMount(async () => {
		const r = await getJSON<{ theme: KnowhereTheme; from: string }>('/api/v1/themes/effective');
		theme = { ...r.theme };
		from = r.from;
	});
	$effect(() => {
		if (theme) applyTheme($state.snapshot(theme) as KnowhereTheme); // live preview
	});
	const doc = () => {
		const t = $state.snapshot(theme) as KnowhereTheme;
		return { schema: 'knowhere.theme', schemaVersion: '0.1.0', name: t.name, seedColor: t.seedColor, colorScheme: t.colorScheme,
			cornerRadius: Number(t.cornerRadius), font: t.font, density: t.density, wallpaper: null };
	};
	async function save(path: string, label: string) {
		try {
			await putJSON(path, doc());
			msg = `Saved as ${label}.`;
		} catch (e) {
			msg = e instanceof ApiError ? e.message : 'Could not save.';
		}
	}
	async function reset() {
		await putJSON('/api/v1/me/theme', null);
		location.reload();
	}
</script>

{#if theme}
	<section class="card" aria-label="Theme">
		<h2>Theme <span class="muted small">(currently from {from})</span></h2>
		<div class="form">
			<label>Colour <input type="color" bind:value={theme.seedColor} /></label>
			<label>Mode
				<select bind:value={theme.colorScheme}><option value="dark">Dark</option><option value="light">Light</option></select>
			</label>
			<label>Corners {theme.cornerRadius}px <input type="range" min="0" max="32" bind:value={theme.cornerRadius} /></label>
			<label>Font
				<select bind:value={theme.font}>{#each Object.keys(fontStacks) as f (f)}<option value={f}>{f}</option>{/each}</select>
			</label>
			<label>Density
				<select bind:value={theme.density}><option value="comfortable">Comfortable</option><option value="compact">Compact</option></select>
			</label>
		</div>
		<div class="row">
			<button onclick={() => save('/api/v1/me/theme', 'your theme')}>Save for me</button>
			<button class="ghost" onclick={reset}>Use household theme</button>
			{#if session.isAdmin}<button class="ghost" onclick={() => save('/api/v1/themes/household/set', 'the household default')}>Set household default</button>{/if}
		</div>
		{#if msg}<p role="status">{msg}</p>{/if}
	</section>
{/if}
