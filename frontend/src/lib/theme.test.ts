import { describe, expect, it } from 'vitest';
import { themeVariables, type KnowhereTheme } from './theme.ts';

const base: KnowhereTheme = {
	schema: 'knowhere.theme', schemaVersion: '0.1.0', seedColor: '#6750A4', colorScheme: 'dark',
	cornerRadius: 12, font: 'inter', density: 'comfortable'
};

describe('themeVariables', () => {
	it('produces all Material 3 roles as hex colours', () => {
		const v = themeVariables(base);
		for (const k of ['--md-primary', '--md-on-primary', '--md-surface', '--md-surface-container-high', '--md-outline']) {
			expect(v[k]).toMatch(/^#[0-9a-f]{6}$/);
		}
	});
	it('light and dark schemes differ', () => {
		expect(themeVariables(base)['--md-surface']).not.toBe(themeVariables({ ...base, colorScheme: 'light' })['--md-surface']);
	});
	it('maps shape, density and font', () => {
		const v = themeVariables({ ...base, cornerRadius: 40, density: 'compact', font: 'jetbrains-mono' });
		expect(v['--radius-m']).toBe('32px');
		expect(v['--space']).toBe('0.75rem');
		expect(v['--font']).toContain('JetBrains Mono');
	});
});
