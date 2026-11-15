// Live widget values from /api/v1/events (P-04); REST is the fallback.
import { getJSON, type Envelope2 } from './api.ts';

class Live {
	widgets = $state<Record<string, Envelope2<any>>>({});
	notices = $state<{ kind: string; title: string; body: string }[]>([]);
	connected = $state(false);
	revoked = $state(false);
	#es: EventSource | null = null;

	connect() {
		if (this.#es) return;
		const es = new EventSource('/api/v1/events');
		this.#es = es;
		es.onopen = () => (this.connected = true);
		es.onerror = () => (this.connected = false);
		es.addEventListener('session.revoked', () => {
			this.revoked = true;
			this.close();
		});
		es.addEventListener('notification', (e) => {
			this.notices = [JSON.parse((e as MessageEvent).data), ...this.notices].slice(0, 20);
		});
		const names = ['system', 'storage', 'services', 'now-playing', 'photos', 'dns', 'downloads', 'upcoming', 'queue', 'indexers'];
		for (const n of names) {
			es.addEventListener('widget.' + n, (e) => {
				this.widgets = { ...this.widgets, [n]: JSON.parse((e as MessageEvent).data) };
			});
		}
	}

	close() {
		this.#es?.close();
		this.#es = null;
		this.connected = false;
	}

	/** Loads a widget over REST (admin views and first paint). */
	async fetch(name: string, path: string) {
		try {
			this.widgets = { ...this.widgets, [name]: await getJSON<Envelope2<any>>(path) };
		} catch {
			/* keep the last value */
		}
	}
}

export const live = new Live();
