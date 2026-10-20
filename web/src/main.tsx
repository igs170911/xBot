import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'
import { initTheme } from './settings/theme'
import { registerServiceWorker, watchInstall } from './mobile/pwa'
import { installRovingKeys } from './ui/rovingKeys'
import { watchViewport } from './mobile/viewport'
import { launchPath } from './mobile/nav'

initTheme()
installRovingKeys()
watchInstall()
registerServiceWorker()
watchViewport()

// 手機從主畫面打開 App：先到對話（2026-10-20）
const landing = launchPath(window.location, typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 767px)').matches)
if (landing) window.history.replaceState(null, '', landing)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
