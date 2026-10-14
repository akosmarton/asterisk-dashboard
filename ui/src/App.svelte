<script lang="ts">
  import { onMount } from 'svelte'

  import { connectDashboardSocket, getData, getSession, logout, type SessionResponse } from './lib/api'
  import { getBadgeClass, getDispositionBadgeClass } from './lib/badges'
  import { initTheme } from './lib/theme'
  import type { DashboardData } from './lib/types'
  import DataTable from './lib/components/DataTable.svelte'
  import LoginView from './lib/components/LoginView.svelte'
  import MetricCard from './lib/components/MetricCard.svelte'
  import Sidebar, { NAV_ITEMS, type NavId } from './lib/components/Sidebar.svelte'
  import Topbar from './lib/components/Topbar.svelte'

  // 'booting' renders nothing on purpose: until /api/session answers we do not
  // know which view is right, and rendering the dashboard meanwhile would flash
  // it behind the login view.
  //
  // Note: the Tailwind scanner reads string literals, so a state name that
  // matches a daisyUI component would pull that component into the bundle.
  type ViewState = 'booting' | 'signed-out' | 'signed-in'

  // The sticky topbar is min-h-16 (64px) and the sections scroll with
  // scroll-mt-20 (80px), hence the line below (see dashboard.js).
  const NAV_LINE = 96

  let view = $state<ViewState>('booting')
  let data = $state<DashboardData | null>(null)
  let socketOpen = $state(false)
  let activeId = $state<NavId>('overview')
  // The whole /api/session response: the sign-in methods the login view offers
  // and the identity behind the account button.
  let session = $state<SessionResponse | null>(null)

  initTheme()

  onMount(() => {
    getSession()
      .then((s) => {
        session = s
        view = s.authenticated ? 'signed-in' : 'signed-out'
      })
      .catch(() => {
        view = 'signed-out'
      })
  })

  // Fetch the initial snapshot and follow every change over the WebSocket.
  $effect(() => {
    if (view !== 'signed-in') return

    let cancelled = false

    getData().then((result) => {
      if (cancelled) return
      if (result.ok) data = result.data
      else if (result.unauthorized) view = 'signed-out'
    })

    const close = connectDashboardSocket({
      onOpen: () => {
        socketOpen = true
      },
      onData: (incoming) => {
        data = incoming
      },
      onClose: () => {
        socketOpen = false
        // The session can expire, or the server restart, while the page is open.
        // The socket dropping is the moment to find out: otherwise stale tables
        // would sit behind an "Offline" badge forever. Changing the view stops
        // the reconnect loop through this effect's cleanup.
        getSession()
          .then((s) => {
            session = s
            if (!cancelled && !s.authenticated) view = 'signed-out'
          })
          .catch(() => {})
      },
    })

    return () => {
      cancelled = true
      close()
    }
  })

  // The active item — and the topbar title — follow the section in view, so the
  // sidebar does not go stale after a jump.
  $effect(() => {
    if (view !== 'signed-in') return

    const onScroll = () => requestAnimationFrame(syncActiveNav)
    window.addEventListener('scroll', onScroll, { passive: true })
    syncActiveNav()

    return () => window.removeEventListener('scroll', onScroll)
  })

  function syncActiveNav(): void {
    // The last section whose top has passed the line is the one in view.
    let current: NavId = NAV_ITEMS[0].id
    for (const item of NAV_ITEMS) {
      const section = document.getElementById(item.id)
      if (section && section.getBoundingClientRect().top <= NAV_LINE) current = item.id
    }

    // On a short page the last section can never reach the line, so highlight it
    // once the scroll is at the bottom; otherwise the previous item would stick.
    const atBottom =
      window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2
    if (atBottom) current = NAV_ITEMS[NAV_ITEMS.length - 1]?.id ?? NAV_ITEMS[0].id

    activeId = current
  }

  async function handleSignOut(): Promise<void> {
    await logout()
    data = null
    socketOpen = false
    view = 'signed-out'
  }

  async function handleLoginSuccess(): Promise<void> {
    data = null
    // Re-read the session so the account button shows the identity just signed
    // in as; the login response only says that it worked.
    session = await getSession().catch(() => session)
    view = 'signed-in'
  }

  const calls = $derived(data?.active_channels_list ?? [])
  const contacts = $derived(data?.contacts ?? [])
  const endpoints = $derived(data?.endpoints ?? [])
  const trunks = $derived(data?.trunks ?? [])
  const history = $derived(data?.call_history ?? [])

  const version = $derived(data ? data.global.version || 'Asterisk 23' : '')
  const lastUpdated = $derived(data?.global.last_updated ?? '')
  const uptime = $derived(data?.global.uptime || 'N/A')
  const activeCalls = $derived(data?.global.active_calls || '0')
  const activeChannels = $derived(data?.global.active_channels || '0')
  const processed = $derived(data?.global.calls_processed || '0')

  const online = $derived(socketOpen && data?.global.status === 'Online')
  const activeLabel = $derived(NAV_ITEMS.find((item) => item.id === activeId)?.label ?? 'Overview')
</script>

{#if view === 'signed-out'}
  <LoginView
    password={session?.password_login ?? false}
    oidc={session?.oidc_login ?? false}
    onSuccess={handleLoginSuccess}
  />
{:else if view === 'signed-in'}
  <div class="drawer lg:drawer-open">
    <input id="app-drawer" type="checkbox" class="drawer-toggle" />

    <!-- ================= Page content ================= -->
    <div class="drawer-content flex min-h-dvh flex-col">
      <Topbar title={activeLabel} {online} user={session?.user ?? null} onSignOut={handleSignOut} />

      <!-- Main content -->
      <main class="flex-1 p-4 sm:p-6">
        <div id="overview" class="mx-auto w-full max-w-7xl scroll-mt-20 space-y-6">
          <p class="text-base-content/70 text-sm">
            <span>{version}</span> &bull; last updated
            <span>{lastUpdated}</span>
          </p>

          <!-- Stat cards -->
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <MetricCard label="Asterisk uptime" value={uptime} hint="since Asterisk start" />
            <MetricCard label="Active calls" value={activeCalls} hint="live conversations" />
            <MetricCard label="Active channels" value={activeChannels} hint="in use right now" />
            <MetricCard label="Processed calls" value={processed} hint="this session" />
          </div>

          <!-- Active calls, then PJSIP contacts — both full width -->
          <div class="space-y-6">
            <DataTable
              id="active-calls"
              title={'Active calls & channels'}
              count={calls.length}
              colspan={6}
              empty="No active calls"
            >
              {#snippet head()}
                <tr>
                  <th>Channel</th>
                  <th>Caller ID</th>
                  <th>Target / destination</th>
                  <th>State</th>
                  <th>Application</th>
                  <th>Duration</th>
                </tr>
              {/snippet}
              {#snippet rows()}
                {#each calls as c}
                  <tr>
                    <td class="font-medium">{c.channel}</td>
                    <td>{c.caller_id_num || '-'}{#if c.caller_id_name} ({c.caller_id_name}){/if}</td>
                    <td>{c.connected_line_num || c.extension || '-'}</td>
                    <td>
                      <span class="badge badge-sm {getBadgeClass(c.channel_state_desc)}">{c.channel_state_desc || '-'}</span>
                    </td>
                    <td>{c.application || '-'}{#if c.app_data} ({c.app_data}){/if}</td>
                    <td>{c.duration || '0'} s</td>
                  </tr>
                {/each}
              {/snippet}
            </DataTable>

            <DataTable
              id="contacts"
              title="PJSIP contacts"
              count={contacts.length}
              colspan={3}
              empty="No registered contacts"
            >
              {#snippet head()}
                <tr>
                  <th>Contact / URI</th>
                  <th>Status</th>
                  <th class="text-right">RTT</th>
                </tr>
              {/snippet}
              {#snippet rows()}
                {#each contacts as c}
                  <tr>
                    <td class="break-all font-medium">{c.object_name}</td>
                    <td><span class="badge badge-sm {getBadgeClass(c.status)}">{c.status || '-'}</span></td>
                    <td class="text-right tabular-nums">{c.roundtrip || '-'}</td>
                  </tr>
                {/each}
              {/snippet}
            </DataTable>
          </div>

          <!-- Endpoints -->
          <DataTable
            id="endpoints"
            title="PJSIP endpoints"
            count={endpoints.length}
            colspan={4}
            empty="No endpoints available"
          >
            {#snippet head()}
              <tr>
                <th>Name</th>
                <th>State</th>
                <th>Context</th>
                <th>Codecs</th>
              </tr>
            {/snippet}
            {#snippet rows()}
              {#each endpoints as ep}
                <tr>
                  <td class="font-medium">{ep.object_name}</td>
                  <td>
                    <span class="badge badge-sm {getBadgeClass(ep.device_state)}">{ep.device_state || 'Unknown'}</span>
                  </td>
                  <td>{ep.context || '-'}</td>
                  <td>{ep.allow || '-'}</td>
                </tr>
              {/each}
            {/snippet}
          </DataTable>

          <!-- Trunks / outbound registrations -->
          <DataTable
            id="trunks"
            title={'Trunks & registrations'}
            count={trunks.length}
            colspan={6}
            empty="No registered trunks or external providers"
          >
            {#snippet head()}
              <tr>
                <th>Trunk name</th>
                <th>Type</th>
                <th>Status</th>
                <th>Server address</th>
                <th>Details</th>
                <th class="text-right">RTT</th>
              </tr>
            {/snippet}
            {#snippet rows()}
              {#each trunks as t}
                <tr>
                  <td class="font-medium">{t.name}</td>
                  <td>{t.type || '-'}</td>
                  <td><span class="badge badge-sm {getBadgeClass(t.status)}">{t.status || '-'}</span></td>
                  <td>{t.address || '-'}</td>
                  <td>{t.details || '-'}</td>
                  <td class="text-right tabular-nums">{t.roundtrip || '-'}</td>
                </tr>
              {/each}
            {/snippet}
          </DataTable>

          <!-- Call history -->
          <DataTable
            id="history"
            title="Call history"
            count={history.length}
            colspan={7}
            empty="No recorded call history yet"
          >
            {#snippet head()}
              <tr>
                <th>Start time</th>
                <th>Caller ID</th>
                <th>Destination</th>
                <th>Disposition</th>
                <th>Total duration</th>
                <th>Billable</th>
                <th>Channel</th>
              </tr>
            {/snippet}
            {#snippet rows()}
              {#each history as h}
                <tr>
                  <td>{h.start_time || '-'}</td>
                  <td class="font-medium">{h.caller || '-'}{#if h.caller_name} ({h.caller_name}){/if}</td>
                  <td>{h.destination || '-'}</td>
                  <td>
                    <span class="badge badge-sm {getDispositionBadgeClass(h.disposition)}">{h.disposition || 'UNKNOWN'}</span>
                  </td>
                  <td>{h.duration || '00:00'}</td>
                  <td>{h.billable || '00:00'}</td>
                  <td class="text-base-content/70 text-xs">{h.channel || '-'}</td>
                </tr>
              {/each}
            {/snippet}
          </DataTable>
        </div>
      </main>

      <footer class="text-base-content/70 border-base-300 border-t px-4 py-3 text-xs sm:px-6">
        Asterisk Dashboard &bull; connected via real-time WebSocket
      </footer>
    </div>

    <!-- ================= Sidebar ================= -->
    <Sidebar {activeId} />
  </div>
{/if}
