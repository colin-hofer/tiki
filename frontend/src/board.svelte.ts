import { api, APIError, message, statuses, watchChanges } from './api';
import type { Item, ItemPatch, NewItem, Status, Updates, User } from './api';
import { readBoard, readColumns, readDirectory } from './board-api';
import type { Columns, Pages } from './board-api';
import { applyItemChanges, compareItems, reconcileItems } from './items';
import type { Filters } from './items';
import { TicketState } from './ticket.svelte';

function emptyBatch() {
  return {
    items: new Map<string, Item>(),
    columns: new Set<Status>(),
    snapshot: false,
    directory: false,
    order: false,
  };
}
type Batch = ReturnType<typeof emptyBatch>;
type Session = { stop: () => void; read?: AbortController; running?: Promise<void> };

// One queue owns board synchronization. Writes interrupt reads; their completion resumes the queue.
export class BoardState {
  items = $state.raw<Item[]>([]);
  users = $state.raw<User[]>([]);
  tags = $state.raw<string[]>([]);
  cursors = $state.raw<Partial<Record<Status, string>>>({});
  filters = $state<Filters>({ status: '', tag: '', assignee: '' });
  columns = $derived(
    statuses.filter((status) => !this.filters.status || status === this.filters.status),
  );
  itemsById = $derived(new Map(this.items.map((item) => [item.id, item])));
  usersById = $derived(new Map(this.users.map((user) => [user.id, user])));
  busy = $state(false);
  writing = $state(0);
  hasLoaded = $state(false);
  connection = $state<'connecting' | 'live' | 'offline' | 'paused'>('connecting');
  lastSync = $state('');
  error = $state('');
  notice = $state('');
  ticket = new TicketState();
  orderChanged = $derived.by(() => {
    const last = new Map<Status, Item>();
    for (const item of this.items) {
      if (!this.columns.includes(item.status)) continue;
      const previous = last.get(item.status);
      if (previous && compareItems(previous, item) > 0) return true;
      last.set(item.status, item);
    }
    return false;
  });

  private pages: Pages = {};
  private pending = emptyBatch();
  private session?: Session;
  private timer?: ReturnType<typeof setTimeout>;
  private failures = 0;
  private streamState: 'connecting' | 'live' | 'offline' = 'connecting';

  constructor(
    private userId: string,
    private onuser: (user: User) => void,
  ) {}

  start() {
    this.stop();
    if (document.hidden) {
      this.connection = 'paused';
      return;
    }
    const session: Session = { stop: () => {} };
    this.session = session;
    session.stop = watchChanges(
      (updates) => this.receive(updates),
      (state) => {
        this.connection = this.streamState = state;
        // Still load the board when the streaming endpoint is unavailable.
        if (state === 'offline' && !this.hasLoaded) this.receive({ reset: true });
      },
    );
  }

  stop() {
    this.session?.stop();
    this.session?.read?.abort();
    this.session = undefined;
    this.ticket.stop();
    clearTimeout(this.timer);
    this.pending = emptyBatch();
    this.busy = false;
    this.failures = 0;
  }

  pause() {
    this.stop();
    this.connection = 'paused';
  }

  refresh() {
    this.pending.snapshot = this.pending.order = true;
    this.session?.read?.abort();
    return this.sync();
  }

  reset() {
    this.pages = {};
    return this.refresh();
  }

  more(status: Status) {
    this.pages[status] = (this.pages[status] || 1) + 1;
    this.pending.columns.add(status);
    this.pending.order = true;
    return this.sync();
  }

  applyOrder() {
    this.items = reconcileItems(this.items, [], this.filters, [], true);
  }

  private receive(updates: Updates) {
    this.pending.snapshot ||= Boolean(updates.reset);
    this.pending.directory ||= Boolean(updates.users);
    if (!this.pending.snapshot) {
      for (const item of updates.items || []) {
        const previous = this.pending.items.get(item.id);
        if (!previous || item.version > previous.version) this.pending.items.set(item.id, item);
      }
      // A burst collapses to one snapshot instead of growing an unbounded event backlog.
      const items = [...this.pending.items.values()];
      if (
        items.length > 64 ||
        items.reduce((size, item) => size + (item.description?.length || 0), 0) > 2 << 20
      )
        this.pending.snapshot = true;
    }
    if (this.pending.snapshot) this.pending.items.clear();
    this.schedule();
  }

  private schedule(delay = 100) {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.sync(), delay);
  }

  private sync(): Promise<void> {
    clearTimeout(this.timer);
    const session = this.session;
    if (!session || this.writing) return Promise.resolve();
    if (session.running) return session.running;
    session.running = this.drain(session).finally(() => {
      session.running = undefined;
    });
    return session.running;
  }

  private async drain(session: Session) {
    while (this.session === session && !this.writing) {
      const batch = this.pending;
      if (!batch.snapshot && !batch.directory && !batch.items.size && !batch.columns.size) return;
      this.pending = emptyBatch();
      const read = (session.read = new AbortController());
      this.busy = batch.order || !this.hasLoaded;
      try {
        if (batch.items.size) this.accept([...batch.items.values()], batch.order, batch.columns);
        await this.reload(batch, read.signal);
        if (read.signal.aborted) continue;
        this.failures = 0;
        this.error = '';
        this.connection = this.streamState;
        this.lastSync = new Date().toLocaleTimeString([], {
          hour: '2-digit',
          minute: '2-digit',
          second: '2-digit',
        });
      } catch (error) {
        if (this.session !== session) return;
        // Interrupted reads retry after the write or newer filter change. Queued events stay intact.
        this.pending.snapshot = true;
        this.pending.order ||= batch.order;
        if (read.signal.aborted) continue;
        if (error instanceof APIError && error.code === 'cursor_expired') {
          if (Object.values(this.pages).some((count) => count > 1)) {
            this.pages = {};
            this.pending.order = true;
            this.notice = 'Order changed; loaded pages reset.';
            continue;
          }
        }
        this.error = message(error);
        this.connection = 'offline';
        this.schedule(Math.min(30000, 1000 * 2 ** Math.min(this.failures++, 5)));
        return;
      } finally {
        if (this.session === session) {
          session.read = undefined;
          this.busy = false;
        }
      }
    }
  }

  private async reload(batch: Batch, signal: AbortSignal) {
    const filters = { ...this.filters };
    if (batch.snapshot || !this.hasLoaded) {
      const board = await readBoard(filters, this.pages, signal);
      signal.throwIfAborted();
      this.setDirectory(board);
      if (filters.tag && !board.tags.includes(filters.tag)) {
        this.filters.tag = '';
        this.pages = {};
        this.pending.snapshot = true;
        this.pending.order ||= batch.order;
        return;
      }
      this.replaceColumns(board.columns, batch.order || !this.hasLoaded);
      this.hasLoaded = true;
      await this.ticket.reload(signal);
      return;
    }
    if (batch.directory) {
      const directory = await readDirectory(signal);
      signal.throwIfAborted();
      this.setDirectory(directory);
    }
    if (batch.columns.size) {
      const columns = await readColumns(
        [...batch.columns].filter((status) => this.columns.includes(status)),
        filters,
        this.pages,
        signal,
      );
      signal.throwIfAborted();
      this.replaceColumns(columns, batch.order);
    }
  }

  private replaceColumns(columns: Columns, order: boolean) {
    const replaced = Object.keys(columns) as Status[];
    this.items = reconcileItems(
      this.items,
      Object.values(columns).flatMap((page) => page.items),
      this.filters,
      replaced,
      order,
    );
    this.cursors = {
      ...this.cursors,
      ...Object.fromEntries(replaced.map((status) => [status, columns[status]!.next_cursor || ''])),
    };
  }

  private accept(items: Item[], order: boolean, columns = this.pending.columns) {
    const tags = new Set(this.tags);
    for (const item of items) {
      this.ticket.accept(item);
      if (item.version > (this.itemsById.get(item.id)?.version || 0))
        for (const tag of item.tags) tags.add(tag);
    }
    const changes = applyItemChanges(this.items, items, this.cursors, this.filters, order);
    this.items = changes.items;
    for (const status of changes.columns) columns.add(status);
    if (tags.size !== this.tags.length) this.tags = [...tags].sort();
  }

  private setDirectory({ users, tags }: { users: User[]; tags: string[] }) {
    this.users = users;
    this.tags = tags;
    const current = users.find((user) => user.id === this.userId);
    if (current) this.onuser(current);
  }

  userChanged(user: User) {
    this.users = this.users.map((current) => (current.id === user.id ? user : current));
    if (user.id === this.userId) this.onuser(user);
  }

  private async write<T>(request: () => Promise<T>, accept: (result: T) => void): Promise<T> {
    const session = this.session;
    session?.read?.abort();
    this.writing++;
    try {
      const result = await request();
      if (session && this.session === session) accept(result);
      return result;
    } finally {
      this.writing--;
      // The acknowledgement completes the save. Background reloads must not hold the editor open.
      if (this.session) this.schedule(0);
    }
  }

  create(item: NewItem) {
    return this.write(
      () => api<Item>('/items', 'POST', item),
      (created) => this.accept([created], true),
    );
  }

  update(id: string, version: number, patch: ItemPatch) {
    return this.write(
      () => api<Item>(`/items/${id}`, 'PATCH', { version, ...patch }),
      (item) => this.accept([item], true),
    );
  }

  move(item: Item, anchor: Item, before: boolean) {
    return this.write(
      () =>
        api<Item>(`/items/${item.id}/move`, 'POST', {
          version: item.version,
          status: anchor.status,
          [before ? 'before' : 'after']: anchor.id,
        }),
      (moved) => this.accept([moved], true),
    );
  }

  remove(item: Item) {
    return this.write(
      async () => {
        try {
          await api(`/items/${item.id}`, 'DELETE', { version: item.version });
        } catch (error) {
          if (!(error instanceof APIError && error.status === 404)) throw error;
        }
      },
      () => {
        this.items = this.items.filter((current) => current.id !== item.id);
        this.pending.items.delete(item.id);
        this.pending.columns.add(item.status);
        if (this.ticket.id === item.id) void this.ticket.open('');
      },
    );
  }

  async tagDeleted(name: string) {
    this.tags = this.tags.filter((tag) => tag !== name);
    if (this.filters.tag === name) {
      this.filters.tag = '';
      await this.reset();
    } else await this.refresh();
  }
}
