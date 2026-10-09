// daisyUI badge variants for device/status/contact values and for call
// dispositions. This is now the only copy of the matching rules (the Go
// template funcs that used to render the same strings are gone). The variant
// strings below are what Tailwind scans, so keep them literal.

/**
 * Badge variant for a device state, trunk status or contact status.
 */
export function getBadgeClass(val: string | undefined): string {
  const value = (val || '').toLowerCase().trim()

  // 1. Red states (offline / unavailable / unreachable)
  if (
    value.includes('unavail') ||
    value.includes('unreach') ||
    value.includes('unreg') ||
    value.includes('offline') ||
    value.includes('reject') ||
    value.includes('down')
  ) {
    return 'badge-error'
  }

  // 2. Green states (idle / not in use / reachable / registered / online / up)
  // MUST be evaluated before "in use", because "not IN USE" contains "in use"!
  if (
    value.includes('not in use') ||
    value.includes('idle') ||
    value.includes('registered') ||
    value.includes('reachable') ||
    value.includes('avail') ||
    value.includes('online') ||
    value.includes('up')
  ) {
    return 'badge-success'
  }

  // 3. Yellow states (active call / in use / busy / ringing)
  if (
    value.includes('in use') ||
    value.includes('busy') ||
    value.includes('ringing') ||
    value.includes('ring')
  ) {
    return 'badge-warning'
  }

  return 'badge-ghost'
}

/**
 * Badge variant for a call-history disposition.
 */
export function getDispositionBadgeClass(disp: string | undefined): string {
  const value = (disp || '').toUpperCase()
  if (value.includes('ANSWER')) return 'badge-success'
  if (value.includes('BUSY')) return 'badge-warning'
  if (value.includes('NO ANSWER') || value.includes('CANCEL')) return 'badge-ghost'
  if (value.includes('FAIL') || value.includes('REJECT') || value.includes('CONGESTION')) {
    return 'badge-error'
  }
  return 'badge-ghost'
}
