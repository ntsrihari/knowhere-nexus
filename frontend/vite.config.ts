import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [
		sveltekit({
			// Static SPA served by the Go binary (P-03). Every path that is not
			// a file falls back to index.html; Go serves it with a strict CSP
			// whose script hashes it computes from that file at startup.
			adapter: adapter({
				pages: 'build',
				assets: 'build',
				fallback: 'index.html',
				precompress: false,
				strict: true
			}),
			// Keep all CSS in external files: style-src is 'self' only.
			inlineStyleThreshold: 0
		})
	],
	server: {
		// Development only: forward API calls to a locally running `nexus serve`.
		proxy: {
			'/api': 'http://127.0.0.1:8480',
			'/healthz': 'http://127.0.0.1:8480'
		}
	},
	test: {
		include: ['src/**/*.test.ts'],
		environment: 'node'
	}
});
