<script lang="ts">
	import { onMount } from 'svelte';
	import Card from './Card.svelte';
	import Meter from './Meter.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { session } from '#lib/session.svelte.ts';
	import { bytes, duration, percent, when } from '#lib/format.ts';

	let { admin = session.isAdmin }: { admin?: boolean } = $props();

	onMount(() => {
		live.fetch('system', '/api/v1/widgets/system');
		live.fetch('storage', '/api/v1/widgets/storage');
		live.fetch('services', '/api/v1/widgets/services');
		live.fetch('upcoming', '/api/v1/widgets/upcoming');
		if (admin) {
			live.fetch('now-playing', '/api/v1/admin/widgets/now-playing');
			live.fetch('downloads', '/api/v1/admin/widgets/downloads');
			live.fetch('queue', '/api/v1/admin/arr/queue');
			live.fetch('photos', '/api/v1/admin/widgets/photos');
			live.fetch('dns', '/api/v1/admin/widgets/dns');
			live.fetch('indexers', '/api/v1/admin/widgets/indexers');
		}
	});
	const w = $derived(live.widgets);
</script>

<div class="grid widgets">
	<Card title="System" env={w.system}>
		{#snippet children(d)}
			<Meter label={`CPU ${percent(d.cpuPercent)}`} value={d.cpuPercent} />
			<Meter label={`Memory ${bytes(d.memUsed)} of ${bytes(d.memTotal)}`} value={d.memUsed} max={d.memTotal} />
			<p class="muted">Load {d.load.map((l: number) => l.toFixed(2)).join(' · ')} · up {duration(d.uptimeSec)}{d.tempC != null ? ` · ${d.tempC.toFixed(0)}°C` : ''}</p>
		{/snippet}
	</Card>
	<Card title="Storage" env={w.storage}>
		{#snippet children(d)}
			{#each d.mounts as m (m.label)}
				{#if m.usedPercent != null}
					<Meter label={`${m.label} · ${percent(m.usedPercent)} used`} value={m.usedPercent} />
				{:else}
					<p><strong>{m.label}</strong> <span class="badge" data-health={m.health}>{m.health}</span></p>
				{/if}
			{/each}
		{/snippet}
	</Card>
	<Card title="Services" env={w.services}>
		{#snippet children(d)}
			<ul class="list">
				{#each d.apps ?? [] as s (s.catalogId)}
					<li><span class="dot" class:up={s.up}></span>{s.name}<span class="muted">{s.up ? 'Up' : 'Down'}</span></li>
				{/each}
			</ul>
		{/snippet}
	</Card>
	<Card title="Coming up" env={w.upcoming}>
		{#snippet children(d)}
			<ul class="list">
				{#each d.items.slice(0, 8) as it, i (i)}
					<li>{it.title}<span class="muted">{new Date(it.date).toLocaleDateString()}</span></li>
				{:else}
					<li class="muted">Nothing scheduled</li>
				{/each}
			</ul>
		{/snippet}
	</Card>
	{#if admin}
		<Card title="Now playing" env={w['now-playing']}>
			{#snippet children(d)}
				<ul class="list">
					{#each d.streams as s, i (i)}
						<li>
							<span><strong>{s.user}</strong> · {s.title}</span>
							<span class="badge" class:warn={s.playMethod === 'transcode'}>{s.playMethod.replace('_', ' ')}{s.paused ? ' · paused' : ''}</span>
						</li>
					{:else}
						<li class="muted">Nobody is watching</li>
					{/each}
				</ul>
			{/snippet}
		</Card>
		<Card title="Downloads" env={w.downloads}>
			{#snippet children(d)}
				{#each d.items.slice(0, 6) as t, i (i)}
					<Meter label={`${t.name} · ${percent(t.progress)} · ${bytes(t.dlSpeed)}/s`} value={t.progress} />
				{:else}
					<p class="muted">No downloads</p>
				{/each}
			{/snippet}
		</Card>
		<Card title="Radarr / Sonarr queue" env={w.queue}>
			{#snippet children(d)}
				{#each d.items.slice(0, 6) as q, i (i)}
					<Meter label={`${q.title} · ${q.status}`} value={q.progress} />
				{:else}
					<p class="muted">Queue is empty</p>
				{/each}
			{/snippet}
		</Card>
		<Card title="Photos" env={w.photos}>
			{#snippet children(d)}<p class="stat">{d.assets.toLocaleString()}</p><p class="muted">{d.photos} photos · {d.videos} videos · {bytes(d.libraryBytes)}</p>{/snippet}
		</Card>
		<Card title="DNS" env={w.dns}>
			{#snippet children(d)}<p class="stat">{d.blockedPercent}%</p><p class="muted">blocked of {d.queries.toLocaleString()} queries</p>{/snippet}
		</Card>
		<Card title="Indexers" env={w.indexers}>
			{#snippet children(d)}<p class="stat">{d.enabled}/{d.total}</p><p class="muted">enabled · {d.failing} failing</p>{/snippet}
		</Card>
	{/if}
</div>
{#if live.notices.length}
	<section class="card notices" aria-label="Notifications">
		<h2>Notifications</h2>
		<ul class="list">
			{#each live.notices as n, i (i)}<li><strong>{n.title}</strong><span class="muted">{n.body}</span></li>{/each}
		</ul>
	</section>
{/if}
<p class="muted small">Live updates {live.connected ? 'connected' : 'reconnecting'} · {when(new Date().toISOString())}</p>
