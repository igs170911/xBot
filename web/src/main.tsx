import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App'
import { initTheme } from './settings/theme'
import { registerServiceWorker, watchInstall } from './mobile/pwa'
import { installRovingKeys } from './ui/rovingKeys'
import { watchViewport } from './mobile/viewport'

initTheme()
installRovingKeys()
watchInstall()
registerServiceWorker()
watchViewport()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
