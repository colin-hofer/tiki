<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, APIError } from './api';
  import Icon from './Icon.svelte';

  let { readonly = false, beforeDelete, ondeleted, onclose }:
    { readonly?: boolean; beforeDelete: () => Promise<boolean>; ondeleted: (name: string) => Promise<void>; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let searchInput = $state<HTMLInputElement>(null!);
  let tags = $state<string[]>([]);
  let usage = $state<Record<string, number>>({});
  let search = $state('');
  let loading = $state(true);
  let deleting = $state(false);
  let target = $state<string | null>(null);
  let error = $state('');
  let notice = $state('');
  let visible = $derived(tags.filter(tag => tag.includes(search.trim().toLowerCase())));
  let returnIndex = 0;

  onMount(() => { dialog.showModal(); searchInput.focus(); void load(); });
  async function load() {
    loading = true; error = '';
    try {
      const names: string[] = [];
      const counts: Record<string, number> = Object.create(null);
      let after = '';
      do {
        const page = await api<{ tags: string[]; usage: Record<string, number>; next_after?: string }>(`/tags?limit=200&usage=true${after ? `&after=${encodeURIComponent(after)}` : ''}`);
        names.push(...page.tags); Object.assign(counts, page.usage);
        after = page.next_after || '';
      } while (after);
      tags = names; usage = counts;
    } catch (e) { error = e instanceof Error ? e.message : 'Could not load tags.'; }
    finally { loading = false; }
  }
  async function confirm(name: string) {
    if (readonly || deleting || loading) return;
    returnIndex = visible.indexOf(name); target = name; error = ''; notice = '';
    await tick(); dialog.querySelector<HTMLButtonElement>('.cancel-delete')?.focus();
  }
  async function back() {
    if (deleting) return;
    target = null; error = ''; await tick(); focusRow(returnIndex);
  }
  function focusRow(index: number) {
    const buttons = dialog.querySelectorAll<HTMLButtonElement>('[data-delete-tag]');
    (buttons[Math.min(Math.max(index, 0), buttons.length - 1)] || searchInput)?.focus();
  }
  function close() { if (!deleting) dialog.close(); }
  async function remove() {
    if (target === null || deleting || readonly) return;
    const name = target;
    deleting = true; error = '';
    try {
      if (!(await beforeDelete())) { error = 'Finish or resolve your ticket draft before deleting tags.'; return; }
      try { await api('/tags', 'DELETE', { name }); }
      catch (e) { if (!(e instanceof APIError && e.status === 404)) throw e; }
      tags = tags.filter(tag => tag !== name); target = null;
      notice = `Deleted #${name}. Tickets were kept.`;
      await ondeleted(name);
    } catch (e) { error = e instanceof Error ? e.message : 'Could not delete this tag.'; }
    finally { deleting = false; await tick(); if (target === null) focusRow(returnIndex); }
  }
  function keydown(event: KeyboardEvent) {
    if (target !== null || event.isComposing || event.ctrlKey || event.metaKey || event.altKey || deleting) return;
    if (event.target === searchInput) {
      if (event.key === 'ArrowDown') { event.preventDefault(); focusRow(0); }
      return;
    }
    const buttons = [...dialog.querySelectorAll<HTMLButtonElement>('[data-delete-tag]')];
    const index = buttons.indexOf(event.target as HTMLButtonElement);
    if (index < 0) return;
    if (['ArrowDown', 'j', 'ArrowUp', 'k', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      focusRow(event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : index + (['ArrowDown', 'j'].includes(event.key) ? 1 : -1));
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog class="dlg" bind:this={dialog} aria-labelledby="tags-heading" onclose={onclose} onkeydown={keydown} oncancel={event => { event.preventDefault(); if (target !== null) void back(); else close(); }}>
  <header class="dlg-head">
    {#if target !== null}<button class="icon-button dlg-back" aria-label="Back to tags" disabled={deleting} onclick={back}><Icon name="back" size={16} /></button>{/if}
    <h2 id="tags-heading">{target === null ? 'Manage tags' : 'Delete tag?'}</h2>
    {#if target === null}<button class="icon-button" aria-label="Refresh tags" title="Refresh" disabled={loading || deleting} onclick={load}><Icon name="refresh" size={15} /></button>{/if}
    <button class="icon-button" aria-label="Close tag manager" disabled={deleting} onclick={close}><Icon name="close" size={16} /></button>
  </header>
  {#if target !== null}
    <div class="dlg-body">
      {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
      <div class="dlg-callout danger">
        <div class="row-main"><strong class="tag-name">#{target}</strong><span class="meta">{usage[target] ?? 0} {(usage[target] ?? 0) === 1 ? 'ticket' : 'tickets'}</span></div>
        <p>Removes the tag from every ticket and the workspace. Tickets and history are kept. Can't be undone.</p>
      </div>
    </div>
    <footer class="dlg-foot"><div class="actions"><button class="small-button cancel-delete" disabled={deleting} onclick={back}>Cancel</button><button class="primary-button danger-button" disabled={deleting || readonly} onclick={remove}>{deleting ? 'Deleting…' : 'Delete tag'}</button></div></footer>
  {:else}
    <div class="dlg-body">
      <label class="dlg-search"><Icon name="search" size={14} /><input bind:this={searchInput} aria-label="Search tags" placeholder="Filter tags…" bind:value={search} /></label>
      {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
      {#if notice}<p class="dlg-status" role="status">{notice}</p>{/if}
      {#if loading}<p class="dlg-empty" role="status">Loading tags…</p>{:else}
        <ul class="dlg-list" aria-label="Workspace tags">{#each visible as name (name)}<li><div class="row-main"><strong class="tag-name">#{name}</strong></div><span class="meta">{usage[name] ?? 0} {(usage[name] ?? 0) === 1 ? 'ticket' : 'tickets'}</span><button class="icon-button danger" data-delete-tag={name} aria-label={`Delete tag ${name}`} title="Delete tag" disabled={deleting || readonly} onclick={() => confirm(name)}><Icon name="trash" size={14} /></button></li>{/each}</ul>
        {#if !visible.length}<p class="dlg-empty">{search ? 'No matching tags.' : 'No tags yet.'}</p>{/if}
      {/if}
    </div>
    <footer class="dlg-foot"><span class="meta">{tags.length} {tags.length === 1 ? 'tag' : 'tags'}</span><span class="note actions">Deleting keeps tickets</span></footer>
  {/if}
</dialog>

<style>
  .tag-name { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; }
  .dlg-callout .tag-name { font-size: 13px; }
</style>
