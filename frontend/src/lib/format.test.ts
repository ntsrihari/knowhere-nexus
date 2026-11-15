import { describe, expect, it } from 'vitest';
import { bytes, duration, percent } from './format.ts';

describe('format', () => {
	it('bytes', () => {
		expect(bytes(512)).toBe('512 B');
		expect(bytes(1536)).toBe('1.5 KB');
		expect(bytes(10 * 1024 ** 3)).toBe('10 GB');
		expect(bytes(null)).toBe('—');
	});
	it('duration', () => {
		expect(duration(59)).toBe('0m');
		expect(duration(3700)).toBe('1h 1m');
		expect(duration(90000)).toBe('1d 1h');
	});
	it('percent', () => expect(percent(42.6)).toBe('43%'));
});
