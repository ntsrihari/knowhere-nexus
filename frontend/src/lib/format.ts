export function bytes(n: number | null | undefined): string {
	if (n == null) return '—';
	const u = ['B', 'KB', 'MB', 'GB', 'TB'];
	let i = 0;
	let v = n;
	while (v >= 1024 && i < u.length - 1) {
		v /= 1024;
		i++;
	}
	return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${u[i]}`;
}

export function duration(sec: number): string {
	const d = Math.floor(sec / 86400);
	const h = Math.floor((sec % 86400) / 3600);
	const m = Math.floor((sec % 3600) / 60);
	if (d > 0) return `${d}d ${h}h`;
	if (h > 0) return `${h}h ${m}m`;
	return `${m}m`;
}

export function percent(n: number | null | undefined): string {
	return n == null ? '—' : `${Math.round(n)}%`;
}

export function when(iso: string | null | undefined): string {
	if (!iso) return '—';
	const d = new Date(iso);
	return isNaN(d.getTime()) ? '—' : d.toLocaleString();
}
