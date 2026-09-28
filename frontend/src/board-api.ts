import { api, directory, statuses } from './api';
import type { Board, Page, Status, User } from './api';
import type { Filters } from './items';

export type Pages = Partial<Record<Status, number>>;
export type Columns = Partial<Record<Status, Page>>;

function queryFor(filters: Filters) {
  return new URLSearchParams(Object.entries(filters).filter(([, value]) => value));
}

export async function readDirectory(signal: AbortSignal, board?: Board) {
  const [users, tags] = await Promise.all([
    directory<User>('/users', 'users', board?.users.users, board?.users.next_after, signal),
    directory<string>('/tags', 'tags', board?.tags.tags, board?.tags.next_after, signal),
  ]);
  return { users, tags };
}

export async function readColumns(
  columns: Status[],
  filters: Filters,
  pages: Pages,
  signal: AbortSignal,
  initial: Columns = {},
): Promise<Columns> {
  const entries = await Promise.all(
    columns.map(async (status) => {
      const query = queryFor(filters);
      query.set('status', status);
      const items: Page['items'] = [];
      let cursor = '';
      for (let index = 0; index < (pages[status] || 1); index++) {
        const preview = !filters.status && ['backlog', 'complete', 'void'].includes(status);
        query.set('limit', String(index === 0 && preview ? 20 : 100));
        if (cursor) query.set('cursor', cursor);
        const page =
          index === 0 && initial[status]
            ? initial[status]
            : await api<Page>(`/items?${query}`, 'GET', undefined, signal);
        items.push(...page.items);
        cursor = page.next_cursor || '';
        if (!cursor) break;
      }
      return [status, { items, next_cursor: cursor }] as const;
    }),
  );
  return Object.fromEntries(entries);
}

export async function readBoard(filters: Filters, pages: Pages, signal: AbortSignal) {
  const board = await api<Board>(`/board?${queryFor(filters)}`, 'GET', undefined, signal);
  const [people, columns] = await Promise.all([
    readDirectory(signal, board),
    readColumns(
      statuses.filter((status) => !filters.status || status === filters.status),
      filters,
      pages,
      signal,
      board.columns,
    ),
  ]);
  return { ...people, columns };
}
