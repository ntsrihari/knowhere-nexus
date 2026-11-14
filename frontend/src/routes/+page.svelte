<script lang="ts">
	import { onMount } from 'svelte';
	import { getJSON, type Health } from '#lib/api.ts';

	let status = $state('checking…');

	onMount(async () => {
		try {
			const h = await getJSON<Health>('/healthz');
			status = h.status;
		} catch {
			status = 'unavailable';
		}
	});
</script>

<main class="shell">
	<h1>Knowhere Nexus</h1>
	<p class="lead">Foundation build (M1). Sign-in, widgets and the launcher arrive in later milestones.</p>
	<p class="status">Server status: <strong data-testid="health-status">{status}</strong></p>
</main>
