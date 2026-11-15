<script lang="ts">
	let { value, max = 100, label }: { value: number; max?: number; label: string } = $props();
</script>

<div class="meter">
	<span>{label}</span>
	<progress {value} {max} aria-label={label}></progress>
</div>
