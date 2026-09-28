<script lang="ts">
  import Icon from './Icon.svelte';

  // Editable combobox: focus stays in the input, suggestions share the Select popup styling.
  // Enter always commits what was typed (or the highlighted suggestion); commas add several tags.
  let {
    value = $bindable(''),
    tags,
    exclude,
    label,
    placeholder = 'Add tag',
    onadd,
    onblur,
  }: {
    value?: string;
    tags: string[];
    exclude: string[];
    label: string;
    placeholder?: string;
    onadd: () => void;
    onblur?: () => void;
  } = $props();

  const id = $props.id();
  let input: HTMLInputElement;
  let field: HTMLDivElement;
  let popup = $state<HTMLDivElement>(null!);
  let focused = $state(false);
  let dismissed = $state(false);
  let active = $state(-1);

  // Only the text after the last comma is being completed.
  const query = $derived(value.split(',').at(-1)!.trim().toLowerCase());
  const options = $derived.by(() => {
    const taken = new Set(exclude);
    const rank = (tag: string) => (tag === query ? 0 : tag.startsWith(query) ? 1 : 2);
    const matches = tags
      .filter((tag) => !taken.has(tag) && tag.includes(query))
      .sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
      .slice(0, 50)
      .map((tag) => ({ value: tag, create: false }));
    if (query && !taken.has(query) && matches[0]?.value !== query)
      matches.unshift({ value: query, create: true });
    return matches;
  });
  const open = $derived(focused && !dismissed && options.length > 0);

  $effect(() => {
    // Typing highlights the first row, so Enter commits the typed tag; an empty field highlights nothing.
    void query;
    active = query ? 0 : -1;
  });
  $effect(() => {
    void options.length;
    if (!popup) return;
    if (open && !popup.matches(':popover-open')) popup.showPopover();
    else if (!open && popup.matches(':popover-open')) popup.hidePopover();
    if (open) place();
  });
  $effect(() => {
    if (!open) return;
    const scroll = (event: Event) => {
      if (!popup.contains(event.target as Node)) dismissed = true;
    };
    window.addEventListener('scroll', scroll, true);
    window.addEventListener('resize', place);
    return () => {
      window.removeEventListener('scroll', scroll, true);
      window.removeEventListener('resize', place);
    };
  });

  function place() {
    const rect = field.getBoundingClientRect();
    popup.style.minWidth = `${Math.max(rect.width, 200)}px`;
    const height = popup.offsetHeight;
    const below = window.innerHeight - rect.bottom - 8;
    const top = below >= height || below >= rect.top ? rect.bottom + 4 : rect.top - height - 4;
    popup.style.left = `${Math.min(Math.max(8, rect.left), window.innerWidth - popup.offsetWidth - 8)}px`;
    popup.style.top = `${Math.max(8, top)}px`;
    popup.dataset.side = top > rect.top ? 'bottom' : 'top';
  }
  function scrollTo(index: number) {
    active = index;
    popup?.querySelector(`#${id}-${index}`)?.scrollIntoView({ block: 'nearest' });
  }
  function choose(index: number) {
    const option = options[index];
    if (!option) return;
    const parts = value.split(',');
    parts[parts.length - 1] = option.value;
    value = parts.join(',');
    onadd();
    dismissed = false;
    input.focus();
  }

  function keydown(event: KeyboardEvent) {
    if (event.isComposing || event.ctrlKey || event.metaKey || event.altKey) return;
    const consume = () => {
      event.preventDefault();
      event.stopPropagation();
    };
    const key = event.key;
    if (key === 'Enter') {
      consume();
      if (open && active >= 0) choose(active);
      else if (value.trim()) onadd();
    } else if (key === 'Escape' && open) {
      consume();
      dismissed = true;
    } else if (key === 'ArrowDown' || key === 'ArrowUp') {
      consume();
      if (!open) {
        dismissed = false;
        if (key === 'ArrowDown') active = 0;
        return;
      }
      const last = options.length - 1;
      scrollTo(key === 'ArrowDown' ? Math.min(active + 1, last) : Math.max(active - 1, 0));
    } else if (key === 'Tab') dismissed = true;
  }
</script>

<div class="tag-entry" bind:this={field}>
  <Icon name="tag" size={13} />
  <input
    bind:this={input}
    bind:value
    role="combobox"
    aria-label={label}
    aria-autocomplete="list"
    aria-expanded={open}
    aria-controls={`${id}-list`}
    aria-activedescendant={open && active >= 0 ? `${id}-${active}` : undefined}
    {placeholder}
    maxlength="64"
    autocomplete="off"
    autocapitalize="none"
    spellcheck="false"
    enterkeyhint="done"
    onfocus={() => {
      focused = true;
      dismissed = false;
    }}
    onblur={() => {
      focused = false;
      onblur?.();
    }}
    oninput={() => (dismissed = false)}
    onpointerdown={() => (dismissed = false)}
    onkeydown={keydown}
  />
</div>
<!-- Pointer-only interactions: keyboard control lives on the combobox input. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  bind:this={popup}
  id={`${id}-list`}
  class="select-popup"
  popover="manual"
  role="listbox"
  aria-label="Tag suggestions"
  tabindex="-1"
>
  {#each options as option, i (option.create ? `+${option.value}` : option.value)}
    <div
      id={`${id}-${i}`}
      class="select-option"
      class:active={i === active}
      role="option"
      tabindex="-1"
      aria-selected={i === active}
      onpointermove={() => (active = i)}
      onpointerdown={(event) => event.preventDefault()}
      onclick={() => choose(i)}
    >
      <span><Icon name={option.create ? 'plus' : 'tag'} size={13} /></span>
      <span class="select-label"
        >{#if option.create}Create <strong>{option.value}</strong>{:else}{option.value}{/if}</span
      >
    </div>
  {/each}
</div>
