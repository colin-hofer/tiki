export const statuses = ['backlog', 'todo', 'in_progress', 'code_review', 'blocked', 'complete', 'void'] as const;
export type Status = typeof statuses[number];
export type ItemType = 'task' | 'bug' | 'feature';
export interface User { id: string; name: string; email?: string; role: string; removed_at?: number }
export interface Item {
  id: string; title: string; description?: string; type: ItemType; status: Status;
  priority: number; version: number; assignees: string[]; tags: string[];
  created_by: string; created_at: string; updated_at: string;
}
export interface Page { items: Item[]; next_cursor?: string }
export interface Board {
  columns: Partial<Record<Status, Page>>;
  users: { users: User[]; next_after?: string };
  tags: { tags: string[]; next_after?: string };
}
export interface Activity { id: string; actor_id: string; kind: string; created_at: string; data: unknown }
export interface ActivityPage { activity: Activity[]; next_after?: string }
export interface Session { user: User; session_token: string; expires_at: number }
export interface Updates { items?: Item[]; users?: boolean; reset?: boolean }

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
  const requestToken = token;
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (requestToken) headers.Authorization = `Bearer ${requestToken}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const response = await fetch(`/api/v1${path}`, {
    method, headers, credentials: 'same-origin', cache: 'no-store', redirect: 'error',
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    const error = new APIError(response.status, data?.error?.code || 'request_failed', data?.error?.message || `Request failed (${response.status})`);
    if (response.status === 401 && token === requestToken && path !== '/auth/login') window.dispatchEvent(new Event('tiki:expired'));
    throw error;
  }
  if (data === null) throw new Error('The server returned an invalid response.');
  return data as T;
}

// Use fetch so the existing bearer token stays in a header, never a URL. This
// reads our server's LF-delimited SSE frames; it is not a general SSE client.
export function watchChanges(changed: (updates: Updates) => void, state: (value: 'connecting' | 'live' | 'offline') => void): () => void {
  const requestToken = token;
  let stopped = false;
  let failures = 0;
  let controller: AbortController;
  let retry: ReturnType<typeof setTimeout>;
  let timeout: ReturnType<typeof setTimeout>;
  const stop = () => { stopped = true; clearTimeout(retry); clearTimeout(timeout); controller?.abort(); };
  const expired = () => {
    stop();
    if (token === requestToken) window.dispatchEvent(new Event('tiki:expired'));
  };
  async function connect() {
    controller = new AbortController();
    const alive = () => { clearTimeout(timeout); timeout = setTimeout(() => controller.abort(), 45000); };
    state('connecting'); alive();
    let reader: ReadableStreamDefaultReader<string> | undefined;
    try {
      const response = await fetch('/api/v1/events', {
        headers: { Accept: 'text/event-stream', Authorization: `Bearer ${requestToken}` },
        credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: controller.signal,
      });
      if (response.status === 401) { expired(); return; }
      if (!response.ok || !response.headers.get('Content-Type')?.startsWith('text/event-stream') || !response.body) throw new Error('Event stream unavailable');
      reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
      let buffer = '';
      while (!stopped) {
        const { value, done } = await reader.read();
        if (stopped) return;
        if (done) throw new Error('Event stream closed');
        alive(); buffer += value;
        let end: number;
        while ((end = buffer.indexOf('\n\n')) !== -1) {
          if (end > 4 << 20) throw new Error('Event stream frame too large');
          const frame = buffer.slice(0, end); buffer = buffer.slice(end + 2);
          const kind = /^event: (ready|change|expired)$/m.exec(frame)?.[1];
          if (kind === 'expired') { expired(); return; }
          if (kind === 'ready') { failures = 0; state('live'); }
          if (kind === 'ready' || kind === 'change') {
            const data = /^data: (.+)$/m.exec(frame)?.[1];
            if (!data) throw new Error('Missing event data');
            changed(JSON.parse(data) as Updates);
          }
        }
        if (buffer.length > 4 << 20) throw new Error('Event stream frame too large');
      }
    } catch {
      if (!stopped) {
        state('offline');
        const delay = Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
        retry = setTimeout(() => void connect(), delay + Math.random() * delay * 0.2);
      }
    } finally {
      clearTimeout(timeout); controller.abort();
      await reader?.cancel().catch(() => {});
      reader?.releaseLock();
    }
  }
  void connect();
  return stop;
}

export async function directory<T>(path: string, key: 'users' | 'tags', initial?: T[], after = '', signal?: AbortSignal): Promise<T[]> {
  const result: T[] = initial ? [...initial] : [];
  if (initial && !after) return result;
  do {
    const page = await api<Record<string, T[]> & { next_after?: string }>(`${path}?limit=200${after ? `&after=${encodeURIComponent(after)}` : ''}`, 'GET', undefined, signal);
    result.push(...page[key]);
    if (page.next_after === after) break;
    after = page.next_after || '';
  } while (after);
  return result;
}
