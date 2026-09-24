import { cubicOut } from 'svelte/easing';
import type { TransitionConfig } from 'svelte/transition';

const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
export const duration = (ms: number) => reduced() ? 0 : ms;

// Card positions from just before a DOM update, so a card re-created in another column can glide from its old slot.
const rects = new Map<string, DOMRect>();
// A touch drag drops a card from wherever the finger is, so its arrival starts there instead of its old slot.
const pinned = new Map<string, { rect: DOMRect; used: () => void }>();
export function pin(id: string, rect: DOMRect, used: () => void) { pinned.set(id, { rect, used }); }
export function unpin(id: string) { pinned.delete(id); }
export function capture(root: ParentNode = document) {
  rects.clear();
  for (const node of root.querySelectorAll<HTMLElement>('[data-card]')) rects.set(node.dataset.card!, node.getBoundingClientRect());
  for (const [id, { rect }] of pinned) rects.set(id, rect);
}

export function arrive(node: HTMLElement): TransitionConfig {
  const from = rects.get(node.dataset.card || '');
  const drop = pinned.get(node.dataset.card || '');
  if (drop) { pinned.delete(node.dataset.card || ''); queueMicrotask(drop.used); }
  rects.delete(node.dataset.card || '');
  if (!from) return { duration: duration(160), easing: cubicOut, css: t => `opacity: ${t}; transform: translateY(${(1 - t) * 4}px)` };
  const to = node.getBoundingClientRect();
  const ms = duration(260);
  if (!ms || (from.left === to.left && from.top === to.top)) return { duration: 0 };
  // Columns clip their scrolling content, so the card flies as a copy in a top-level layer, then the real card appears in place.
  const ghost = node.cloneNode(true) as HTMLElement;
  for (const element of [ghost, ...ghost.querySelectorAll('[id]')]) element.removeAttribute('id');
  ghost.setAttribute('aria-hidden', 'true');
  ghost.inert = true;
  Object.assign(ghost.style, { position: 'fixed', left: `${to.left}px`, top: `${to.top}px`, width: `${to.width}px`, height: `${to.height}px`, margin: '0', listStyle: 'none', zIndex: '5', pointerEvents: 'none' });
  document.body.append(ghost);
  const lift = '0 12px 32px #00000038, 0 0 0 1px var(--line-strong)';
  ghost.firstElementChild?.animate([{ boxShadow: lift }, { boxShadow: lift, offset: .7 }, {}], { duration: ms, easing: 'ease-out' });
  ghost.animate([
    { transform: `translate(${from.left - to.left}px, ${from.top - to.top}px) scale(1.02)` },
    { transform: 'none' },
  ], { duration: ms, easing: 'cubic-bezier(.33, 1, .68, 1)' }).finished.then(() => ghost.remove(), () => ghost.remove());
  return { duration: ms, css: t => `opacity: ${t < 1 ? 0 : 1}` };
}

// The detail panel slides in from the right edge; the outgoing panel is inert while it leaves.
export function panel(node: HTMLElement, _: unknown, { direction }: { direction: 'in' | 'out' | 'both' }): TransitionConfig {
  if (direction === 'out') node.inert = true;
  // Full-page on phones: slide the whole width like a pushed screen.
  const full = typeof matchMedia === 'function' && matchMedia('(max-width: 700px)').matches;
  return { duration: duration(full ? 260 : direction === 'out' ? 160 : 220), easing: cubicOut, css: (t, u) => full ? `transform: translateX(${u * 100}%)` : `transform: translateX(${u * 24}px); opacity: ${t}` };
}
