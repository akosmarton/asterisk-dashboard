import { mount } from 'svelte'
import App from './App.svelte'

// Transitions are disabled via `.no-transitions` on <html> (see index.html) so
// the themed borders/backgrounds do not animate from their pre-CSS values (white
// borders, because color-scheme is already dark) and flash on load. Drop the
// gate after the first painted frame, once every color has settled.
requestAnimationFrame(() => {
  requestAnimationFrame(() => document.documentElement.classList.remove('no-transitions'))
})

mount(App, { target: document.getElementById('app')! })
