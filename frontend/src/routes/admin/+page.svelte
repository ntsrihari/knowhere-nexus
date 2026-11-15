<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError, getJSON, postJSON } from '#lib/api.ts';
	import { session } from '#lib/session.svelte.ts';
	import { when } from '#lib/format.ts';
	import Sessions from '#lib/components/Sessions.svelte';

	type Up = { name: string; configured: boolean; breaker: string; lastSuccessAt: string | null; lastError?: string };
	type Task = { id: string; name: string; category: string; state: string; lastResult: string | null };
	type Entry = { seq: number; at: string; actorName: string; action: string; target: string; result: string };
	type Analytics = { byUser: { user: string; minutes: number }[]; topTitles: { title: string; minutes: number }[]; minutesByHour: number[]; transcodePercent: number; totalMinutes: number };

	let status = $state<{ version: string; integrations: Up[] } | null>(null);
	let tasks = $state<Task[] | null>(null);
	let audit = $state<Entry[]>([]);
	let chain = $state('');
	let stats = $state<Analytics | null>(null);
	let msg = $state('');
	let password = $state('');
	let revokeUser = $state('');

	async function load() {
		status = await getJSON('/api/v1/admin/status');
		audit = await getJSON('/api/v1/admin/audit?limit=50');
		stats = await getJSON('/api/v1/admin/analytics?days=30');
		try {
			tasks = await getJSON('/api/v1/admin/jellyfin/tasks');
		} catch {
			tasks = null;
		}
	}
	onMount(() => {
		if (session.isAdmin) load();
	});

	async function verify() {
		const r = await getJSON<{ ok: boolean; brokenAt: number | null }>('/api/v1/admin/audit/verify');
		chain = r.ok ? 'Audit chain intact.' : `Audit chain broken at entry ${r.brokenAt}.`;
	}
	async function run(t: Task) {
		await postJSON(`/api/v1/admin/jellyfin/tasks/${encodeURIComponent(t.id)}/run`);
		msg = `Started ${t.name}.`;
	}
	async function reload() {
		try {
			const r = await postJSON<{ revision: number; apps: number }>('/api/v1/admin/catalog/reload');
			msg = `Catalog revision ${r.revision} loaded (${r.apps} apps).`;
		} catch (e) {
			msg = e instanceof ApiError ? e.message : 'Reload failed.';
		}
	}
	async function endAllFor(e: SubmitEvent) {
		e.preventDefault();
		try {
			await postJSON('/api/v1/auth/reauth', { password });
			const r = await postJSON<{ revoked: number }>(`/api/v1/admin/users/${encodeURIComponent(revokeUser)}/revoke-sessions`);
			msg = `Ended ${r.revoked} sessions.`;
		} catch (err) {
			msg = err instanceof ApiError ? err.message : 'Failed.';
		} finally {
			password = '';
		}
	}
	const maxHour = $derived(Math.max(1, ...(stats?.minutesByHour ?? [1])));
</script>

<div class="page">
	<h1>Admin</h1>
	{#if !session.isAdmin}
		<p class="muted">Administrator access is required. If you are an admin, sign in again to refresh your role.</p>
	{:else}
		{#if msg}<p role="status">{msg}</p>{/if}
		<div class="grid widgets">
			<section class="card" aria-label="Integrations">
				<h2>Integrations <span class="muted small">v{status?.version}</span></h2>
				<ul class="list">
					{#each status?.integrations ?? [] as u (u.name)}
						<li>{u.name}<span class="badge" data-health={u.configured ? u.breaker : 'off'}>{u.configured ? u.breaker.replace('_', ' ') : 'not configured'}</span></li>
					{/each}
				</ul>
				<button class="ghost" onclick={reload}>Reload catalog</button>
			</section>
			<section class="card" aria-label="Watch statistics">
				<h2>Watching, last 30 days</h2>
				{#if stats}
					<p class="stat">{Math.round(stats.totalMinutes / 60)} h</p>
					<p class="muted">{stats.transcodePercent}% transcoded</p>
					<ul class="list">{#each stats.byUser as u (u.user)}<li>{u.user}<span class="muted">{Math.round(u.minutes)} min</span></li>{/each}</ul>
					<div class="bars" aria-label="Minutes by hour of day">
						{#each stats.minutesByHour as m, h (h)}<span class="bar" style:height={`${(m / maxHour) * 100}%`} title={`${h}:00 · ${Math.round(m)} min`}></span>{/each}
					</div>
					<ul class="list">{#each stats.topTitles as t (t.title)}<li>{t.title}<span class="muted">{Math.round(t.minutes)} min</span></li>{/each}</ul>
				{/if}
			</section>
			<section class="card" aria-label="Jellyfin tasks">
				<h2>Jellyfin tasks</h2>
				{#if tasks}
					<ul class="list">{#each tasks as t (t.id)}<li>{t.name}<span class="muted">{t.state}</span><button class="ghost" onclick={() => run(t)}>Run</button></li>{/each}</ul>
				{:else}
					<p class="muted">Needs a Jellyfin admin key in the secrets file.</p>
				{/if}
			</section>
		</div>
		<Sessions admin />
		<section class="card" aria-label="End a user's sessions">
			<h2>End all sessions for a user</h2>
			<form class="row" onsubmit={endAllFor}>
				<input placeholder="Jellyfin user id" bind:value={revokeUser} required aria-label="User id" />
				<input type="password" placeholder="Your password" bind:value={password} required aria-label="Confirm password" />
				<button type="submit">End sessions</button>
			</form>
		</section>
		<section class="card" aria-label="Audit log">
			<h2>Audit log <button class="ghost" onclick={verify}>Verify chain</button></h2>
			{#if chain}<p role="status">{chain}</p>{/if}
			<ul class="list audit">
				{#each audit as e (e.seq)}<li><span>{when(e.at)} · <strong>{e.actorName || 'system'}</strong> · {e.action}</span><span class="muted">{e.result}</span></li>{/each}
			</ul>
		</section>
	{/if}
</div>
