import {createApp} from 'vue'
import App from './App.vue'
import './styles/endfield/endfield.css'
import './styles/app.css'

try {document.documentElement.dataset.theme = localStorage.getItem('maacc-theme') || 'light'} catch { /* storage is optional */ }
createApp(App).mount('#app')
