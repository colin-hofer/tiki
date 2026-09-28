<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { avatarHue, initials, label } from './api';
  import type { User, Activity } from './api';
  import type { TimelineState } from './timeline.svelte';
  import { activityKey } from './timeline.svelte';

  let {
    conversation,
    usersById,
    scrollport,
    readonly,
    deleted,
    suspended,
    unread = $bindable(false),
  }: {
    conversation: TimelineState;
    usersById: Map<string, User>;
    scrollport: HTMLElement | undefined;
    readonly: boolean;
    deleted: boolean;
    suspended: boolean;
    unread?: boolean;
  } = $props();
  let end: HTMLDivElement;
  let nearBottom = true;
  let last = '';
  let lastPending = '';
  $effect(() => {
    const node = scrollport;
    if (!node) return;
    const scrolled = () => {
      nearBottom = node.scrollHeight - node.clientHeight - node.scrollTop < 100;
      if (nearBottom) unread = false;
    };
    scrolled();
    node.addEventListener('scroll', scrolled);
    return () => node.removeEventListener('scroll', scrolled);
  });
  $effect(() => {
    const latest = conversation.events.at(-1)?.id || '';
    const pending = conversation.draft?.pending.at(-1)?.client_id || '';
    const loaded = conversation.loaded;
    untrack(() => {
      if ((pending && pending !== lastPending) || (loaded && latest !== last && last !== '')) {
        if (nearBottom || (pending && pending !== lastPending)) void bottom();
        else unread = true;
      }
      if (loaded) last = latest || '0';
      lastPending = pending;
    });
  });
  export async function bottom() {
    await tick();
    end?.scrollIntoView({ block: 'end' });
    unread = false;
  }
  async function older() {
    const node = scrollport;
    const height = node?.scrollHeight || 0;
    const top = node?.scrollTop || 0;
    await conversation.more();
    await tick();
    if (node) node.scrollTop = top + node.scrollHeight - height;
  }
  const author = (id: string) => usersById.get(id)?.name || `User ${id}`;
  type Message = { kind?: string; actor_id?: string; created_at: string };
  // Consecutive messages from one author within five minutes share a bubble group.
  const joins = (a: Message | undefined, b: Message | undefined) =>
    !!a &&
    !!b &&
    (!a.kind || a.kind === 'comment.created') &&
    (!b.kind || b.kind === 'comment.created') &&
    (a.actor_id ?? conversation.userId) === (b.actor_id ?? conversation.userId) &&
    day(a.created_at) === day(b.created_at) &&
    Math.abs(Date.parse(b.created_at) - Date.parse(a.created_at)) < 5 * 60_000;
  const day = (at: string) => new Date(at).toDateString();
  const clock = (at: string) =>
    new Date(at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  function dayLabel(at: string) {
    const date = new Date(at);
    const today = new Date();
    if (day(at) === today.toDateString()) return 'Today';
    today.setDate(today.getDate() - 1);
    if (day(at) === today.toDateString()) return 'Yesterday';
    return date.toLocaleDateString(undefined, {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
      year: date.getFullYear() === new Date().getFullYear() ? undefined : 'numeric',
    });
  }
  const pendingHead = $derived(conversation.draft?.pending[0]);
  const lastConfirmedOwn = $derived.by(() => {
    const latest = conversation.events.at(-1);
    return latest?.kind === 'comment.created' && latest.actor_id === conversation.userId
      ? latest
      : undefined;
  });
  function eventLabel(event: Activity) {
    const changes = event.data?.changes;
    switch (event.kind) {
      case 'item.created':
        return 'created this ticket';
      case 'item.updated': {
        if (!changes) return 'updated this ticket';
        const parts: string[] = [];
        if (changes.status) parts.push(`moved to ${label(changes.status)}`);
        if (changes.type) parts.push(`changed type to ${label(changes.type)}`);
        if (changes.title !== undefined) parts.push('changed the title');
        if (changes.description !== undefined) parts.push('updated the description');
        if (changes.url !== undefined)
          parts.push(changes.url ? 'updated the link' : 'removed the link');
        if (changes.add_tags?.length) parts.push(`added tags: ${changes.add_tags.join(', ')}`);
        if (changes.remove_tags?.length)
          parts.push(`removed tags: ${changes.remove_tags.join(', ')}`);
        if (changes.add_assignees?.length)
          parts.push(`assigned ${changes.add_assignees.map(author).join(', ')}`);
        if (changes.remove_assignees?.length)
          parts.push(`unassigned ${changes.remove_assignees.map(author).join(', ')}`);
        return parts.join(' · ') || 'updated this ticket';
      }
      case 'item.moved':
        return event.data?.move?.status
          ? `moved to ${label(event.data.move.status)}`
          : 'reordered this ticket';
      case 'tag.deleted':
        return `deleted tag ${event.data?.tag || ''}`;
      default:
        return event.kind.replaceAll('_', ' ').replaceAll('.', ' ');
    }
  }
</script>

<section class="comments-section" aria-label="Ticket activity">
  {#if conversation.before}<button
      type="button"
      class="text-button"
      disabled={conversation.loadingOlder || deleted}
      onclick={older}
    >
      {conversation.loadingOlder ? 'Loading…' : 'Load older events'}
    </button>{/if}
  {#if conversation.error}<div class="error-banner" role="alert">
      {conversation.error}
      <button
        type="button"
        class="text-button"
        disabled={conversation.loading || deleted}
        onclick={() => conversation.sync()}>Retry loading activity</button
      >
    </div>{/if}
  {#if conversation.loading && !conversation.loaded}<p class="muted">Loading activity…</p>
  {:else if conversation.loaded && !conversation.events.length && !conversation.draft?.pending.length}<p
      class="comments-empty"
    >
      Start the conversation about this ticket.
    </p>{/if}
  <div
    class="comment-log"
    role="log"
    aria-label="Ticket conversation"
    aria-live={conversation.loaded && !conversation.loadingOlder ? 'polite' : 'off'}
    aria-relevant="additions"
    aria-busy={conversation.loading && !conversation.loaded}
  >
    {#each conversation.events as event, i (activityKey(event))}
      {@const own = event.actor_id === conversation.userId}
      {@const previous = conversation.events[i - 1]}
      {@const next = conversation.events[i + 1] ?? (own ? pendingHead : undefined)}
      {@const start = !joins(previous, event)}
      {@const finish = !joins(event, next)}
      {#if !previous || day(previous.created_at) !== day(event.created_at)}<div
          class="chat-day"
          role="separator"
        >
          <span>{dayLabel(event.created_at)}</span>
        </div>{/if}
      {#if event.kind === 'comment.created'}<article
          class="comment"
          class:own
          class:group-start={start}
          class:group-end={finish}
          data-comment-id={event.id}
        >
          {#if !own}<span
              class="mini-avatar"
              class:placeholder={!start}
              style:--hue={avatarHue(event.actor_id)}
              aria-hidden="true">{start ? initials(author(event.actor_id)) : ''}</span
            >{/if}
          <div class="comment-content">
            {#if start && !own}<div class="comment-meta">
                <strong>{author(event.actor_id)}</strong>
              </div>{/if}
            <div class="comment-bubble">
              <p>{event.data.body}</p>
              <time datetime={event.created_at} title={new Date(event.created_at).toLocaleString()}
                >{clock(event.created_at)}</time
              >
            </div>
          </div>
        </article>
      {:else}<div class="timeline-event" data-activity-id={event.id}>
          <span class="timeline-node" aria-hidden="true"></span>
          <span
            ><strong>{author(event.actor_id)}</strong>
            {eventLabel(event)}
            <time datetime={event.created_at} title={new Date(event.created_at).toLocaleString()}
              >{clock(event.created_at)}</time
            >
          </span>
        </div>{/if}
    {/each}
    {#each conversation.draft?.pending || [] as pending, i (pending.client_id)}
      {@const previous = i ? conversation.draft.pending[i - 1] : lastConfirmedOwn}
      {@const start = !joins(previous, pending)}
      {@const finish = !joins(pending, conversation.draft.pending[i + 1])}
      <article
        class="comment own"
        class:group-start={start}
        class:group-end={finish}
        class:comment-pending={pending.sending}
        class:comment-failed={!pending.sending}
      >
        <div class="comment-content">
          <div class="comment-bubble">
            <p>{pending.body}</p>
            <time datetime={pending.created_at}>{clock(pending.created_at)}</time>
          </div>
          <div class="comment-status">
            {#if pending.sending}<span>Sending…</span>
            {:else}<span class="comment-error" role="status">{pending.error || 'Not sent'}</span
              ><button
                type="button"
                class="text-button"
                disabled={readonly || deleted || suspended}
                onclick={() => conversation.retry(pending)}>Retry sending</button
              >{/if}
          </div>
        </div>
      </article>
    {/each}
  </div>
  <div bind:this={end}></div>
</section>
