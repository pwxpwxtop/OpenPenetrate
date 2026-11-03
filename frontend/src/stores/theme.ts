import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { themePresets } from '../themes'
import type { ThemePreset } from '../themes'

const STORAGE_KEY = 'intranet-penetration-theme'
const DEFAULT_THEME_ID = themePresets[0]?.id ?? 'dark-0'

function readSavedTheme() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved && themePresets.some((theme) => theme.id === saved)) {
      return saved
    }
  } catch {
    // localStorage 不可用时使用默认主题
  }
  return DEFAULT_THEME_ID
}

export const useThemeStore = defineStore('theme', () => {
  const currentId = ref(readSavedTheme())
  const current = computed<ThemePreset>(
    () => themePresets.find((theme) => theme.id === currentId.value) ?? themePresets[0],
  )

  function apply() {
    const theme = current.value
    if (!theme) {
      return
    }
    const root = document.documentElement
    root.style.setProperty('--app-background', theme.background)
    root.style.setProperty('--accent', theme.accent)
    root.style.setProperty('--accent-2', theme.accent2)
    root.style.setProperty('--accent-soft', theme.accentSoft)
    root.style.setProperty('--accent-border', theme.accentBorder)
    root.style.setProperty('--text', theme.text)
    root.style.setProperty('--muted', theme.muted)
    root.style.setProperty('--border', theme.border)
    root.style.setProperty('--panel', theme.panel)
    root.style.setProperty('--panel-strong', theme.panelStrong)
    root.style.setProperty('--sidebar-bg', theme.sidebarBg)
    root.style.setProperty('--success', theme.mode === 'dark' ? '#38d39f' : '#12805c')
    root.style.setProperty('--danger', theme.mode === 'dark' ? '#ff5c72' : '#cf2f45')
    root.style.colorScheme = theme.mode
    root.classList.toggle('app-dark', theme.mode === 'dark')
  }

  function setTheme(id: string) {
    if (!themePresets.some((theme) => theme.id === id)) {
      return
    }
    currentId.value = id
    apply()
    try {
      localStorage.setItem(STORAGE_KEY, id)
    } catch {
      // 忽略持久化失败
    }
  }

  return {
    presets: themePresets,
    currentId,
    current,
    apply,
    setTheme,
  }
})
