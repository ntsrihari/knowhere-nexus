import { getJSON, postJSON, type SessionView } from './api.ts';

class SessionState {
	current = $state<SessionView | null>(null);
	loaded = $state(false);

	get isAdmin() {
		return this.current?.role === 'admin';
	}

	async load() {
		try {
			this.current = await getJSON<SessionView>('/api/v1/auth/session');
		} catch {
			this.current = null;
		}
		this.loaded = true;
		return this.current;
	}

	async logout() {
		try {
			await postJSON('/api/v1/auth/logout');
		} finally {
			this.current = null;
		}
	}
}

export const session = new SessionState();
