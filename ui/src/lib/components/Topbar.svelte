<script lang="ts">
  interface Props {
    title: string
    online: boolean
    onSignOut: () => void
  }

  let { title, online, onSignOut }: Props = $props()
</script>

<header class="navbar bg-base-100 border-base-300 sticky top-0 z-30 min-h-16 border-b px-2 sm:px-4">
  <div class="navbar-start gap-2">
    <label for="app-drawer" class="btn btn-ghost btn-square lg:hidden" aria-label="Menu">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" class="size-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" d="M4 6h16M4 12h16M4 18h16" />
      </svg>
    </label>
    <!-- The brand lives in the sidebar; the topbar names the current view. -->
    <h1 class="text-base font-semibold sm:text-lg">{title}</h1>
  </div>

  <div class="navbar-end gap-2">
    <!-- Connection state: the badge follows the socket and the Asterisk status -->
    <span class="badge badge-sm gap-2 {online ? 'badge-success' : 'badge-error'}">
      <span class="inline-block size-2 rounded-full bg-current animate-pulse"></span>
      <span>{online ? 'Online' : 'Offline'}</span>
    </span>

    <!-- Theme switch: the icon follows the data-theme attribute -->
    <button type="button" class="btn btn-ghost btn-circle btn-sm" data-theme-toggle aria-label="Light / dark theme">
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" class="theme-icon theme-icon-sun size-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.4-6.4-.7.7M6.3 17.7l-.7.7m12.8 0-.7-.7M6.3 6.3l-.7-.7" />
        <circle cx="12" cy="12" r="4" />
      </svg>
      <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" class="theme-icon theme-icon-moon size-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" />
      </svg>
    </button>

    <div class="dropdown dropdown-end">
      <div tabindex="0" role="button" class="btn btn-ghost btn-circle avatar" aria-label="Account menu">
        <div class="bg-primary text-primary-content w-8 rounded-full">
          <span class="flex h-full items-center justify-center text-xs font-semibold">AD</span>
        </div>
      </div>
      <!-- daisyUI's dropdown needs the focusable content element; role="menu" is not set on it. -->
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <ul tabindex="0" class="dropdown-content menu bg-base-100 rounded-box border-base-300 z-40 mt-2 w-52 border p-2 shadow-lg">
        <li><button type="button" onclick={onSignOut}>Sign out</button></li>
      </ul>
    </div>
  </div>
</header>
