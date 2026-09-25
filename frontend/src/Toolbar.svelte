<script lang="ts">
  import { tick } from 'svelte';
  import { statuses, label, initials, avatarHue } from './api';
  import type { User } from './api';
  import type { BoardState } from './board.svelte';
  import Select from './Select.svelte';
  import Icon from './Icon.svelte';
  let {
    data,
    user,
    mobile,
    readonly,
    accountOpen,
    query = $bindable(''),
    onfilters,
    onquery,
    onclear,
    onpeople,
    oninstall,
    oncommands,
    onhelp,
    onaccount,
    ontags,
  }: {
    data: BoardState;
    user: User;
    mobile: boolean;
    readonly: boolean;
    accountOpen: boolean;
    query?: string;
    onfilters: () => void;
    onquery: () => void;
    onclear: () => void;
    onpeople: () => void;
    oninstall: () => void;
    oncommands: () => void;
    onhelp: () => void;
    onaccount: () => void;
    ontags: () => void;
  } = $props();
  const users = $derived(data.users);
  const tags = $derived(data.tags);
  const activeFilters = $derived(Object.values(data.filters).filter(Boolean).length);
  let searchInput: HTMLInputElement;
  let searchOpen = $state(false);
  let filtersOpen = $state(false);
  $effect(() => {
    if (!mobile) {
      filtersOpen = false;
      searchOpen = false;
    }
  });
  export async function focusSearch(select = false) {
    searchOpen = true;
    await tick();
    searchInput.focus();
    if (select) searchInput.select();
  }
  function closeSearch() {
    query = '';
    searchOpen = false;
    onquery();
  }
</script>

<div class="toolbar">
  <span class="wordmark"><span class="logo-mark" aria-hidden="true"></span>tiki</span>
  <div class="search-field" class:open={searchOpen || Boolean(query)}>
    <Icon name="search" size={14} /><input
      aria-label="Search loaded items"
      placeholder="Filter loaded items…"
      bind:value={query}
      bind:this={searchInput}
      oninput={onquery}
      onblur={() => {
        if (!query) searchOpen = false;
      }}
    /><kbd>/</kbd><button
      class="icon-button search-close mobile-only"
      aria-label="Close search"
      onmousedown={(event) => event.preventDefault()}
      onclick={closeSearch}><Icon name="close" size={16} /></button
    >
  </div>
  <div class="filters" class:open={filtersOpen} inert={mobile && !filtersOpen}>
    <div class="sheet-header mobile-only">
      <h2>Filters</h2>
      <button class="text-button" onclick={() => (filtersOpen = false)}>Done</button>
    </div>
    <Select
      label="Filter assignee"
      title="Assignee"
      variant="filter"
      placeholder="Assignee"
      placeholderIcon="person"
      bind:value={data.filters.assignee}
      onchange={onfilters}
      options={[
        { value: '', label: 'Any assignee', icon: 'person' },
        { value: 'none', label: 'Unassigned', icon: 'unassigned' },
        ...users.map((u) => ({
          value: u.id,
          label: u.removed_at
            ? `${u.name} (removed)`
            : u.id === user?.id
              ? `${u.name} (me)`
              : u.name,
          avatar: initials(u.name),
          avatarHue: avatarHue(u.id),
        })),
      ]}
    />
    <Select
      label="Filter tag"
      title="Tag"
      variant="filter"
      placeholder="Tag"
      placeholderIcon="tag"
      bind:value={data.filters.tag}
      onchange={onfilters}
      options={[
        { value: '', label: 'Any tag', icon: 'tag' },
        ...tags.map((tag) => ({ value: tag, label: tag, icon: 'hash' })),
        ...(!readonly
          ? [{ value: '\u0000manage-tags', label: 'Manage tags…', icon: 'tag', action: ontags }]
          : []),
      ]}
    />
    <Select
      label="Filter status"
      title="Status"
      variant="filter"
      placeholder="Status"
      placeholderIcon="status"
      bind:value={data.filters.status}
      onchange={onfilters}
      options={[
        { value: '', label: 'Any status', icon: 'status' },
        ...statuses.map((status) => ({
          value: status,
          label: label(status),
          icon: status,
          iconClass: `status-icon ${status}`,
        })),
      ]}
    />
    {#if data.filters.tag || data.filters.assignee || data.filters.status || query}<button
        class="icon-button"
        aria-label="Clear filters"
        title="Clear filters"
        onclick={onclear}
        ><Icon name="clear-filter" size={15} /><span class="mobile-only">Clear all</span></button
      >{/if}
  </div>
  <span class="toolbar-spacer"></span>
  {#if data.orderChanged}<button
      class="text-button order-notice"
      onclick={() => data.refresh({ order: true })}>Apply order</button
    >{/if}
  <span
    class={`connection ${data.connection}`}
    title={`Updates arrive live. ${data.lastSync ? `Last sync ${data.lastSync}.` : ''}`}
    ><span class="tiny-dot"></span><span
      >{data.connection === 'live'
        ? 'Live'
        : data.connection === 'offline'
          ? 'Offline'
          : data.connection === 'paused'
            ? 'Paused'
            : 'Syncing'}</span
    ></span
  >
  <button class="icon-button mobile-only" aria-label="Search" onclick={() => focusSearch()}
    ><Icon name="search" size={18} /></button
  >
  <button
    class="icon-button mobile-only filter-toggle"
    aria-label="Filters"
    aria-expanded={filtersOpen}
    onclick={() => (filtersOpen = !filtersOpen)}
    ><Icon name="filter" size={18} />{#if activeFilters}<span class="badge">{activeFilters}</span
      >{/if}</button
  >
  <button
    class="icon-button desktop-only"
    aria-label="Refresh board"
    title="Refresh (R)"
    disabled={data.busy}
    onclick={() => data.refresh({ order: true })}><Icon name="refresh" size={15} /></button
  >
  {#if user.role === 'admin'}<button
      class="icon-button"
      aria-label="Manage people"
      title="Manage people"
      onclick={onpeople}><Icon name="add-person" size={17} /></button
    >{/if}
  <button
    class="icon-button desktop-only"
    aria-label="CLI & agents"
    title="CLI & agents"
    onclick={oninstall}><Icon name="terminal" size={17} /></button
  >
  <button
    class="icon-button"
    aria-label="Commands"
    title="Commands (Ctrl/Cmd+K)"
    onclick={oncommands}
    ><span class="desktop-only"><Icon name="command" size={15} /></span><span class="mobile-only"
      ><Icon name="more" size={18} /></span
    ></button
  >
  <button
    class="icon-button desktop-only"
    aria-label="Keyboard shortcuts"
    title="Keyboard shortcuts (?)"
    onclick={onhelp}><Icon name="keyboard" size={16} /></button
  >
  <button
    class="icon-button"
    aria-label="Account menu"
    aria-haspopup="dialog"
    aria-expanded={accountOpen}
    title={user.name}
    onclick={onaccount}
    ><span class="mini-avatar account-avatar" style:--hue={avatarHue(user.id)} aria-hidden="true"
      >{initials(user.name)}</span
    ></button
  >
</div>
{#if filtersOpen}<button
    class="sheet-backdrop"
    aria-label="Close filters"
    tabindex="-1"
    onclick={() => (filtersOpen = false)}
  ></button>{/if}
