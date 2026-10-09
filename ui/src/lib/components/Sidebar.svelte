<script module lang="ts">
  // The section ids match the ids of the rendered sections, so the sidebar can
  // also drive the scroll-sync in App.svelte (the active item follows the
  // section in view). Keep the order in sync with the page sections.
  export const NAV_ITEMS = [
    { id: 'overview', label: 'Overview' },
    { id: 'active-calls', label: 'Active calls' },
    { id: 'contacts', label: 'Contacts' },
    { id: 'endpoints', label: 'Endpoints' },
    { id: 'trunks', label: 'Trunks' },
    { id: 'history', label: 'Call history' },
  ] as const

  export type NavId = (typeof NAV_ITEMS)[number]['id']
</script>

<script lang="ts">
  import BrandTile from './BrandTile.svelte'

  interface Props {
    activeId: NavId
  }

  let { activeId }: Props = $props()

  // On mobile, tapping a sidebar item closes the drawer.
  function closeDrawer(): void {
    const drawer = document.getElementById('app-drawer')
    if (drawer instanceof HTMLInputElement) drawer.checked = false
  }
</script>

<aside class="drawer-side z-40">
  <label for="app-drawer" class="drawer-overlay" aria-label="Close"></label>
  <div class="bg-base-100 border-base-300 flex min-h-full w-72 flex-col border-r" data-mobile-menu>
    <div class="border-base-300 flex h-16 items-center gap-2 border-b px-4">
      <BrandTile />
      <span class="font-semibold">Asterisk Dashboard</span>
    </div>

    <nav class="flex-1 overflow-y-auto p-3">
      <ul class="menu w-full gap-1">
        <li class="menu-title">Monitoring</li>
        <!-- Scrolls back to the top; the active item stays in sync with the
             section in view (App.svelte). -->
        {#each NAV_ITEMS as item (item.id)}
          <li>
            <a
              href={'#' + item.id}
              class={activeId === item.id ? 'menu-active' : undefined}
              aria-current={activeId === item.id ? 'page' : undefined}
              onclick={closeDrawer}
            >
              {#if item.id === 'overview'}
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" class="size-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 10.5 12 3l9 7.5M5.5 9.5V21h13V9.5" />
                </svg>
              {:else if item.id === 'active-calls'}
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" class="size-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 5a2 2 0 0 1 2-2h2.3a1 1 0 0 1 1 .8l.8 3a1 1 0 0 1-.3 1L7.5 9a12 12 0 0 0 6.5 6.5l1.2-1.3a1 1 0 0 1 1-.3l3 .8a1 1 0 0 1 .8 1V18a2 2 0 0 1-2 2h-.5A16.5 16.5 0 0 1 3 5.5V5Z" />
                </svg>
              {:else if item.id === 'contacts'}
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" class="size-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm13 10v-2a4 4 0 0 0-3-3.9" />
                </svg>
              {:else if item.id === 'endpoints'}
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" class="size-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 12h7V3H3v9Zm0 9h7v-5H3v5Zm11 0h7V12h-7v9Zm0-18v5h7V3h-7Z" />
                </svg>
              {:else if item.id === 'trunks'}
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" class="size-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 3v18M5 8l7-5 7 5M5 16l7 5 7-5" />
                </svg>
              {:else}
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" class="size-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M3 3v18h18M7 15l4-4 3 3 5-6" />
                </svg>
              {/if}
              {item.label}
            </a>
          </li>
        {/each}
      </ul>
    </nav>

    <div class="border-base-300 border-t p-3">
      <div class="bg-base-200 rounded-box p-3 text-xs">
        <p class="font-medium">AMI event stream</p>
        <p class="text-base-content/70 mt-1">State is pushed over WebSocket as Asterisk events arrive.</p>
      </div>
    </div>
  </div>
</aside>
