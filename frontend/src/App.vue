<script setup lang="ts">
import { ref } from 'vue'
import Toast from 'primevue/toast'
import ThemeDialog from './components/ThemeDialog.vue'
import { useThemeStore } from './stores/theme'

const theme = useThemeStore()
const themeDialogVisible = ref(false)
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-icon">
          <i class="fa-solid fa-tower-broadcast"></i>
        </div>
        <div class="brand-text">
          <span class="brand-name">内网穿透</span>
          <span class="brand-sub">Intranet Tunnel</span>
        </div>
      </div>

      <div class="nav-label">功能菜单</div>
      <nav class="nav">
        <RouterLink to="/local-proxies" class="nav-item">
          <i class="fa-solid fa-hard-drive"></i>
          <span>配置本地代理</span>
        </RouterLink>
        <RouterLink to="/proxy-servers" class="nav-item">
          <i class="fa-solid fa-cloud"></i>
          <span>代理服务器</span>
        </RouterLink>
        <RouterLink to="/client-nodes" class="nav-item">
          <i class="fa-solid fa-server"></i>
          <span>服务器挂载</span>
        </RouterLink>
      </nav>

      <button type="button" class="theme-trigger" @click="themeDialogVisible = true">
        <i class="fa-solid fa-palette"></i>
        <span class="theme-trigger-text">
          <span class="theme-trigger-title">主题风格</span>
          <span class="theme-trigger-name">{{ theme.current.name }}</span>
        </span>
        <i class="fa-solid fa-chevron-right theme-trigger-arrow"></i>
      </button>

      <div class="sidebar-footer">
        <i class="fa-solid fa-shield-halved"></i>
        <div>
          <span class="footer-title">frp + SSH</span>
          <span class="footer-desc">自动部署公网服务端</span>
        </div>
      </div>
    </aside>

    <main class="main">
      <RouterView />
    </main>
  </div>

  <Toast position="top-right" />
  <ThemeDialog v-model:visible="themeDialogVisible" />
</template>

<style scoped>
.app-shell {
  display: grid;
  grid-template-columns: 236px minmax(0, 1fr);
  height: 100vh;
}

.sidebar {
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 22px 16px 18px;
  border-right: 1px solid var(--border);
  background: var(--sidebar-bg);
  backdrop-filter: blur(18px);
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 2px 6px 22px;
}

.brand-icon {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 12px;
  font-size: 17px;
  color: #fff;
  background: linear-gradient(135deg, var(--accent) 0%, var(--accent-2) 100%);
  box-shadow: 0 8px 22px var(--accent-soft);
}

.brand-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.brand-name {
  font-size: 16px;
  font-weight: 700;
  letter-spacing: 0.3px;
}

.brand-sub {
  font-size: 11px;
  color: var(--muted);
  letter-spacing: 0.6px;
  text-transform: uppercase;
}

.nav-label {
  padding: 0 10px 10px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 1.2px;
  color: var(--muted);
  opacity: 0.75;
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 11px 12px;
  border: 1px solid transparent;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 500;
  color: var(--muted);
  text-decoration: none;
  transition: background 0.18s ease, color 0.18s ease, border-color 0.18s ease;
}

.nav-item i {
  width: 18px;
  font-size: 15px;
  text-align: center;
}

.nav-item:hover {
  color: var(--text);
  background: var(--panel-strong);
}

.nav-item.router-link-active {
  color: var(--text);
  border-color: var(--accent-border);
  background: linear-gradient(135deg, var(--accent-soft), transparent);
}

.theme-trigger {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: auto;
  padding: 11px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel);
  color: var(--muted);
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.16s ease, color 0.16s ease, background 0.16s ease;
}

.theme-trigger:hover {
  border-color: var(--accent-border);
  background: var(--panel-strong);
  color: var(--text);
}

.theme-trigger > i:first-child {
  color: var(--accent);
  font-size: 14px;
}

.theme-trigger-text {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.theme-trigger-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--text);
}

.theme-trigger-name {
  overflow: hidden;
  font-size: 11px;
  color: var(--muted);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.theme-trigger-arrow {
  font-size: 10px;
  color: var(--muted);
}

.sidebar-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 10px;
  padding: 12px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel);
  color: var(--muted);
}

.sidebar-footer > i {
  font-size: 15px;
  color: var(--accent);
}

.sidebar-footer div {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.footer-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--text);
}

.footer-desc {
  font-size: 11px;
}

.main {
  min-width: 0;
  overflow-y: auto;
  padding: 28px 32px 40px;
}
</style>
