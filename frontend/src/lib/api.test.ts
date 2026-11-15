import { describe, expect, it, vi } from 'vitest';
import { ApiError, getJSON, request, toApiError } from './api.ts';

function reply(status: number, body: string): Response {
	return new Response(status === 204 ? null : body, { status, headers: { 'Content-Type': 'application/json' } });
}

describe('toApiError', () => {
	it('reads the envelope', () => {
		const e = toApiError(404, { error: { code: 'not_found', message: 'not found' } });
		expect(e).toBeInstanceOf(ApiError);
		expect(e.code).toBe('not_found');
	});
	it('tolerates malformed bodies', () => {
		expect(toApiError(500, { error: { code: 5 } }).code).toBe('unknown');
		expect(toApiError(502, null).message).toBe('HTTP 502');
	});
});

describe('request', () => {
	it('returns JSON and sends CSRF only on unsafe methods', async () => {
		const f = vi.fn(async (_p: RequestInfo | URL, init?: RequestInit) => {
			const h = init?.headers as Record<string, string>;
			return reply(200, JSON.stringify({ csrf: h['X-Nexus-CSRF'] ?? null }));
		});
		await expect(getJSON('/healthz', f as unknown as typeof fetch)).resolves.toEqual({ csrf: null });
		await expect(request('POST', '/api/v1/x', { fetchFn: f as unknown as typeof fetch })).resolves.toEqual({ csrf: '1' });
	});
	it('refreshes once on 401 and retries', async () => {
		const calls: string[] = [];
		const f = vi.fn(async (p: RequestInfo | URL) => {
			calls.push(String(p));
			if (String(p) === '/api/v1/auth/refresh') return reply(200, '{}');
			return calls.filter((c) => c === '/api/v1/me').length === 1 ? reply(401, '{}') : reply(200, '{"ok":true}');
		});
		await expect(getJSON('/api/v1/me', f as unknown as typeof fetch)).resolves.toEqual({ ok: true });
		expect(calls).toEqual(['/api/v1/me', '/api/v1/auth/refresh', '/api/v1/me']);
	});
	it('throws ApiError when refresh fails', async () => {
		const f = vi.fn(async (p: RequestInfo | URL) => (String(p).endsWith('refresh') ? reply(401, '{}') : reply(401, '{"error":{"code":"unauthenticated","message":"x"}}')));
		await expect(getJSON('/api/v1/me', f as unknown as typeof fetch)).rejects.toMatchObject({ status: 401, code: 'unauthenticated' });
	});
	it('refuses cross-origin URLs', async () => {
		await expect(getJSON('https://evil.example/x')).rejects.toThrow('same-origin');
	});
});
