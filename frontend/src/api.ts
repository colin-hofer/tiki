export const statuses = ['backlog', 'todo', 'in_progress', 'code_review', 'blocked', 'complete', 'void'] as const;
export type Status = typeof statuses[number];
export type ItemType = 'task' | 'bug' | 'feature';
export interface User { id: string; name: string; email?: string; role: string }
export interface Item {
  id: string; title: string; description?: string; type: ItemType; status: Status;
  priority: number; version: number; assignees: string[]; tags: string[];
  created_by: string; created_at: string; updated_at: string;
}
export interface Page { items: Item[]; next_cursor?: string }
export interface Activity { id: string; actor_id: string; kind: string; created_at: string; data: unknown }
export interface ActivityPage { activity: Activity[]; next_after?: string }
export interface Session { user: User; session_token: string; expires_at: number }

export const label = (value: string) => value.replaceAll('_', ' ').replace(/^./, c => c.toUpperCase());
export const initials = (name: string) => name.split(/\s+/).map(p => p[0]).slice(0, 2).join('').toUpperCase();

let token = '';
try { token = sessionStorage.getItem('tiki.session') || ''; } catch { /* Private storage can be disabled. */ }
export const hasSession = () => Boolean(token);
export function setSession(value: string) {
  token = value;
  try {
    if (value) sessionStorage.setItem('tiki.session', value);
    else sessionStorage.removeItem('tiki.session');
  } catch { /* Continue with an in-memory session. */ }
}

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

export async function api<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const response = await fetch(`/api/v1${path}`, {
    method, headers, credentials: 'same-origin', cache: 'no-store', redirect: 'error',
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new APIError(response.status, data?.error?.code || 'request_failed', data?.error?.message || `Request failed (${response.status})`);
    if (response.status === 401 && path !== '/auth/login') window.dispatchEvent(new Event('tiki:expired'));
    throw error;
  }
  if (data === null) throw new Error('The server returned an invalid response.');
  return data as T;
}

export async function directory<T>(path: string, key: 'users' | 'tags'): Promise<T[]> {
  const result: T[] = [];
  let after = '';
  do {
    const page = await api<Record<string, T[]> & { next_after?: string }>(`${path}?limit=200${after ? `&after=${encodeURIComponent(after)}` : ''}`);
    result.push(...page[key]);
    if (page.next_after === after) break;
    after = page.next_after || '';
  } while (after);
  return result;
}
