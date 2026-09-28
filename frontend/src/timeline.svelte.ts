import { api, APIError, message } from './api';
import type { Activity, ActivityPage } from './api';

export type PendingComment = {
  client_id: string;
  body: string;
  created_at: string;
  sending: boolean;
  error: string;
};
type Draft = { text: string; pending: PendingComment[] };
const compare = (a: Activity, b: Activity) => (BigInt(a.id) < BigInt(b.id) ? -1 : 1);

// History belongs to the open ticket; drafts and uncertain sends survive ticket
// navigation and reloads. Only an explicit send/retry can post a message.
export class TimelineState {
  id = $state('');
  events = $state.raw<Activity[]>([]);
  drafts = $state<Record<string, Draft>>({});
  draft = $derived(this.drafts[this.id]);
  loaded = $state(false);
  loading = $state(false);
  loadingOlder = $state(false);
  error = $state('');
  before = $state('');
  private cursor = '0';
  private request?: AbortController;
  private olderRequest?: AbortController;

  constructor(
    readonly userId: string,
    private onmissing: (id: string) => void,
  ) {}

  private key(id: string) {
    return `tiki.comments.${this.userId}.${id}`;
  }

  private remember(id: string) {
    try {
      const draft = this.drafts[id];
      if (draft.text || draft.pending.length)
        sessionStorage.setItem(this.key(id), JSON.stringify(draft));
      else sessionStorage.removeItem(this.key(id));
    } catch {
      // Drafts remain in memory when browser storage is unavailable or full.
    }
  }

  setText(text: string) {
    this.draft.text = text;
    this.remember(this.id);
  }

  stop() {
    this.request?.abort();
    this.olderRequest?.abort();
    this.loading = this.loadingOlder = false;
  }

  open(id: string) {
    if (id !== this.id) {
      this.stop();
      this.id = id;
      this.events = [];
      this.loaded = false;
      this.before = this.error = '';
      this.cursor = '0';
      if (id && !this.drafts[id]) {
        let draft: Draft = { text: '', pending: [] };
        try {
          const saved = JSON.parse(sessionStorage.getItem(this.key(id)) || 'null');
          if (typeof saved?.text === 'string' && Array.isArray(saved.pending)) {
            draft = {
              text: saved.text,
              pending: saved.pending
                .filter(
                  (p: PendingComment) =>
                    typeof p.client_id === 'string' &&
                    typeof p.body === 'string' &&
                    typeof p.created_at === 'string',
                )
                .map((p: PendingComment) => ({
                  ...p,
                  sending: false,
                  error: 'Delivery not confirmed. Retry safely.',
                })),
            };
          }
        } catch {
          /* Start with an empty draft if storage is unavailable. */
        }
        this.drafts[id] = draft;
      }
    }
    if (id) void this.sync();
  }

  accept(events: Activity[]) {
    for (const event of events) {
      const draft = this.drafts[event.item_id || ''];
      if (draft && event.kind === 'comment.created' && event.actor_id === this.userId) {
        draft.pending = draft.pending.filter((p) => p.client_id !== event.client_id);
        this.remember(event.item_id!);
      }
    }
    const incoming = events.filter((event) => event.item_id === this.id);
    if (!incoming.length) return;
    const merged = new Map(this.events.map((event) => [event.id, event]));
    for (const event of incoming) merged.set(event.id, event);
    if (merged.size === this.events.length) return;
    this.events = [...merged.values()].sort(compare);
  }

  async sync() {
    this.request?.abort();
    if (!this.id) return;
    const id = this.id;
    const request = (this.request = new AbortController());
    this.loading = true;
    this.error = '';
    try {
      // Streamed arrivals never advance this cursor: doing so could skip a
      // event missed before reconnecting, or between paginated history reads.
      let after = this.loaded ? this.cursor : '';
      do {
        const page = await api<ActivityPage>(
          `/items/${id}/activity?limit=50${after ? `&after=${after}` : ''}`,
          'GET',
          undefined,
          request.signal,
        );
        if (request.signal.aborted) return;
        this.accept(page.activity);
        if (!this.loaded) this.before = page.next_before || '';
        this.loaded = true;
        this.cursor = page.activity.at(-1)?.id || this.cursor;
        after = page.next_after || '';
      } while (after);
    } catch (error) {
      if (!request.signal.aborted) {
        if (error instanceof APIError && error.status === 404) this.onmissing(id);
        else this.error = message(error);
      }
    } finally {
      if (this.request === request) this.loading = false;
    }
  }

  async more() {
    if (!this.before || this.loadingOlder) return;
    const id = this.id;
    const request = (this.olderRequest = new AbortController());
    this.loadingOlder = true;
    this.error = '';
    try {
      const page = await api<ActivityPage>(
        `/items/${id}/activity?limit=50&before=${this.before}`,
        'GET',
        undefined,
        request.signal,
      );
      if (request.signal.aborted) return;
      this.accept(page.activity);
      this.before = page.next_before || '';
    } catch (error) {
      if (!request.signal.aborted) {
        if (error instanceof APIError && error.status === 404) this.onmissing(id);
        else this.error = message(error);
      }
    } finally {
      if (this.olderRequest === request) this.loadingOlder = false;
    }
  }

  send() {
    const body = this.draft?.text.trim();
    if (!body || new TextEncoder().encode(body).length > 16 * 1024) return;
    this.draft.pending.push({
      client_id: crypto.randomUUID(),
      body,
      created_at: new Date().toISOString(),
      sending: false,
      error: '',
    });
    const pending = this.draft.pending.at(-1)!;
    this.setText('');
    void this.retry(pending);
  }

  async retry(pending: PendingComment) {
    if (pending.sending) return;
    const id = this.id;
    pending.sending = true;
    pending.error = '';
    this.remember(id);
    try {
      const comment = await api<Activity>(`/items/${id}/comments`, 'POST', {
        body: pending.body,
        client_id: pending.client_id,
      });
      this.accept([comment]);
    } catch (error) {
      pending.error = message(error);
      if (error instanceof APIError && error.status === 404) this.onmissing(id);
    } finally {
      pending.sending = false;
      this.remember(id);
    }
  }
}
