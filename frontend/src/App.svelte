<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, APIError, hasSession, message, setSession } from './api';
  import type { Session, User } from './api';
  import Join from './Join.svelte';
  import Workspace from './Workspace.svelte';

  const initialInvite = new URLSearchParams(location.hash.slice(1)).get('invite');
  let inviteToken = $state(initialInvite);
  let user = $state<User | null>(null);
  let expired = $state(false);
  let checking = $state(initialInvite === null && hasSession());
  let email = $state('');
  let password = $state('');
  let signingIn = $state(false);
  let error = $state('');
  let notice = $state('');
  let workspace = $state<Workspace>();

  async function signedIn(session: Session) {
    if (inviteToken !== null) {
      const url = new URL(location.href);
      url.hash = '';
      history.replaceState(null, '', url);
      inviteToken = null;
    }
    setSession(session.session_token);
    user = session.user;
    password = '';
    expired = false;
    notice = '';
  }
  async function login(event: SubmitEvent) {
    event.preventDefault();
    signingIn = true;
    error = '';
    try {
      await signedIn(await api<Session>('/auth/login', 'POST', { email, password }));
    } catch (cause) {
      error = message(cause);
    } finally {
      signingIn = false;
    }
  }
  function expire() {
    if (expired) return;
    expired = true;
    setSession('');
    error = 'Your session expired. Sign in again to continue. Your open draft is preserved.';
  }
  function userChanged(changed: User) {
    if (changed.removed_at) expire();
    else user = changed;
  }
  async function logout() {
    try {
      await api('/auth/logout', 'POST');
    } catch (cause) {
      if (!(cause instanceof APIError && cause.status === 401)) throw cause;
    }
    setSession('');
    user = null;
    expired = false;
    password = '';
    const url = new URL(location.href);
    url.searchParams.delete('item');
    history.replaceState(null, '', url);
  }
  async function passwordChanged() {
    email = user?.email || email;
    expire();
    error = '';
    notice = 'Password changed. Sign in again with your new password.';
    await tick();
    document.querySelector<HTMLInputElement>('.login-form input[type="password"]')?.focus();
  }
  onMount(() => {
    if (inviteToken === null && hasSession()) {
      void api<User>('/auth/me')
        .then((value) => (user = value))
        .catch((cause) => {
          setSession('');
          error = message(cause);
        })
        .finally(() => (checking = false));
    }
    const hashchange = async () => {
      const url = new URL(location.href);
      if (new URLSearchParams(url.hash.slice(1)).get('invite') === inviteToken) return;
      if (workspace && !(await workspace.guardDraft())) {
        url.hash = inviteToken === null ? '' : `invite=${encodeURIComponent(inviteToken)}`;
        history.replaceState(null, '', url);
      } else location.reload();
    };
    window.addEventListener('tiki:expired', expire);
    window.addEventListener('hashchange', hashchange);
    return () => {
      window.removeEventListener('tiki:expired', expire);
      window.removeEventListener('hashchange', hashchange);
    };
  });
</script>

{#if inviteToken !== null}
  <Join token={inviteToken} onjoin={signedIn} />
{:else if !user || expired}
  <div class="login-screen">
    <form class="login-form" onsubmit={login}>
      <h1><span class="logo-mark" aria-hidden="true"></span>tiki</h1>
      <p class="login-sub">Sign in to your workspace</p>
      {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
      {#if notice}<p class="notice-banner" role="status">{notice}</p>{/if}
      <label
        >Email<input
          type="email"
          autocomplete="username"
          required
          bind:value={email}
          disabled={checking || signingIn}
        /></label
      >
      <label
        >Password<input
          type="password"
          autocomplete="current-password"
          required
          bind:value={password}
          disabled={checking || signingIn}
        /></label
      >
      <button class="primary-button" disabled={checking || signingIn}
        >{checking ? 'Restoring session…' : signingIn ? 'Signing in…' : 'Sign in'}</button
      >
    </form>
  </div>
{/if}
{#if user && inviteToken === null}
  {#key user.id}
    <Workspace
      bind:this={workspace}
      {user}
      {expired}
      onuser={userChanged}
      onlogout={logout}
      onpasswordchanged={passwordChanged}
    />
  {/key}
{/if}
