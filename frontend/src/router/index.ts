import { createRouter, createWebHashHistory } from 'vue-router'
import LocalProxyView from '../views/LocalProxyView.vue'
import ProxyServerView from '../views/ProxyServerView.vue'
import ClientNodeView from '../views/ClientNodeView.vue'
import PromotionServer from '../views/promotion/PromotionServer.vue'

// 桌面端（Wails）使用 hash 模式，避免内置资源服务器无法回退到 index.html
const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/local-proxies' },
    { path: '/local-proxies', name: 'local-proxies', component: LocalProxyView },
    { path: '/proxy-servers', name: 'proxy-servers', component: ProxyServerView },
    { path: '/client-nodes', name: 'client-nodes', component: ClientNodeView },
    { path: '/promotion', name: 'promotion', component: PromotionServer },
  ],
})

export default router
