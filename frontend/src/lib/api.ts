// Minimal API client. Every Nexus error uses the envelope
// {"error":{"code","message"}} (docs/API_SPEC.md §0).

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

export async function getJSON<T>(path: string, fetchFn: typeof fetch = fetch): Promise<T> {
	if (!path.startsWith('/')) {
		// Only same-origin paths: the client never talks to other hosts (A-02, P-12).
		throw new Error('path must be same-origin');
	}
	const res = await fetchFn(path, {
		credentials: 'same-origin',
		headers: { Accept: 'application/json' }
	});
	let body: unknown = null;
	try {
		body = await res.json();
	} catch {
		body = null;
	}
	if (!res.ok) throw toApiError(res.status, body);
	return body as T;
}

export type Health = { status: string };
