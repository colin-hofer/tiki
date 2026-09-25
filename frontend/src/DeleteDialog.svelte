<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { APIError, message } from './api';
  import type { Item } from './api';
  import type { BoardState } from './board.svelte';

  let {
    data,
    item,
    preparing,
    readonly,
    onclose,
    ondeleted,
  }: {
    data: BoardState;
    item: Item;
    preparing: boolean;
    readonly: boolean;
    onclose: () => void;
    ondeleted: (id: string) => Promise<void>;
  } = $props();
  let dialog: HTMLDialogElement;
  let deleting = $state(false);
  let error = $state('');
  let conflict = $state(false);
  const busy = $derived(preparing || deleting);
  onMount(() => dialog.showModal());
  $effect(() => {
    if (!preparing)
      void tick().then(() => dialog.querySelector<HTMLButtonElement>('button')?.focus());
  });
  function close() {
    if (!busy) onclose();
  }
  async function remove() {
    if (busy || readonly || conflict) return;
    deleting = true;
    error = '';
    try {
      await data.remove(item);
      await ondeleted(item.id);
    } catch (cause) {
      conflict = cause instanceof APIError && cause.status === 409;
      error = conflict
        ? 'This ticket changed. Cancel and review the latest version before deleting.'
        : message(cause);
      if (conflict) await data.refresh();
    } finally {
      deleting = false;
    }
  }
</script>

<dialog
  class="dlg delete-dialog"
  bind:this={dialog}
  aria-labelledby="delete-title"
  aria-describedby="delete-description"
  oncancel={(event) => {
    event.preventDefault();
    close();
  }}
>
  <header class="dlg-head"><h2 id="delete-title">Delete TK-{item.id}?</h2></header>
  <div class="dlg-body">
    <div class="dlg-callout danger">
      <strong class="delete-ticket-title">{item.title}</strong>
      <p id="delete-description">
        Deletes the ticket and its activity, discarding unsaved edits. Can't be undone.
      </p>
    </div>
    {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
  </div>
  <footer class="dlg-foot">
    <div class="actions">
      <button class="small-button" disabled={busy} onclick={close}>Cancel</button>
      <button
        class="primary-button danger-button"
        disabled={busy || readonly || conflict}
        onclick={remove}>{busy ? 'Please wait…' : 'Delete ticket'}</button
      >
    </div>
  </footer>
</dialog>
