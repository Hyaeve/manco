import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import './styles.css'
import './shell.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
