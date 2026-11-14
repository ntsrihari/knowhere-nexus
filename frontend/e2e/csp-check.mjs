// Loads Nexus pages in Chromium and fails on any Content-Security-Policy
// violation, console error or uncaught exception (M1 acceptance criterion 6).
// Usage: node e2e/csp-check.mjs http://127.0.0.1:8480
import { chromium } from 'playwright';

const base = (process.argv[2] ?? 'http://127.0.0.1:8480').replace(/\/$/, '');
// A deep path must be served index.html by Go (HTTP 200); SvelteKit then
// shows its own client-side "not found" page, since M1 has no such route.
const pages = [
	{ path: '/', expectHealth: true },
	{ path: '/some/client/route', expectHealth: false }
];

const browser = await chromium.launch();
const problems = [];
try {
	for (const { path: p, expectHealth } of pages) {
		const page = await browser.newPage();
		await page.addInitScript(() => {
			window.__cspViolations = [];
			document.addEventListener('securitypolicyviolation', (e) => {
				window.__cspViolations.push(`${e.effectiveDirective} blocked ${e.blockedURI || 'inline'}`);
			});
		});
		page.on('console', (m) => {
			if (m.type() === 'error') problems.push(`${p}: console error: ${m.text()}`);
		});
		page.on('pageerror', (e) => problems.push(`${p}: page error: ${e.message}`));

		const resp = await page.goto(base + p, { waitUntil: 'networkidle' });
		if (!resp || resp.status() !== 200) problems.push(`${p}: status ${resp?.status()}`);
		const csp = resp?.headers()['content-security-policy'] ?? '';
		if (!csp || /unsafe-inline|unsafe-eval/.test(csp)) problems.push(`${p}: weak or missing CSP: ${csp}`);

		if (expectHealth) {
			try {
				await page.waitForFunction(
					() => document.querySelector('[data-testid="health-status"]')?.textContent === 'ok',
					null,
					{ timeout: 10000 }
				);
			} catch {
				const text = await page.evaluate(() => document.body.innerText.slice(0, 300));
				problems.push(`${p}: page did not render a healthy status; body text: ${JSON.stringify(text)}`);
			}
		}
		const violations = await page.evaluate(() => window.__cspViolations);
		for (const v of violations) problems.push(`${p}: CSP violation: ${v}`);
		await page.close();
	}
} finally {
	await browser.close();
}

if (problems.length) {
	console.error('CSP/browser check FAILED:\n  ' + problems.join('\n  '));
	process.exit(1);
}
console.log(`CSP/browser check passed for ${pages.length} pages at ${base}`);
