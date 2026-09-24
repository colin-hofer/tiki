<script lang="ts">
  import { onMount } from 'svelte';
  import { api, label } from './api';
  import type { Session } from './api';

  let { token, onjoin }: { token: string; onjoin: (session: Session) => Promise<void> } = $props();
  let invite = $state<{ role: string; expires_at: number }>();
  let checking = $state(true);
  let busy = $state(false);
  let error = $state('');
  let name = $state('');
  let email = $state('');
  let password = $state('');
  let confirmation = $state('');

  async function inspect() {
    checking = true; error = '';
    try { invite = await api('/auth/invite', 'POST', { token }); }
    catch (e) { error = e instanceof Error ? e.message : 'Could not check this invite. Please try again.'; }
    finally { checking = false; }
  }
  onMount(() => { void inspect(); });

  async function join(event: SubmitEvent) {
    event.preventDefault(); error = '';
    if (password !== confirmation) { error = 'Passwords do not match.'; return; }
    busy = true;
    try {
      const session = await api<Session>('/auth/join', 'POST', { token, name, email, password });
      password = ''; confirmation = '';
      await onjoin(session);
    } catch (e) { error = e instanceof Error ? e.message : 'Could not create your account. Please try again.'; }
    finally { busy = false; }
  }
</script>

<div class="login-screen">
  <form class="login-form" onsubmit={join}>
    <h1><span class="logo-mark" aria-hidden="true"></span>tiki</h1>
    <p class="login-sub">Join your workspace</p>
    {#if checking}<p role="status">Checking your invite…</p>{/if}
    {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
    {#if invite}
      <p class="hint">{label(invite.role)} access · Expires {new Date(invite.expires_at * 1000).toLocaleDateString()}</p>
      <label>Name<input autocomplete="name" required maxlength="200" bind:value={name} disabled={busy} /></label>
      <label>Email<input type="email" autocomplete="username" required maxlength="254" bind:value={email} disabled={busy} /></label>
      <label>Password<input type="password" autocomplete="new-password" aria-describedby="password-hint" required minlength="8" maxlength="1024" bind:value={password} disabled={busy} /></label>
      <span id="password-hint" class="hint">At least 8 characters</span>
      <label>Confirm password<input type="password" autocomplete="new-password" required minlength="8" maxlength="1024" bind:value={confirmation} disabled={busy} /></label>
      <button class="primary-button" disabled={busy}>{busy ? 'Creating account…' : 'Create account'}</button>
    {:else if !checking}<button type="button" class="small-button" onclick={inspect}>Check invite again</button>{/if}
    <a href="/" class="hint">Already have an account? Sign in</a>
  </form>
</div>
