// Browser end-to-end check (M1 AC6 and M2–M6 user flows): signs in through
// the real form against the fake Jellyfin, visits every page, and fails on
// any Content-Security-Policy violation, console error or uncaught exception.
// Usage: node e2e/csp-check.mjs http://localhost:18480
import { chromium } from 'playwright';

const base = (process.argv[2] ?? 'http://localhost:8480').replace(/\/$/, '');
// Loopback only: never route the check through an egress proxy.
const browser = await chromium.launch({ args: ['--no-proxy-server'] });
const problems = [];
const ctx = await browser.newContext();
await ctx.addInitScript(() => {
	window.__csp = [];
	document.addEventListener('securitypolicyviolation', (e) => window.__csp.push(`${e.effectiveDirective} blocked ${e.blockedURI || 'inline'}`));
});
const page = await ctx.newPage();
page.on('console', (m) => {
	// HTTP error statuses are handled by the UI and asserted by the flow
	// checks below; anything else logged as an error is a failure.
	if (m.type() === 'error' && !/^Failed to load resource: the server responded with a status of/.test(m.text()))
		problems.push(`console error: ${m.text()}`);
});
page.on('pageerror', (e) => problems.push(`page error: ${e.message}`));

async function check(label, fn) {
	try {
		await fn();
	} catch (e) {
		problems.push(`${label}: ${e.message.split('\n')[0]}`);
	}
	const v = await page.evaluate(() => window.__csp.splice(0));
	for (const x of v) problems.push(`${label}: CSP violation: ${x}`);
}

await check('login page', async () => {
	const resp = await page.goto(base + '/', { waitUntil: 'load' });
	const csp = resp?.headers()['content-security-policy'] ?? '';
	if (!csp || /unsafe-inline|unsafe-eval/.test(csp)) throw new Error(`weak CSP: ${csp}`);
	await page.waitForURL('**/login');
});
await check('wrong password', async () => {
	await page.getByLabel('Username').fill('family');
	await page.getByLabel('Password').fill('nope');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByRole('alert').waitFor();
});
await check('sign in as admin', async () => {
	await page.getByLabel('Username').fill('admin');
	await page.getByLabel('Password').fill('admin');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.waitForFunction(() => document.querySelector('[data-testid="health-status"]')?.textContent === 'ok', null, { timeout: 15000 });
	await page.getByText('Hello, admin').waitFor();
});
await check('home widgets and launcher', async () => {
	await page.getByRole('region', { name: 'System' }).getByText(/CPU/).waitFor({ timeout: 15000 });
	await page.getByRole('region', { name: 'Apps' }).getByText('Jellyfin').waitFor();
});
await check('settings', async () => {
	await page.getByRole('link', { name: 'Settings' }).click();
	await page.getByRole('region', { name: 'Theme' }).waitFor();
	await page.getByRole('region', { name: 'Sessions' }).getByText('This device').waitFor();
	await page.getByLabel('Mode').selectOption('light');
	await page.getByRole('button', { name: 'Save for me' }).click();
	await page.getByText('Saved as your theme.').waitFor();
});
await check('requests (not configured)', async () => {
	await page.getByRole('link', { name: 'Requests' }).click();
	await page.getByText('Requests are not set up on this server yet.').waitFor();
});
await check('admin', async () => {
	await page.getByRole('link', { name: 'Admin' }).click();
	await page.getByRole('region', { name: 'Audit log' }).getByText('auth.login').first().waitFor();
	await page.getByRole('button', { name: 'Verify chain' }).click();
	await page.getByText('Audit chain intact.').waitFor();
});
await check('desktop mode', async () => {
	await page.getByRole('link', { name: 'Desktop' }).click();
	await page.getByRole('dialog', { name: 'Status' }).waitFor();
	await page.getByRole('navigation', { name: 'Dock' }).getByRole('button', { name: 'Apps' }).click();
	await page.getByRole('dialog', { name: 'Apps' }).waitFor();
});
await check('sign out', async () => {
	await page.getByRole('button', { name: 'Sign out' }).click();
	await page.waitForURL('**/login');
});
await check('phone layout', async () => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.getByLabel('Username').fill('family');
	await page.getByLabel('Password').fill('family');
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.getByText('Hello, family').waitFor();
	if (await page.getByRole('link', { name: 'Admin' }).count()) throw new Error('admin link shown to a user');
});

await browser.close();
if (problems.length) {
	console.error('Browser e2e FAILED:\n  ' + problems.join('\n  '));
	process.exit(1);
}
console.log(`Browser e2e passed at ${base}: login, home, settings, requests, admin, desktop, phone layout; no CSP violations`);
