<script lang="ts">
	import { onMount } from 'svelte';
	import { del, getJSON, postJSON } from '#lib/api.ts';
	import { when } from '#lib/format.ts';

	let { admin = false }: { admin?: boolean } = $props();
	type S = { id: string; username: string; userId: string; kind: string; deviceLabel: string; authMethod: string; lastSeenAt: string; current: boolean; role: string };
	let list = $state<S[]>([]);
	let msg = $state('');

	async function load() {
		list = await getJSON<S[]>(admin ? '/api/v1/admin/sessions' : '/api/v1/me/sessions');
	}
	onMount(load);

	async function revoke(s: S) {
		await del(admin ? `/api/v1/admin/sessions/${s.id}` : `/api/v1/me/sessions/${s.id}`);
		msg = 'Session ended.';
		load();
	}
	async function revokeOthers() {
		await postJSON('/api/v1/me/sessions/revoke-others');
		msg = 'Signed out everywhere else.';
		load();
	}
</script>

<section class="card" aria-label="Sessions">
	<h2>{admin ? 'All sessions' : 'Your sessions'}</h2>
	<ul class="list">
		{#each list as s (s.id)}
			<li>
				<span>
					{#if admin}<strong>{s.username}</strong> · {/if}{s.kind === 'milano' ? `Milano ${s.deviceLabel}` : 'Browser'} · {s.authMethod}
					<span class="muted">· last seen {when(s.lastSeenAt)}</span>
				</span>
				{#if s.current}<span class="badge">This device</span>{:else}<button class="ghost" onclick={() => revoke(s)}>End</button>{/if}
			</li>
		{/each}
	</ul>
	{#if !admin}<button class="ghost" onclick={revokeOthers}>Sign out other devices</button>{/if}
	{#if msg}<p role="status">{msg}</p>{/if}
</section>
