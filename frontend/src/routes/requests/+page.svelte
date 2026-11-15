<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError, getJSON, postJSON } from '#lib/api.ts';

	type Result = { tmdbId: number; mediaType: 'movie' | 'tv'; title: string; year: string; overview: string; available: boolean; requested: boolean };
	type Req = { id: number; mediaType: string; tmdbId: number; status: string; mediaStatus: string; createdAt: string };
	let q = $state('');
	let results = $state<Result[]>([]);
	let mine = $state<Req[]>([]);
	let message = $state('');
	let unavailable = $state(false);

	async function loadMine() {
		try {
			mine = await getJSON<Req[]>('/api/v1/requests');
		} catch (e) {
			unavailable = e instanceof ApiError && (e.status === 503 || e.status === 502);
		}
	}
	onMount(loadMine);

	async function search(e: SubmitEvent) {
		e.preventDefault();
		message = '';
		try {
			results = await getJSON<Result[]>(`/api/v1/requests/search?q=${encodeURIComponent(q.trim())}`);
			if (!results.length) message = 'No results.';
		} catch (err) {
			message = err instanceof ApiError ? err.message : 'Search failed.';
		}
	}

	async function request(r: Result) {
		try {
			await postJSON('/api/v1/requests', { mediaType: r.mediaType, tmdbId: r.tmdbId });
			message = `Requested ${r.title}.`;
			r.requested = true;
			loadMine();
		} catch (err) {
			message = err instanceof ApiError ? err.message : 'Request failed.';
		}
	}
</script>

<div class="page">
	<h1>Requests</h1>
	{#if unavailable}
		<p class="muted">Requests are not set up on this server yet.</p>
	{:else}
		<form class="row" onsubmit={search}>
			<input placeholder="Search films and shows" bind:value={q} maxlength="100" required aria-label="Search" />
			<button type="submit">Search</button>
		</form>
		{#if message}<p role="status">{message}</p>{/if}
		<ul class="list results">
			{#each results as r (r.mediaType + r.tmdbId)}
				<li>
					<span><strong>{r.title}</strong> <span class="muted">{r.year} · {r.mediaType === 'tv' ? 'Series' : 'Film'}</span></span>
					{#if r.available}<span class="badge">Available</span>
					{:else if r.requested}<span class="badge">Requested</span>
					{:else}<button onclick={() => request(r)}>Request</button>{/if}
				</li>
			{/each}
		</ul>
		<h2 class="section">My requests</h2>
		<ul class="list">
			{#each mine as m (m.id)}
				<li>{m.mediaType} #{m.tmdbId}<span class="muted">{m.status} · {m.mediaStatus.replace('_', ' ')}</span></li>
			{:else}
				<li class="muted">No requests yet.</li>
			{/each}
		</ul>
	{/if}
</div>
