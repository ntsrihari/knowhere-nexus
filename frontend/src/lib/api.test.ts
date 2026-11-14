import { describe, expect, it } from 'vitest';
import { ApiError, getJSON, toApiError } from './api.ts';

function fakeFetch(status: number, body: string): typeof fetch {
	return (async () => new Response(body, { status, headers: { 'Content-Type': 'application/json' } })) as typeof fetch;
}

describe('toApiError', () => {
	it('reads the envelope', () => {
		const e = toApiError(404, { error: { code: 'not_found', message: 'not found' } });
		expect(e).toBeInstanceOf(ApiError);
		expect(e.code).toBe('not_found');
		expect(e.status).toBe(404);
	});
	it('tolerates malformed bodies', () => {
		const e = toApiError(500, { error: { code: 5 } });
		expect(e.code).toBe('unknown');
		expect(e.message).toBe('HTTP 500');
		expect(toApiError(502, null).code).toBe('unknown');
	});
});

describe('getJSON', () => {
	it('returns parsed JSON on success', async () => {
		await expect(getJSON('/healthz', fakeFetch(200, '{"status":"ok"}'))).resolves.toEqual({ status: 'ok' });
	});
	it('throws ApiError on failure', async () => {
		await expect(getJSON('/api/x', fakeFetch(404, '{"error":{"code":"not_found","message":"nope"}}'))).rejects.toMatchObject({
			code: 'not_found',
			status: 404
		});
	});
	it('handles a non-JSON error body', async () => {
		await expect(getJSON('/api/x', fakeFetch(502, '<html>'))).rejects.toMatchObject({ code: 'unknown', status: 502 });
	});
	it('refuses cross-origin URLs', async () => {
		await expect(getJSON('https://evil.example/x', fakeFetch(200, '{}'))).rejects.toThrow('same-origin');
	});
});
