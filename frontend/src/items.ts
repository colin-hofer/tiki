import { statuses } from './api';
import type { Item, Status } from './api';

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
