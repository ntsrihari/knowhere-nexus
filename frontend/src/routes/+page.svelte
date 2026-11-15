<script lang="ts">
	import Launcher from '#lib/components/Launcher.svelte';
	import Widgets from '#lib/components/Widgets.svelte';
	import { session } from '#lib/session.svelte.ts';
	import { getJSON, type Health } from '#lib/api.ts';
	import { onMount } from 'svelte';

	let status = $state('checking…');
	onMount(async () => {
		try {
			status = (await getJSON<Health>('/healthz')).status;
		} catch {
			status = 'unavailable';
		}
	});
</script>

<div class="page">
	<h1>Hello, {session.current?.user.name}</h1>
	<p class="muted">Server status: <strong data-testid="health-status">{status}</strong></p>
	<Launcher />
	<h2 class="section">Status</h2>
	<Widgets />
</div>
