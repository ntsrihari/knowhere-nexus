<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { Envelope2 } from '#lib/api.ts';

	let { title, env, children }: { title: string; env?: Envelope2<any>; children: Snippet<[any]> } = $props();
	const labels: Record<string, string> = {
		stale: 'Out of date',
		unavailable: 'Unavailable',
		not_configured: 'Not configured'
	};
</script>

<section class="card" aria-label={title}>
	<h2>{title}</h2>
	{#if !env}
		<p class="muted">Loading…</p>
	{:else if env.status === 'not_configured' || env.status === 'unavailable' || env.data == null}
		<p class="muted" data-status={env.status}>{labels[env.status] ?? 'Unavailable'}</p>
	{:else}
		{#if env.status === 'stale'}<p class="badge warn">{labels.stale}</p>{/if}
		{@render children(env.data)}
	{/if}
</section>
