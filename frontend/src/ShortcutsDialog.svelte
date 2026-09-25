<script lang="ts">
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
  let { onclose }: { onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  onMount(() => dialog.showModal());
</script>

<dialog
  class="dlg wide help-dialog"
  bind:this={dialog}
  oncancel={(event) => {
    event.preventDefault();
    onclose();
  }}
  aria-label="Keyboard shortcuts"
>
  <header class="dlg-head">
    <h2>Keyboard shortcuts</h2>
    <button class="icon-button" aria-label="Close shortcuts" onclick={onclose}
      ><Icon name="close" size={16} /></button
    >
  </header>
  {#each [{ title: 'Move around', keys: [['↑ ↓ / j k', 'Previous / next ticket'], ['← → / h l', 'Previous / next column'], ['Home / gg · End / G', 'First · last loaded ticket'], ['1–7', 'Jump to a column'], ['Enter', 'Open ticket'], ['[ ] / j k', 'Previous / next in details'], ['F6', 'Switch board / details or search']] }, { title: 'Work with tickets', keys: [['C', 'Create in current column'], ['Delete', 'Delete ticket (with confirmation)'], ['E / I · D', 'Edit title · description'], ['A · M', 'Assignees · assign / unassign me'], ['S · T · Y', 'Status · tags · type'], ['Alt ↑ ↓ / Shift K J', 'Reorder ticket'], ['Alt ← → / Shift H L', 'Move to adjacent status'], ['Ctrl / ⌘ Enter', 'Save now / create and edit'], ['Ctrl / ⌘ Shift Enter', 'Save and close details']] }, { title: 'Find and control', keys: [['/', 'Search loaded tickets'], ['Enter / ↓ in search', 'Focus first result'], ['Ctrl / ⌘ K', 'Commands'], ['↑ ↓ / Ctrl J K', 'Navigate a command menu'], ['R', 'Refresh and apply order'], ['Escape', 'Leave field, close or cancel'], ['?', 'This guide']] }] as group}<h3
    >
      {group.title}
    </h3>
    <dl>
      {#each group.keys as [keys, action]}<div>
          <dt>{action}</dt>
          <dd><kbd>{keys}</kbd></dd>
        </div>{/each}
    </dl>{/each}
  <footer class="dlg-foot">
    <span class="note">Letter keys pause while typing · edits save automatically</span>
  </footer>
</dialog>
