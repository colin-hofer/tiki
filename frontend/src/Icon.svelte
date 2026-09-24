<script lang="ts">
  let { name, size = 18 }: { name: string; size?: number } = $props();
  const paths: Record<string, string> = {
    search: 'm21 21-4.4-4.4M19 10.5a8.5 8.5 0 1 1-17 0 8.5 8.5 0 0 1 17 0',
    plus: 'M12 5v14M5 12h14',
    close: 'm6 6 12 12M6 18 18 6',
    list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
    person: 'M20 21v-2a7 7 0 0 0-14 0v2M17 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0',
    tag: 'M20 13 13 20a2 2 0 0 1-3 0L2 12V3h9l9 7a2 2 0 0 1 0 3ZM7 7h.01',
    arrow: 'm9 5 7 7-7 7',
    refresh: 'M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-2l2 3M4 16l2 3a7 7 0 0 0 12-2',
    check: 'm5 12 4 4L19 6',
    command: 'M9 9V5a2 2 0 1 0-2 2h10a2 2 0 1 0-2-2v14a2 2 0 1 0 2-2H7a2 2 0 1 0 2 2V9',
    logout: 'M9 21H4V3h5M10 12h12m-5-5 5 5-5 5',
    up: 'm6 14 6-6 6 6',
    down: 'm6 10 6 6 6-6',
    code: 'm8 7-5 5 5 5m8-10 5 5-5 5M14 4l-4 16',
    bolt: 'm13 2-9 12h7l-1 8 10-13h-7l1-7',
  };
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={paths[name] || paths.list} /></svg>
