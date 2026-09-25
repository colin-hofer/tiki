<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Icon from './Icon.svelte';
  let {
    actions,
    onclose,
    title = 'Commands',
  }: {
    actions: { id: string; label: string; hint?: string; run: () => void }[];
    onclose: () => void | Promise<void>;
    title?: string;
  } = $props();
  let query = $state('');
  let index = $state(0);
  let dialog: HTMLDialogElement;
  let input: HTMLInputElement;
  let matches = $derived(
    actions.filter((a) => a.label.toLowerCase().includes(query.toLowerCase())),
  );
  onMount(() => {
    dialog.showModal();
    input.focus();
  });
  async function run(i: number) {
    const action = matches[i];
    if (action) {
      await onclose();
      action.run();
    }
  }
  async function keydown(event: KeyboardEvent) {
    if (event.isComposing || event.target !== input) return;
    const down =
      event.key === 'ArrowDown' ||
      (event.ctrlKey && ['j', 'n'].includes(event.key)) ||
      (event.altKey && event.key === 'j');
    const up =
      event.key === 'ArrowUp' ||
      (event.ctrlKey && ['k', 'p'].includes(event.key)) ||
      (event.altKey && event.key === 'k');
    if (down || up) {
      event.preventDefault();
      event.stopPropagation();
      index = (index + (down ? 1 : -1) + matches.length) % (matches.length || 1);
      await tick();
      document.getElementById(`command-${index}`)?.scrollIntoView({ block: 'nearest' });
    } else if (event.key === 'Enter') {
      event.preventDefault();
      run(index);
    }
  }
</script>

<!-- The dialog owns its keyboard scope and native focus trap. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog
  class="command-dialog"
  bind:this={dialog}
  oncancel={onclose}
  onkeydown={keydown}
  aria-label={title}
>
  <div class="command-search">
    <Icon name="search" size={16} /><input
      bind:this={input}
      bind:value={query}
      oninput={() => (index = 0)}
      placeholder="Filter commands…"
      aria-label="Find a command"
      role="combobox"
      aria-autocomplete="list"
      aria-expanded="true"
      aria-controls="command-results"
      aria-activedescendant={matches.length ? `command-${index}` : undefined}
    /><button class="key-button" onclick={onclose}>esc</button>
  </div>
  <div class="command-caption">{title} <span>{matches.length}</span></div>
  <div id="command-results" role="listbox" aria-label="Matching commands" class="command-results">
    {#each matches as action, i (action.id)}<div
        id={`command-${i}`}
        role="option"
        aria-selected={i === index}
      >
        <button
          class:active={i === index}
          tabindex="-1"
          onpointermove={() => (index = i)}
          onclick={() => run(i)}
          ><span>{action.label}</span>{#if action.hint}<kbd>{action.hint}</kbd>{/if}</button
        >
      </div>{/each}{#if !matches.length}<p class="empty-commands">No matching commands.</p>{/if}
  </div>
  <div class="command-footer">
    <span><kbd>↑</kbd><kbd>↓</kbd> navigate</span><span><kbd>↵</kbd> run command</span>
  </div>
</dialog>
