import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter/wght.css'
import './index.css'
import { GraphApp } from './graph/GraphApp'

const root = document.getElementById('root')
if (root) {
  createRoot(root).render(
    <StrictMode>
      <GraphApp />
    </StrictMode>,
  )
}
