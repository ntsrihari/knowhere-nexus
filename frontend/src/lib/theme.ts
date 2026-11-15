// Theme runtime (P-14): knowhere.theme JSON -> Material 3 colour roles ->
// CSS custom properties set through the CSSOM (no inline style attributes).
import { argbFromHex, Hct, hexFromArgb, SchemeTonalSpot } from '@material/material-color-utilities';

export type KnowhereTheme = {
	schema: 'knowhere.theme';
	schemaVersion: string;
	name?: string;
	seedColor: string;
	colorScheme: 'light' | 'dark';
	cornerRadius: number;
	font: string;
	density: 'comfortable' | 'compact';
	wallpaper?: { ref: string } | null;
};

const roles = [
	'primary', 'onPrimary', 'primaryContainer', 'onPrimaryContainer',
	'secondary', 'onSecondary', 'secondaryContainer', 'onSecondaryContainer',
	'tertiary', 'onTertiary', 'tertiaryContainer', 'onTertiaryContainer',
	'error', 'onError', 'errorContainer', 'onErrorContainer',
	'background', 'onBackground', 'surface', 'onSurface', 'surfaceVariant', 'onSurfaceVariant',
	'surfaceContainerLowest', 'surfaceContainerLow', 'surfaceContainer', 'surfaceContainerHigh', 'surfaceContainerHighest',
	'outline', 'outlineVariant', 'inverseSurface', 'inverseOnSurface', 'inversePrimary', 'scrim', 'shadow'
] as const;

const kebab = (s: string) => s.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase());

export const fontStacks: Record<string, string> = {
	inter: "'Inter', system-ui, sans-serif",
	roboto: "'Roboto', system-ui, sans-serif",
	'roboto-flex': "'Roboto Flex', 'Roboto', system-ui, sans-serif",
	'jetbrains-mono': "'JetBrains Mono', ui-monospace, monospace"
};

/** Computes the CSS custom properties for a theme. */
export function themeVariables(t: KnowhereTheme): Record<string, string> {
	const scheme = new SchemeTonalSpot(Hct.fromInt(argbFromHex(t.seedColor)), t.colorScheme === 'dark', 0);
	const vars: Record<string, string> = {};
	for (const r of roles) {
		const v = (scheme as unknown as Record<string, number>)[r];
		if (typeof v === 'number') vars[`--md-${kebab(r)}`] = hexFromArgb(v);
	}
	const r = Math.max(0, Math.min(32, t.cornerRadius));
	vars['--radius-s'] = `${Math.round(r / 2)}px`;
	vars['--radius-m'] = `${r}px`;
	vars['--radius-l'] = `${Math.round(r * 1.5)}px`;
	vars['--space'] = t.density === 'compact' ? '0.75rem' : '1rem';
	vars['--font'] = fontStacks[t.font] ?? "'Inter', system-ui, sans-serif";
	return vars;
}

export function applyTheme(t: KnowhereTheme, el: HTMLElement = document.documentElement) {
	for (const [k, v] of Object.entries(themeVariables(t))) el.style.setProperty(k, v);
	el.dataset.scheme = t.colorScheme;
	document.querySelector('meta[name="color-scheme"]')?.setAttribute('content', t.colorScheme);
}
