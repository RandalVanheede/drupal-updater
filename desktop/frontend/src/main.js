import '@fontsource-variable/atkinson-hyperlegible-next'
import '@fontsource-variable/space-grotesk'
import '@fontsource-variable/jetbrains-mono'
import { mount } from 'svelte'
import App from './App.svelte'

const app = mount(App, {
  target: document.getElementById('app'),
})

export default app
