import { createApp } from 'vue'
import PrimeVue from 'primevue/config'
import ToastService from 'primevue/toastservice'
import Aura from '@primeuix/themes/aura'
import 'primeicons/primeicons.css'
import '@fortawesome/fontawesome-free/css/all.css'
import App from './App.vue'
import router from './router'
import { pinia } from './stores'
import { useThemeStore } from './stores/theme'

const app = createApp(App)

app
  .use(pinia)
  .use(router)
  .use(PrimeVue, {
    theme: {
      preset: Aura,
      options: {
        darkModeSelector: '.app-dark',
      },
    },
  })
  .use(ToastService)

// 启动前应用已保存的主题，避免首屏闪烁。
useThemeStore(pinia).apply()
app.mount('#app')
