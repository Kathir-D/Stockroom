import { mount } from 'svelte'
import './style.css'
import './lib/design/nocturne.css'
import './lib/design/theme.css'
import App from './App.svelte'

const app = mount(App, {
  target: document.getElementById('app')!
})

export default app
