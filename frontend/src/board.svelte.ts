import { api, APIError, directory, message, statuses, watchChanges } from './api';
import type { Board, Item, ItemPatch, NewItem, Page, Status, User } from './api';

export interface Filters {
  status: Status | '';
  tag: string;
  assignee: string;
}
const compare = (a: Item, b: Item) =>
  a.priority - b.priority ||
  (BigInt(a.id) < BigInt(b.id) ? -1 : BigInt(a.id) > BigInt(b.id) ? 1 : 0);
const summary = ({ description, ...item }: Item): Item => item;

// One owner for server state. Components own navigation, focus, and unsaved drafts.
export class BoardState {
  items = $state<Item[]>([]);
  users = $state<User[]>([]);
  tags = $state<string[]>([]);
  filters = $state<Filters>({ status: '', tag: '', assignee: '' });
  columns = $derived(
    statuses.filter((status) => !this.filters.status || status === this.filters.status),
  );
  cursors = $state<Partial<Record<Status, string>>>({});
  busy = $state(false);
  writing = $state(0);
  hasLoaded = $state(false);
  connection = $state<'connecting' | 'live' | 'offline' | 'paused'>('connecting');
  lastSync = $state('');
  error = $state('');
  notice = $state('');
  orderChanged = $state(false);
  openId = $state('');
  detail = $state<Item | null>(null);
  detailLoading = $state(false);
  detailDeleted = $state(false);

  private pages: Partial<Record<Status, number>> = {};
  private generation = 0;
  private detailGeneration = 0;
  private controller?: AbortController;
  private timer?: ReturnType<typeof setTimeout>;
  private stopEvents?: () => void;
  private active = false;
  private syncing = false;
  private flushing = false;
  private failures = 0;
  private pendingReset = false;
  private pendingOrder = false;
  private pendingUsers = false;
  private pendingItems = new Map<string, Item>();
  private streamState: 'connecting' | 'live' | 'offline' | 'paused' = 'connecting';

  constructor(
    private userId: string,
    private onuser: (user: User) => void,
  ) {}

  start() {
    this.stop();
    this.active = true;
    if (document.hidden) {
      this.connection = this.streamState = 'paused';
      return;
    }
    this.stopEvents = watchChanges(
      (updates) => {
        this.pendingReset ||= Boolean(updates.reset);
        this.pendingUsers ||= Boolean(updates.users);
        if (!this.pendingReset)
          for (const item of updates.items || []) {
            if (item.version > (this.pendingItems.get(item.id)?.version || 0))
              this.pendingItems.set(item.id, item);
          }
        if (
          this.pendingItems.size > 64 ||
          [...this.pendingItems.values()].reduce(
            (n, item) => n + (item.description?.length || 0),
            0,
          ) >
            2 << 20
        )
          this.pendingReset = true;
        if (this.pendingReset) this.pendingItems.clear();
        this.schedule();
      },
      (state) => {
        this.connection = this.streamState = state;
        // The board remains usable if a proxy or server cannot open the stream.
        // A later ready event always requests a fresh snapshot to close the gap.
        if (state === 'offline' && !this.hasLoaded) {
          this.pendingReset = true;
          this.schedule();
        }
      },
    );
  }

  stop() {
    this.active = false;
    this.stopEvents?.();
    this.stopEvents = undefined;
    clearTimeout(this.timer);
    this.pendingReset = this.pendingUsers = this.pendingOrder = false;
    this.pendingItems.clear();
    this.cancelRead();
    this.detailGeneration++;
  }

  pause() {
    this.stop();
    this.connection = this.streamState = 'paused';
  }

  private cancelRead() {
    this.controller?.abort();
    this.generation++;
    this.syncing = this.busy = false;
  }

  private schedule(delay = 100) {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.flush(), delay);
  }

  private async flush() {
    if (
      !this.active ||
      document.hidden ||
      (!this.pendingReset && !this.pendingUsers && !this.pendingItems.size)
    )
      return;
    if (this.flushing || this.syncing || this.writing) {
      this.schedule();
      return;
    }
    const stream = this.stopEvents;
    const reset = this.pendingReset || !this.hasLoaded;
    const order = this.pendingOrder;
    const users = this.pendingUsers;
    const incoming = [...this.pendingItems.values()];
    this.pendingReset = this.pendingUsers = this.pendingOrder = false;
    this.pendingItems.clear();
    this.flushing = true;
    let loaded = false;
    try {
      if (!reset && users) await this.loadDirectory();
      if (stream !== this.stopEvents) return;
      if (reset) loaded = await this.refresh({ order });
      else {
        const affected = this.merge(incoming);
        loaded = !affected.size || (await this.refresh({ only: affected, detail: false }));
      }
    } catch (error) {
      if (stream === this.stopEvents) {
        this.error = message(error);
        this.connection = 'offline';
      }
    } finally {
      this.flushing = false;
    }
    if (stream !== this.stopEvents) return;
    if (loaded) {
      this.failures = 0;
      this.connection = this.streamState;
    } else {
      this.pendingReset = true;
      this.failures = Math.min(this.failures + 1, 5);
    }
    if (this.pendingReset || this.pendingUsers || this.pendingItems.size)
      this.schedule(loaded ? 100 : Math.min(30000, 1000 * 2 ** this.failures));
  }

  private async loadDirectory(board?: Board, signal?: AbortSignal) {
    const stream = this.stopEvents;
    const [users, tags] = await Promise.all([
      directory<User>('/users', 'users', board?.users.users, board?.users.next_after, signal),
      directory<string>('/tags', 'tags', board?.tags.tags, board?.tags.next_after, signal),
    ]);
    if (!this.active || stream !== this.stopEvents || signal?.aborted) return;
    this.users = users;
    this.tags = tags;
    const current = users.find((user) => user.id === this.userId);
    if (current) this.userChanged(current);
  }

  userChanged(user: User) {
    this.users = this.users.map((current) => (current.id === user.id ? user : current));
    if (user.id === this.userId) this.onuser(user);
  }

  private matches(item: Item) {
    const { status, tag, assignee } = this.filters;
    return (
      (!status || item.status === status) &&
      (!tag || item.tags.includes(tag)) &&
      (!assignee ||
        (assignee === 'none' ? !item.assignees.length : item.assignees.includes(assignee)))
    );
  }

  private synced() {
    this.lastSync = new Date().toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  }

  private acceptDetail(item: Item) {
    if (item.id === this.openId && (!this.detail || item.version > this.detail.version))
      this.detail = item;
  }

  private merge(incoming: Item[], applyOrder = false): Set<Status> {
    const affected = new Set<Status>();
    const byId = new Map(this.items.map((item) => [item.id, item]));
    const tags = new Set(this.tags);
    for (const item of incoming) {
      this.acceptDetail(item);
      const previous = byId.get(item.id);
      if (previous && previous.version >= item.version) continue;
      for (const tag of item.tags) tags.add(tag);
      const matches = this.matches(item);
      const inWindow =
        !this.cursors[item.status] ||
        previous?.status === item.status ||
        this.items.some((card) => card.status === item.status && compare(item, card) <= 0);
      if (
        !previous ||
        !matches ||
        previous.status !== item.status ||
        previous.priority !== item.priority
      ) {
        if (previous && this.cursors[previous.status]) affected.add(previous.status);
        if (matches && inWindow && this.cursors[item.status]) affected.add(item.status);
      }
      if (!matches || !inWindow) byId.delete(item.id);
      else if (previous || !this.cursors[item.status]) byId.set(item.id, summary(item));
    }
    this.items = [...byId.values()];
    if (tags.size !== this.tags.length) this.tags = [...tags].sort();
    if (applyOrder) this.items.sort(compare);
    this.checkOrder();
    this.synced();
    return affected;
  }

  private checkOrder() {
    this.orderChanged = this.columns.some((status) => {
      const column = this.items.filter((item) => item.status === status);
      const sorted = [...column].sort(compare);
      return column.some((item, index) => item.id !== sorted[index].id);
    });
  }

  async refresh({
    order = false,
    reset = false,
    only,
    detail = true,
  }: {
    order?: boolean;
    reset?: boolean;
    only?: Set<Status>;
    detail?: boolean;
  } = {}): Promise<boolean> {
    if (!this.active) return false;
    if (reset) this.pages = {};
    // Every write invalidates older reads. A refresh requested during a write
    // waits for its acknowledgement rather than racing it with a stale snapshot.
    if (this.writing) {
      this.pendingReset = true;
      this.pendingOrder ||= order;
      this.schedule();
      return false;
    }
    this.cancelRead();
    const controller = (this.controller = new AbortController());
    const signal = controller.signal;
    const own = this.generation;
    const filters = { ...this.filters };
    const targetId = this.openId;
    this.syncing = true;
    this.busy = order || !this.hasLoaded;
    try {
      const params = new URLSearchParams();
      for (const [key, value] of Object.entries(filters)) if (value) params.set(key, value);
      const board = only
        ? undefined
        : await api<Board>(`/board?${params}`, 'GET', undefined, signal);
      if (board) await this.loadDirectory(board, signal);
      if (own !== this.generation) return false;
      if (
        board &&
        filters.tag &&
        this.filters.tag === filters.tag &&
        !this.tags.includes(filters.tag)
      ) {
        this.filters.tag = '';
        return this.refresh({ order, reset: true });
      }
      const columns = this.columns.filter((status) => !only || only.has(status));
      const pages = await Promise.all(
        columns.map(async (status) => {
          const preview =
            !filters.status && ['backlog', 'complete', 'void'].includes(status) ? 20 : 100;
          const query = new URLSearchParams(params);
          query.set('status', status);
          const rows: Item[] = [];
          let cursor = '';
          for (let i = 0; i < (this.pages[status] || 1); i++) {
            if (cursor) query.set('cursor', cursor);
            query.set('limit', String(i === 0 ? preview : 100));
            const page =
              i === 0 && board
                ? board.columns[status]!
                : await api<Page>(`/items?${query}`, 'GET', undefined, signal);
            rows.push(...page.items);
            cursor = page.next_cursor || '';
            if (!cursor) break;
          }
          return { status, rows, cursor };
        }),
      );
      if (own !== this.generation) return false;
      const current = new Map(this.items.map((item) => [item.id, item]));
      const incoming = [
        ...this.items.filter((item) => only && !columns.includes(item.status)),
        ...pages.flatMap((page) => page.rows),
      ];
      const unique = new Map<string, Item>();
      for (let item of incoming) {
        const previous = current.get(item.id);
        if (previous && previous.version > item.version) item = previous;
        if (
          (!unique.has(item.id) || item.version > unique.get(item.id)!.version) &&
          this.matches(item)
        )
          unique.set(item.id, item);
      }
      const rows = [...unique.values()].sort(compare);
      if (order || !this.hasLoaded) this.items = rows;
      else {
        const stable = this.items
          .filter((item) => unique.has(item.id))
          .map((item) => unique.get(item.id)!);
        const oldIds = new Set(stable.map((item) => item.id));
        this.items = [...stable, ...rows.filter((item) => !oldIds.has(item.id))];
      }
      this.cursors = {
        ...(only ? this.cursors : {}),
        ...Object.fromEntries(pages.map((page) => [page.status, page.cursor])),
      };
      this.checkOrder();
      this.hasLoaded = true;
      this.error = '';
      this.connection = this.streamState;
      this.synced();
      if (detail && targetId) await this.loadDetail(targetId, signal);
      return true;
    } catch (error) {
      if (signal.aborted || own !== this.generation) return false;
      if (
        error instanceof APIError &&
        error.code === 'cursor_expired' &&
        Object.values(this.pages).some((count) => count > 1)
      ) {
        this.notice = 'Order changed; loaded pages reset.';
        return this.refresh({ order: true, reset: true });
      }
      this.connection = 'offline';
      this.error = message(error);
      return false;
    } finally {
      if (own === this.generation) this.syncing = this.busy = false;
    }
  }

  async more(status: Status) {
    this.pages[status] = (this.pages[status] || 1) + 1;
    await this.refresh({ order: true, only: new Set([status]), detail: false });
  }

  async loadDetail(id = this.openId, signal?: AbortSignal) {
    const own = ++this.detailGeneration;
    this.detailLoading = true;
    try {
      const item = await api<Item>(`/items/${id}`, 'GET', undefined, signal);
      if (own === this.detailGeneration && this.active) this.acceptDetail(item);
    } catch (error) {
      if (own !== this.detailGeneration || signal?.aborted) return;
      if (error instanceof APIError && error.status === 404) this.detailDeleted = true;
      else this.error = message(error);
    } finally {
      if (own === this.detailGeneration) this.detailLoading = false;
    }
  }

  open(id: string) {
    this.detailGeneration++;
    this.openId = id;
    this.detail = null;
    this.detailDeleted = false;
    if (id) return this.loadDetail(id);
  }

  // All ticket mutations, including autosave, use this one read/write boundary.
  private async write<T>(
    request: () => Promise<T>,
    accept: (result: T) => Set<Status>,
  ): Promise<T> {
    if (this.syncing) this.pendingReset = true;
    this.cancelRead();
    const stream = this.stopEvents;
    this.writing++;
    let affected = new Set<Status>();
    try {
      const result = await request();
      if (this.active && stream === this.stopEvents) affected = accept(result);
      return result;
    } finally {
      this.writing--;
      if (this.active && stream === this.stopEvents) {
        if (affected.size) await this.refresh({ order: true, only: affected, detail: false });
        this.schedule();
      }
    }
  }

  create(item: NewItem) {
    return this.write(
      () => api<Item>('/items', 'POST', item),
      (created) => this.merge([created], true),
    );
  }

  update(id: string, version: number, patch: ItemPatch) {
    return this.write(
      () => api<Item>(`/items/${id}`, 'PATCH', { version, ...patch }),
      (item) => this.merge([item], true),
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
      (moved) => this.merge([moved], true),
    );
  }

  async remove(item: Item) {
    await this.write(
      async () => {
        try {
          await api(`/items/${item.id}`, 'DELETE', { version: item.version });
        } catch (error) {
          if (!(error instanceof APIError && error.status === 404)) throw error;
        }
      },
      () => {
        this.items = this.items.filter((current) => current.id !== item.id);
        this.pendingItems.delete(item.id);
        if (this.openId === item.id) this.open('');
        return new Set([item.status]);
      },
    );
  }

  async tagDeleted(name: string) {
    this.tags = this.tags.filter((tag) => tag !== name);
    if (this.filters.tag === name) this.filters.tag = '';
    await this.refresh();
  }
}
