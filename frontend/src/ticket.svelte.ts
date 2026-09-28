import { api, APIError, message } from './api';
import type { Item } from './api';

// The open ticket has its own request lifetime. Board reloads never replace its draft.
export class TicketState {
  id = $state('');
  item = $state.raw<Item | null>(null);
  deleted = $state(false);
  error = $state('');
  private request = $state.raw<AbortController | null>(null);
  loading = $derived(this.request !== null);

  accept(item: Item) {
    if (item.id === this.id && (!this.item || item.version > this.item.version)) this.item = item;
  }

  stop() {
    this.request?.abort();
    this.request = null;
  }

  open(id: string) {
    this.stop();
    this.id = id;
    this.item = null;
    this.deleted = false;
    this.error = '';
    return this.reload();
  }

  async reload(parent?: AbortSignal) {
    this.stop();
    if (!this.id) return;
    const request = (this.request = new AbortController());
    const signal = parent ? AbortSignal.any([request.signal, parent]) : request.signal;
    this.error = '';
    try {
      const item = await api<Item>(`/items/${this.id}`, 'GET', undefined, signal);
      if (!signal.aborted) this.accept(item);
    } catch (error) {
      if (signal.aborted) return;
      if (error instanceof APIError && error.status === 404) this.deleted = true;
      else this.error = message(error);
    } finally {
      if (this.request === request) this.request = null;
    }
  }
}
