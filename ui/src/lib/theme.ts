export const STORAGE_KEY = 'asterisk-dashboard:theme'

/**
 * The daisyUI themes registered in style.css.
 * Add a new theme to the `themes:` list there and to this array.
 */
export const THEMES = ['light', 'dark'] as const

export type Theme = (typeof THEMES)[number]

export const DARK: Theme = 'dark'
export const LIGHT: Theme = 'light'

export function getPreferredTheme(): Theme {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved && (THEMES as readonly string[]).includes(saved)) return saved as Theme
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? DARK : LIGHT
}

export function applyTheme(theme: string): void {
  if (!(THEMES as readonly string[]).includes(theme)) return
  document.documentElement.setAttribute('data-theme', theme)
  localStorage.setItem(STORAGE_KEY, theme)
  document.dispatchEvent(new CustomEvent('themechange', { detail: { theme } }))
}

export function toggleTheme(): void {
  applyTheme(document.documentElement.dataset.theme === DARK ? LIGHT : DARK)
}

/**
 * Applies the initial theme and flips it when a `[data-theme-toggle]` element
 * is clicked. The icon swap is pure CSS (see style.css), so there is no DOM
 * sync in JS.
 */
export function initTheme(): void {
  document.addEventListener('click', (event) => {
    const target = event.target as Element | null
    if (target?.closest('[data-theme-toggle]')) toggleTheme()
  })

  applyTheme(getPreferredTheme())
}
