<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  let { actions, onclose }: { actions: { id: string; label: string; hint?: string; run: () => void }[]; onclose: () => void } = $props();
  let query = $state('');
  let index = $state(0);
  let dialog: HTMLDialogElement;
  let input: HTMLInputElement;
  let matches = $derived(actions.filter(a => a.label.toLowerCase().includes(query.toLowerCase())));
  onMount(() => { dialog.showModal(); input.focus(); });
  function run(i: number) { const action = matches[i]; if (action) { onclose(); action.run(); } }
  function keydown(event: KeyboardEvent) {
    if (event.isComposing) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault(); index = (index + (event.key === 'ArrowDown' ? 1 : -1) + matches.length) % (matches.length || 1);
      document.getElementById(`command-${index}`)?.scrollIntoView({ block: 'nearest' });
    } else if (event.key === 'Enter') { event.preventDefault(); run(index); }
  }
</script>

<!-- The dialog owns its keyboard scope and native focus trap. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog class="command-dialog" bind:this={dialog} oncancel={onclose} onkeydown={keydown} aria-label="Commands">
  <div class="command-search"><Icon name="search" /><input bind:this={input} bind:value={query} oninput={() => index = 0} placeholder="What would you like to do?" aria-label="Find a command" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls="command-results" aria-activedescendant={matches.length ? `command-${index}` : undefined} /><button class="key-button" onclick={onclose}>esc</button></div>
  <div class="command-caption">ACTIONS <span>{matches.length} available</span></div>
  <div id="command-results" role="listbox" aria-label="Matching commands" class="command-results">{#each matches as action, i (action.id)}<div id={`command-${i}`} role="option" aria-selected={i === index}><button class:active={i === index} tabindex="-1" onpointermove={() => index = i} onclick={() => run(i)}><span>{action.label}</span>{#if action.hint}<kbd>{action.hint}</kbd>{/if}</button></div>{/each}{#if !matches.length}<p class="empty-commands">No matching commands.</p>{/if}</div>
  <div class="command-footer"><span><kbd>↑</kbd><kbd>↓</kbd> navigate</span><span><kbd>↵</kbd> run command</span></div>
</dialog>
