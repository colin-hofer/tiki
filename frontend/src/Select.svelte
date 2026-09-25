<script lang="ts" module>
  export interface Option {
    value: string;
    label: string;
    icon?: string;
    iconClass?: string;
    avatar?: string;
    avatarHue?: number;
    hint?: string;
    description?: string;
    action?: () => void;
  }
  let sequence = 0;
</script>

<script lang="ts">
  import { tick } from 'svelte';
  import Icon from './Icon.svelte';

  // Select-only combobox: focus stays on the trigger and the list is referenced through aria-activedescendant.
  let {
    value = $bindable(''),
    options,
    label,
    placeholder = '',
    placeholderIcon = '',
    disabled = false,
    variant = 'plain',
    title = '',
    onchange,
  }: {
    value?: string;
    options: Option[];
    label: string;
    placeholder?: string;
    placeholderIcon?: string;
    disabled?: boolean;
    variant?: 'plain' | 'filter' | 'property' | 'add' | 'field';
    title?: string;
    onchange?: (value: string) => void;
  } = $props();

  const id = `select-${++sequence}`;
  let open = $state(false);
  let active = $state(0);
  let trigger: HTMLButtonElement;
  let popup = $state<HTMLDivElement>(null!);
  let typed = '';
  let typedAt = 0;

  let current = $derived(options.find((o) => o.value === value));
  let showPlaceholder = $derived(!current || (current.value === '' && Boolean(placeholder)));

  async function show() {
    if (disabled || open || !options.length) return;
    active = Math.max(
      0,
      options.findIndex((o) => o.value === value),
    );
    open = true;
    await tick();
    popup.showPopover?.();
    place();
    popup.querySelector(`#${id}-${active}`)?.scrollIntoView({ block: 'nearest' });
  }
  function hide(focus = true) {
    if (!open) return;
    open = false;
    if (popup?.matches(':popover-open')) popup.hidePopover();
    if (focus) trigger.focus();
  }
  function choose(index: number) {
    const option = options[index];
    hide();
    if (option?.action) {
      option.action();
      return;
    }
    if (!option || (option.value === value && variant !== 'add')) return;
    if (variant !== 'add') value = option.value;
    onchange?.(option.value);
  }
  function place() {
    if (!open || !popup) return;
    const rect = trigger.getBoundingClientRect();
    const width = Math.max(rect.width, 180);
    const height = popup.offsetHeight;
    const below = window.innerHeight - rect.bottom - 8;
    const top = below >= height || below >= rect.top ? rect.bottom + 4 : rect.top - height - 4;
    popup.style.minWidth = `${width}px`;
    popup.style.left = `${Math.min(Math.max(8, rect.left), window.innerWidth - popup.offsetWidth - 8)}px`;
    popup.style.top = `${Math.max(8, top)}px`;
    popup.dataset.side = top > rect.top ? 'bottom' : 'top';
  }
  function scrollTo(index: number) {
    active = index;
    popup?.querySelector(`#${id}-${index}`)?.scrollIntoView({ block: 'nearest' });
  }
  function typeahead(key: string) {
    const now = Date.now();
    typed = now - typedAt > 700 ? key : typed + key;
    typedAt = now;
    const start = typed.length === 1 ? active + 1 : active;
    for (let i = 0; i < options.length; i++) {
      const index = (start + i) % options.length;
      if (options[index].label.toLowerCase().startsWith(typed.toLowerCase())) return index;
    }
    return -1;
  }

  function keydown(event: KeyboardEvent) {
    if (event.isComposing || event.ctrlKey || event.metaKey) return;
    const key = event.key;
    const consume = () => {
      event.preventDefault();
      event.stopPropagation();
    };
    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(key)) {
        consume();
        void show();
      } else if (key.length === 1 && !event.altKey && /\S/.test(key) && options.length) {
        consume();
        void show().then(() => {
          const index = typeahead(key);
          if (index >= 0) scrollTo(index);
        });
      }
      return;
    }
    if (key === 'Tab') {
      hide(false);
      return;
    }
    consume();
    if (key === 'Escape') hide();
    else if (key === 'Enter' || key === ' ') choose(active);
    else if (key === 'ArrowDown') scrollTo(Math.min(active + 1, options.length - 1));
    else if (key === 'ArrowUp') scrollTo(Math.max(active - 1, 0));
    else if (key === 'Home' || key === 'PageUp') scrollTo(0);
    else if (key === 'End' || key === 'PageDown') scrollTo(options.length - 1);
    else if (key.length === 1) {
      const index = typeahead(key);
      if (index >= 0) scrollTo(index);
    }
  }

  $effect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (!popup.contains(event.target as Node) && !trigger.contains(event.target as Node))
        hide(false);
    };
    const scroll = (event: Event) => {
      if (!popup.contains(event.target as Node)) hide(false);
    };
    document.addEventListener('pointerdown', outside, true);
    window.addEventListener('scroll', scroll, true);
    window.addEventListener('resize', place);
    return () => {
      document.removeEventListener('pointerdown', outside, true);
      window.removeEventListener('scroll', scroll, true);
      window.removeEventListener('resize', place);
    };
  });
  $effect(() => {
    if (disabled) hide(false);
  });
</script>

<button
  bind:this={trigger}
  type="button"
  class={`select-trigger ${variant}`}
  class:open
  class:has-value={!showPlaceholder}
  role="combobox"
  aria-label={label}
  aria-haspopup="listbox"
  aria-expanded={open}
  aria-controls={`${id}-list`}
  aria-activedescendant={open ? `${id}-${active}` : undefined}
  title={title || undefined}
  {disabled}
  onclick={() => (open ? hide() : void show())}
  onkeydown={keydown}
>
  {#if showPlaceholder}
    {#if placeholderIcon}<Icon name={placeholderIcon} size={13} />{/if}<span class="select-value"
      >{placeholder || current?.label || ''}</span
    >
  {:else if current}
    {#if current.avatar}<span class="mini-avatar" style:--hue={current.avatarHue}
        >{current.avatar}</span
      >{:else if current.icon}<span class={current.iconClass || ''}
        ><Icon name={current.icon} size={14} /></span
      >{/if}<span class="select-value">{current.label}</span>
  {/if}
  {#if variant !== 'add'}<span class="select-chevron"><Icon name="down" size={13} /></span>{/if}
</button>
{#if open}
  <!-- Pointer-only interactions: keyboard control lives on the combobox trigger. -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    bind:this={popup}
    id={`${id}-list`}
    class="select-popup"
    popover="manual"
    role="listbox"
    aria-label={`${label} options`}
    tabindex="-1"
  >
    {#each options as option, i (option.value)}
      <div
        id={`${id}-${i}`}
        class="select-option"
        class:active={i === active}
        class:select-action={Boolean(option.action)}
        role="option"
        tabindex="-1"
        aria-selected={option.value === value}
        onpointermove={() => (active = i)}
        onpointerdown={(event) => event.preventDefault()}
        onclick={() => choose(i)}
      >
        {#if option.avatar}<span class="mini-avatar" style:--hue={option.avatarHue}
            >{option.avatar}</span
          >{:else if option.icon}<span class={option.iconClass || ''}
            ><Icon name={option.icon} size={14} /></span
          >{/if}
        {#if option.description}<span class="select-label select-stack"
            ><span>{option.label}</span><small>{option.description}</small></span
          >{:else}<span class="select-label">{option.label}</span>{/if}
        {#if option.hint}<span class="select-hint">{option.hint}</span>{/if}
        <span class="select-check"
          >{#if option.value === value && !(option.value === '' && variant === 'add')}<Icon
              name="check"
              size={13}
            />{/if}</span
        >
      </div>
    {/each}
  </div>
{/if}

<style>
  .select-action {
    border-top: 1px solid var(--line-strong);
    margin-top: 4px;
  }
</style>
