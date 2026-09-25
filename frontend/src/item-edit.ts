import type { Item, ItemPatch } from './api';

export type EditorField = 'title' | 'description' | 'status' | 'assignee' | 'tags' | 'type';
export type ItemFields = Pick<Item, 'title' | 'status' | 'type' | 'tags' | 'assignees'> & {
  description: string;
};
export function itemFields(item: Item): ItemFields {
  return {
    title: item.title,
    description: item.description || '',
    status: item.status,
    type: item.type,
    tags: [...item.tags],
    assignees: [...item.assignees],
  };
}

export function itemPatch(base: Item | ItemFields, draft: ItemFields): ItemPatch {
  const patch: ItemPatch = {};
  if (draft.title !== base.title) patch.title = draft.title;
  if (draft.description !== (base.description || '')) patch.description = draft.description;
  if (draft.status !== base.status) patch.status = draft.status;
  if (draft.type !== base.type) patch.type = draft.type;
  for (const [field, add, remove] of [
    ['tags', 'add_tags', 'remove_tags'],
    ['assignees', 'add_assignees', 'remove_assignees'],
  ] as const) {
    const added = draft[field].filter((value) => !base[field].includes(value));
    const removed = base[field].filter((value) => !draft[field].includes(value));
    if (added.length) patch[add] = added;
    if (removed.length) patch[remove] = removed;
  }
  return patch;
}

// Keep edits made since `before`, accepting every other field from the server.
// Used both for acknowledgements during typing and explicit conflict resolution.
export function rebaseFields(
  latest: Item,
  before: Item | ItemFields,
  draft: ItemFields,
): ItemFields {
  const {
    add_tags = [],
    remove_tags = [],
    add_assignees = [],
    remove_assignees = [],
    ...fields
  } = itemPatch(before, draft);
  return {
    ...itemFields(latest),
    ...fields,
    tags: [...new Set([...latest.tags.filter((tag) => !remove_tags.includes(tag)), ...add_tags])],
    assignees: [
      ...new Set([
        ...latest.assignees.filter((id) => !remove_assignees.includes(id)),
        ...add_assignees,
      ]),
    ],
  };
}
