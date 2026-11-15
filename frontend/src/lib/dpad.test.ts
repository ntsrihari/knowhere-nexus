import { describe, expect, it } from 'vitest';
import { nextInDirection } from './dpad.ts';

const rect = (x: number, y: number) => ({ left: x, top: y, width: 10, height: 10 }) as DOMRect;

describe('nextInDirection', () => {
	const a = { el: 'right' as unknown as Element, r: rect(20, 0) };
	const b = { el: 'down' as unknown as Element, r: rect(0, 20) };
	const c = { el: 'farright' as unknown as Element, r: rect(60, 2) };
	it('picks the nearest element in the direction', () => {
		expect(nextInDirection(rect(0, 0), [a, b, c], 'ArrowRight')).toBe('right');
		expect(nextInDirection(rect(0, 0), [a, b, c], 'ArrowDown')).toBe('down');
		expect(nextInDirection(rect(0, 0), [a, b, c], 'ArrowLeft')).toBe(null);
	});
});
