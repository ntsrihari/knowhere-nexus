<script lang="ts">
	import { goto } from '$app/navigation';
	import { ApiError, postJSON } from '#lib/api.ts';
	import { session } from '#lib/session.svelte.ts';
	import { live } from '#lib/live.svelte.ts';

	let username = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);
	let qcCode = $state('');
	let qcTimer: ReturnType<typeof setTimeout> | undefined;

	async function done() {
		await session.load();
		live.connect();
		location.assign('/'); // full reload applies the theme and starts fresh
	}

	function explain(e: unknown) {
		if (e instanceof ApiError) {
			if (e.code === 'rate_limited') return 'Too many attempts. Try again in a few minutes.';
			if (e.status === 401) return 'That username or password is not right.';
			if (e.status === 502) return 'Jellyfin is not responding right now.';
			return e.message;
		}
		return 'Something went wrong.';
	}

	async function signIn(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			await postJSON('/api/v1/auth/login', { username, password });
			await done();
		} catch (err) {
			error = explain(err);
		} finally {
			busy = false;
			password = '';
		}
	}

	async function quickConnect() {
		error = '';
		try {
			const r = await postJSON<{ code: string; pollId: string }>('/api/v1/auth/quickconnect');
			qcCode = r.code;
			const poll = async () => {
				const s = await postJSON<{ state: string }>(`/api/v1/auth/quickconnect/${encodeURIComponent(r.pollId)}`);
				if (s.state === 'authorized') return done();
				if (s.state === 'expired') {
					qcCode = '';
					error = 'The Quick Connect code expired. Try again.';
					return;
				}
				qcTimer = setTimeout(poll, 2000);
			};
			qcTimer = setTimeout(poll, 2000);
		} catch (err) {
			error = explain(err);
		}
	}

	$effect(() => () => clearTimeout(qcTimer));
	$effect(() => {
		if (session.current) goto('/');
	});
</script>

<div class="login">
	<h1>Knowhere</h1>
	<p class="muted">Sign in with your Jellyfin account.</p>
	<form onsubmit={signIn} class="card">
		<label>Username <input autocomplete="username" bind:value={username} required maxlength="128" /></label>
		<label>Password <input type="password" autocomplete="current-password" bind:value={password} required maxlength="1024" /></label>
		<button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
		<button type="button" class="ghost" onclick={quickConnect} disabled={!!qcCode}>Use Quick Connect</button>
		{#if qcCode}
			<p class="qc">Enter <strong data-testid="qc-code">{qcCode}</strong> in Jellyfin under Quick Connect. Waiting…</p>
		{/if}
		{#if error}<p class="error" role="alert">{error}</p>{/if}
	</form>
</div>
