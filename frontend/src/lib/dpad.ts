// D-pad / arrow-key navigation for TV remotes: moves focus to the nearest
// focusable element in the pressed direction within a container.
const SEL = 'a[href], button:not([disabled]), input, select, [tabindex="0"]';

export function nextInDirection(from: DOMRect, candidates: { el: Element; r: DOMRect }[], key: string) {
	const cx = from.left + from.width / 2;
	const cy = from.top + from.height / 2;
	let best: Element | null = null;
	let bestScore = Infinity;
	for (const { el, r } of candidates) {
		const x = r.left + r.width / 2;
		const y = r.top + r.height / 2;
		const dx = x - cx;
		const dy = y - cy;
		const ok =
			(key === 'ArrowRight' && dx > 1) || (key === 'ArrowLeft' && dx < -1) ||
			(key === 'ArrowDown' && dy > 1) || (key === 'ArrowUp' && dy < -1);
		if (!ok) continue;
		const along = key === 'ArrowLeft' || key === 'ArrowRight' ? Math.abs(dx) : Math.abs(dy);
		const across = key === 'ArrowLeft' || key === 'ArrowRight' ? Math.abs(dy) : Math.abs(dx);
		const score = along + across * 2;
		if (score < bestScore) {
			bestScore = score;
			best = el;
		}
	}
	return best;
}

export function dpad(node: HTMLElement) {
	const onKey = (e: KeyboardEvent) => {
		if (!['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight'].includes(e.key)) return;
		const active = document.activeElement;
		if (!active || !node.contains(active)) return;
		if (active instanceof HTMLInputElement && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) return;
		const all = [...node.querySelectorAll(SEL)].filter((el) => el !== active && (el as HTMLElement).offsetParent !== null);
		const next = nextInDirection(active.getBoundingClientRect(), all.map((el) => ({ el, r: el.getBoundingClientRect() })), e.key);
		if (next) {
			e.preventDefault();
			(next as HTMLElement).focus();
		}
	};
	node.addEventListener('keydown', onKey);
	return { destroy: () => node.removeEventListener('keydown', onKey) };
}
