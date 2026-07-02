import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter/wght.css'
import './index.css'
import { SearchApp } from './search/SearchApp'
import { Toaster } from './components/ui/sonner'

const root = document.getElementById('root')
if (root) {
  createRoot(root).render(
    <StrictMode>
      <SearchApp />
      <Toaster />
    </StrictMode>,
  )
}
