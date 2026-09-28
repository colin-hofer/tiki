import { statuses } from './api';
import type { Item, Status } from './api';

export interface Filters {
  status: Status | '';
  tag: string;
  assignee: string;
}

export function matchesFilters(item: Item, filters: Filters) {
  const { status, tag, assignee } = filters;
  return (
    (!status || item.status === status) &&
    (!tag || item.tags.includes(tag)) &&
    (!assignee ||
      (assignee === 'none' ? !item.assignees.length : item.assignees.includes(assignee)))
  );
}

export const compareItems = (a: Item, b: Item) =>
  a.priority - b.priority ||
  (BigInt(a.id) < BigInt(b.id) ? -1 : BigInt(a.id) > BigInt(b.id) ? 1 : 0);

export function groupItems(items: Item[]): Record<Status, Item[]> {
  const grouped = Object.fromEntries<Item[]>(statuses.map((status) => [status, []])) as Record<
    Status,
    Item[]
  >;
  for (const item of items) grouped[item.status].push(item);
  return grouped;
}

// Snapshots replace column membership; individual updates replace only their own ticket.
// Both paths keep the newest version and reuse unchanged objects and arrays.
export function reconcileItems(
  current: Item[],
  incoming: Item[],
  filters: Filters,
  replace: Status[] = [],
  order = false,
): Item[] {
  const previous = new Map(current.map((item) => [item.id, item]));
  const next = new Map(
    current
      .filter((item) => !replace.includes(item.status) && matchesFilters(item, filters))
      .map((item) => [item.id, item]),
  );
  for (const item of incoming) {
    const known = next.get(item.id) || previous.get(item.id);
    const latest = known && known.version >= item.version ? known : item;
    if (matchesFilters(latest, filters)) {
      const { description, ...summary } = latest;
      next.set(item.id, latest === known ? known : summary);
    } else next.delete(item.id);
  }
  // Preserve existing positions during live updates; newly visible tickets append in server order.
  const rows: Item[] = [];
  if (!order) {
    for (const existing of current) {
      const item = next.get(existing.id);
      if (item) rows.push(item);
      next.delete(existing.id);
    }
  }
  rows.push(...[...next.values()].sort(compareItems));
  return rows.length === current.length && rows.every((item, i) => item === current[i])
    ? current
    : rows;
}

// Live changes update known tickets directly. Changes at a pagination boundary need a reload.
export function applyItemChanges(
  current: Item[],
  incoming: Item[],
  cursors: Partial<Record<Status, string>>,
  filters: Filters,
  order = false,
): { items: Item[]; columns: Set<Status> } {
  const next = new Map(current.map((item) => [item.id, item]));
  const columns = new Set<Status>();
  for (const item of incoming) {
    const previous = next.get(item.id);
    if (previous && previous.version >= item.version) continue;
    const matches = matchesFilters(item, filters);
    const inWindow =
      !cursors[item.status] ||
      previous?.status === item.status ||
      current.some((card) => card.status === item.status && compareItems(item, card) <= 0);
    if (
      !previous ||
      !matches ||
      previous.status !== item.status ||
      previous.priority !== item.priority
    ) {
      if (previous && cursors[previous.status]) columns.add(previous.status);
      if (matches && inWindow && cursors[item.status]) columns.add(item.status);
    }
    if (!matches || !inWindow) next.delete(item.id);
    else if (previous || !cursors[item.status]) next.set(item.id, item);
  }
  return {
    items: reconcileItems(current, [...next.values()], filters, [...statuses], order),
    columns,
  };
}
