<script lang="ts">
  import { login } from '../api'
  import BrandTile from './BrandTile.svelte'

  interface Props {
    onSuccess: () => void
  }

  let { onSuccess }: Props = $props()

  let error = $state('')

  async function handleSubmit(event: SubmitEvent): Promise<void> {
    event.preventDefault()
    const form = event.currentTarget as HTMLFormElement
    const formData = new FormData(form)
    const username = String(formData.get('username') ?? '')
    const password = String(formData.get('password') ?? '')

    const result = await login(username, password)
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

        {#if error}
          <div role="alert" class="alert alert-error">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" class="size-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <circle cx="12" cy="12" r="9" />
              <path stroke-linecap="round" d="m15 9-6 6m0-6 6 6" />
            </svg>
            <span>{error}</span>
          </div>
        {/if}

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
      </div>
    </div>

    <p class="text-base-content/70 mt-6 text-center text-xs">
      Real-time PBX metrics over WebSocket
    </p>
  </div>
</div>
