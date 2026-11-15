// Nexus API client. Every error uses {"error":{"code","message"}} (API_SPEC §0).
// Unsafe methods carry X-Nexus-CSRF (R-16). A 401 triggers one refresh,
// serialised across tabs with the Web Locks API (R-14), then a retry.

export class ApiError extends Error {
	readonly status: number;
	readonly code: string;

	constructor(status: number, code: string, message: string) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
		this.code = code;
	}
}

type Envelope = { error?: { code?: unknown; message?: unknown } };

/** Turns a non-OK response body into an ApiError, tolerating malformed bodies. */
export function toApiError(status: number, body: unknown): ApiError {
	const env = (body ?? {}) as Envelope;
	const code = typeof env.error?.code === 'string' ? env.error.code : 'unknown';
	const message = typeof env.error?.message === 'string' ? env.error.message : `HTTP ${status}`;
	return new ApiError(status, code, message);
}

let refreshing: Promise<boolean> | null = null;

/** Refreshes the session once; concurrent callers (and tabs) share one attempt. */
export function refreshSession(fetchFn: typeof fetch = fetch): Promise<boolean> {
	if (refreshing) return refreshing;
	const run = async () => {
		const res = await fetchFn('/api/v1/auth/refresh', {
			method: 'POST',
			credentials: 'same-origin',
			headers: { 'X-Nexus-CSRF': '1' }
		});
		return res.ok;
	};
	const locks = typeof navigator !== 'undefined' ? (navigator as Navigator & { locks?: LockManager }).locks : undefined;
	refreshing = (locks ? locks.request('nexus-refresh', run) : run()).finally(() => {
		refreshing = null;
	});
	return refreshing;
}

export type RequestOptions = { body?: unknown; retry?: boolean; fetchFn?: typeof fetch };

export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
	if (!path.startsWith('/')) {
		// Only same-origin paths: the client never talks to other hosts (A-02, P-12).
		throw new Error('path must be same-origin');
	}
	const fetchFn = opts.fetchFn ?? fetch;
	const headers: Record<string, string> = { Accept: 'application/json' };
	if (method !== 'GET') headers['X-Nexus-CSRF'] = '1';
	let body: string | undefined;
	if (opts.body !== undefined) {
		headers['Content-Type'] = 'application/json';
		body = JSON.stringify(opts.body);
	}
	const res = await fetchFn(path, { method, credentials: 'same-origin', headers, body });
	if (res.status === 401 && opts.retry !== false && !path.startsWith('/api/v1/auth/')) {
		if (await refreshSession(fetchFn)) return request<T>(method, path, { ...opts, retry: false });
	}
	if (res.status === 204) return undefined as T;
	let data: unknown = null;
	try {
		data = await res.json();
	} catch {
		data = null;
	}
	if (!res.ok) throw toApiError(res.status, data);
	return data as T;
}

export const getJSON = <T>(path: string, fetchFn?: typeof fetch) => request<T>('GET', path, { fetchFn });
export const postJSON = <T>(path: string, body?: unknown) => request<T>('POST', path, { body });
export const putJSON = <T>(path: string, body: unknown) => request<T>('PUT', path, { body });
export const del = (path: string) => request<void>('DELETE', path);

export type Health = { status: string };
export type User = { id: string; name: string; role: 'admin' | 'user' };
export type SessionView = { user: User; role: 'admin' | 'user'; absoluteExpiresAt: string; authMethod: string };
export type Envelope2<T> = { status: 'ok' | 'stale' | 'unavailable' | 'not_configured'; updatedAt: string | null; data: T | null };
