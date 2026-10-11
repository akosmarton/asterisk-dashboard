<script lang="ts">
  import { login } from '../api'
  import BrandTile from './BrandTile.svelte'

  interface Props {
    password: boolean
    oidc: boolean
    onSuccess: () => void
  }

  let { password, oidc, onSuccess }: Props = $props()

  let error = $state('')

  // A refused SSO callback lands back here with the reason in the query string.
  // Read it once, then drop it so a reload does not repeat the message.
  const authErrorParam = new URLSearchParams(window.location.search).get('auth_error')
  let authError = $state(
    authErrorParam === 'forbidden'
      ? 'This account is not permitted to sign in.'
      : authErrorParam
        ? 'Sign-in failed. Please try again.'
        : '',
  )
  if (authErrorParam) history.replaceState(null, '', '/')

  async function handleSubmit(event: SubmitEvent): Promise<void> {
    event.preventDefault()
    const form = event.currentTarget as HTMLFormElement
    const formData = new FormData(form)
    const username = String(formData.get('username') ?? '')
    const enteredPassword = String(formData.get('password') ?? '')

    const result = await login(username, enteredPassword)
    if (result.ok) {
      error = ''
      onSuccess()
    } else {
      error = result.error
    }
  }
</script>

<div class="bg-base-200 grid min-h-dvh place-items-center p-4">
  <div class="w-full max-w-sm">
    <div class="mb-6 flex items-center justify-center gap-2">
      <BrandTile />
      <span class="font-semibold">Asterisk Dashboard</span>
    </div>

    <div class="card bg-base-100 border-base-300 border">
      <div class="card-body gap-4">
        <h1 class="card-title text-base">Sign in</h1>

        {#if authError || error}
          <div role="alert" class="alert alert-error">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" class="size-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9" />
              <path stroke-linecap="round" d="m15 9-6 6m0-6 6 6" />
            </svg>
            <div class="grid gap-1">
              {#if authError}
                <span>{authError}</span>
              {/if}
              {#if error}
                <span>{error}</span>
              {/if}
            </div>
          </div>
        {/if}

        {#if password}
          <form class="grid gap-4" onsubmit={handleSubmit}>
            <fieldset class="fieldset">
              <label class="label" for="username">Username</label>
              <!-- Autofocus is deliberate: the sign-in form is the only thing on screen. -->
              <!-- svelte-ignore a11y_autofocus -->
              <input id="username" name="username" type="text" class="input w-full" required autofocus autocomplete="username" />
            </fieldset>

            <fieldset class="fieldset">
              <label class="label" for="password">Password</label>
              <input id="password" name="password" type="password" class="input w-full" required autocomplete="current-password" />
            </fieldset>

            <button class="btn btn-primary w-full" type="submit">Sign in</button>
          </form>
        {/if}

        {#if password && oidc}
          <div class="divider text-xs">or</div>
        {/if}

        {#if oidc}
          <!-- SSO is a navigation, not a fetch: the browser leaves the SPA here. -->
          <a class="btn w-full {password ? 'btn-outline' : 'btn-primary'}" href="/api/oidc/login">Sign in with SSO</a>
        {/if}

        {#if !password && !oidc}
          <div role="alert" class="alert alert-info">
            <span>No sign-in method is configured. This dashboard is reachable only with an API key.</span>
          </div>
        {/if}
      </div>
    </div>

    <p class="text-base-content/70 mt-6 text-center text-xs">
      Real-time PBX metrics over WebSocket
    </p>
  </div>
</div>
