<script lang="ts">
	import '../app.css';
	import type { Snippet } from 'svelte';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';
	import { getJSON } from '#lib/api.ts';
	import { applyTheme, type KnowhereTheme } from '#lib/theme.ts';
	import { dpad } from '#lib/dpad.ts';

	let { children }: { children: Snippet } = $props();

	async function loadTheme() {
		try {
			const r = await getJSON<{ theme: KnowhereTheme }>('/api/v1/themes/effective');
			applyTheme(r.theme);
		} catch {
			/* built-in CSS defaults stay */
		}
	}

	onMount(async () => {
		const s = await session.load();
		if (!s && page.url.pathname !== '/login') {
			goto('/login');
			return;
		}
		if (s) {
			await loadTheme();
			live.connect();
		}
	});

	$effect(() => {
		if (live.revoked) {
			session.current = null;
			live.revoked = false;
			goto('/login');
		}
	});

	async function signOut() {
		live.close();
		await session.logout();
		goto('/login');
	}

	const links = $derived([
		{ href: '/', label: 'Home' },
		{ href: '/requests', label: 'Requests' },
		{ href: '/desktop', label: 'Desktop' },
		{ href: '/settings', label: 'Settings' },
		...(session.isAdmin ? [{ href: '/admin', label: 'Admin' }] : [])
	]);
</script>

<div class="app" use:dpad>
	{#if session.current && page.url.pathname !== '/login'}
		<header class="topbar">
			<a class="brand" href="/">Knowhere</a>
			<nav aria-label="Main">
				{#each links as l (l.href)}
					<a href={l.href} class:active={page.url.pathname === l.href}>{l.label}</a>
				{/each}
			</nav>
			<button class="ghost" onclick={signOut}>Sign out</button>
		</header>
	{/if}
	<main>
		{#if session.current || page.url.pathname === '/login'}
			{@render children()}
		{/if}
	</main>
</div>
