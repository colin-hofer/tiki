<script lang="ts">
  import { tick } from 'svelte';
  import type { Status } from './api';
  import type { BoardState } from './board-state.svelte';

  let { data, status, onfocus }: { data: BoardState; status: Status; onfocus: () => void } =
    $props();
  let button: HTMLButtonElement;
  const disabled = $derived(data.busy || Boolean(data.writing) || data.loadingMore[status]);

  $effect(() => {
    if (
      disabled ||
      !data.cursors[status] ||
      data.pageErrors[status] ||
      data.error ||
      data.connection === 'paused'
    )
      return;
    // The viewport root also clips offscreen mobile columns and collapsed groups.
    // Re-observe after each page so layout, rather than a stale intersection,
    // decides whether another page is needed to fill the viewport.
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          observer.disconnect();
          void data.more(status);
        }
      },
      {
        rootMargin: '200px 0px',
      },
    );
    observer.observe(button);
    return () => observer.disconnect();
  });

  async function load() {
    const node = button;
    const known = new Set(data.items.map((item) => item.id));
    await data.more(status);
    await tick();
    if (
      document.activeElement !== node &&
      (node.isConnected || document.activeElement !== document.body)
    )
      return;
    const next = data.items.find((item) => item.status === status && !known.has(item.id));
    document.getElementById(`ticket-${next?.id}`)?.focus();
  }
</script>

{#if data.pageErrors[status]}<p class="column-empty" role="status">
    {data.pageErrors[status]}
  </p>{/if}
<button bind:this={button} class="load-more" {disabled} {onfocus} onclick={load}>
  {data.loadingMore[status]
    ? 'Loading…'
    : data.pageErrors[status]
      ? 'Retry loading more'
      : 'Load more'}
</button>
