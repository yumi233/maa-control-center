import {createApp} from 'vue'
import App from './App.vue'
import SklandBinding from './components/SklandBinding.vue'
import './styles/endfield/endfield.css'
import './styles/app.css'
import './styles/assistant.css'

try {document.documentElement.dataset.theme = localStorage.getItem('maacc-theme') || 'light'} catch { /* storage is optional */ }
const isSkland = /^\/skland\/?$/.test(location.pathname) || location.hash.startsWith('#/skland')
createApp(isSkland ? SklandBinding : App).mount('#app')
