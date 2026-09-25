<script lang="ts">
  import { untrack } from 'svelte';
  import { slide } from 'svelte/transition';
  import { api, message } from './api';
  import type { Item, User, Activity, ActivityPage } from './api';
  import { duration } from './motion';
  import Icon from './Icon.svelte';

  let { item, users, deleted }: { item: Item; users: User[]; deleted: boolean } = $props();
  let open = $state(false);
  let activity = $state<Activity[]>([]);
  let cursor = $state('');
  let busy = $state(false);
  let error = $state('');
  let controller: AbortController | undefined;
  let generation = 0;
  $effect(() => {
    void item.version;
    if (open && !deleted) untrack(() => void load());
    return () => {
      controller?.abort();
      generation++;
    };
  });
  async function load(more = false) {
    controller?.abort();
    const own = ++generation;
    const signal = (controller = new AbortController()).signal;
    busy = true;
    error = '';
    try {
      const page = await api<ActivityPage>(
        `/items/${item.id}/activity?limit=50${more && cursor ? `&after=${cursor}` : ''}`,
        'GET',
        undefined,
        signal,
      );
      if (own !== generation) return;
      activity = more ? [...activity, ...page.activity] : page.activity;
      cursor = page.next_after || '';
    } catch (cause) {
      if (!signal.aborted) error = message(cause);
    } finally {
      if (own === generation) busy = false;
    }
  }
</script>

<div class="activity-section">
  <button type="button" class="activity-toggle" aria-expanded={open} onclick={() => (open = !open)}
    ><Icon name={open ? 'down' : 'arrow'} size={14} />Activity</button
  >
  {#if open}<div transition:slide={{ duration: duration(180) }}>
      {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
      <ol class="activity-list">
        {#each activity as event (event.id)}
          <li>
            <span class="activity-node" aria-hidden="true"></span>
            <div>
              <strong
                >{users.find((user) => user.id === event.actor_id)?.name ||
                  `User ${event.actor_id}`}</strong
              >
              <span class="muted">{event.kind.replaceAll('_', ' ').replaceAll('.', ' ')}</span>
              <time datetime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time>
            </div>
          </li>
        {/each}
      </ol>
      {#if cursor}<button
          type="button"
          class="text-button"
          disabled={busy || deleted}
          onclick={() => load(true)}>Load more activity</button
        >{/if}
      {#if busy}<p class="muted">Loading activity…</p>{/if}
    </div>{/if}
</div>
