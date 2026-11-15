<script lang="ts">
	import { onMount } from 'svelte';
	import { getJSON } from '#lib/api.ts';

	type App = { id: string; name: string; description?: string; icon: string; href: string | null; available: boolean };
	let apps = $state<App[] | null>(null);
	let error = $state('');
	onMount(async () => {
		try {
			apps = await getJSON<App[]>('/api/v1/apps');
		} catch {
			error = 'The app list is unavailable.';
		}
	});
	const initials = (n: string) => n.split(/\s+/).map((p) => p[0]).join('').slice(0, 2).toUpperCase();
</script>

<section aria-label="Apps">
	<h2 class="section">Apps</h2>
	{#if error}<p class="muted">{error}</p>{/if}
	{#if apps}
		<div class="grid tiles">
			{#each apps as a (a.id)}
				{#if a.available && a.href}
					<a class="tile" href={a.href} data-icon={a.icon}><span class="glyph">{initials(a.name)}</span><span>{a.name}</span></a>
				{:else}
					<button class="tile" disabled title="Not reachable through an approved route yet" data-icon={a.icon}>
						<span class="glyph">{initials(a.name)}</span><span>{a.name}</span><small>No approved route</small>
					</button>
				{/if}
			{:else}
				<p class="muted">No apps in the catalog.</p>
			{/each}
		</div>
	{/if}
</section>
