<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import { useThemeStore } from '../stores/theme'
import type { ThemeMode } from '../themes'

const props = defineProps<{
  visible: boolean
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const themeStore = useThemeStore()
const mode = ref<ThemeMode>(themeStore.current.mode)

const filteredThemes = computed(() => themeStore.presets.filter((theme) => theme.mode === mode.value))
const darkCount = computed(() => themeStore.presets.filter((theme) => theme.mode === 'dark').length)
const lightCount = computed(() => themeStore.presets.filter((theme) => theme.mode === 'light').length)

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      mode.value = themeStore.current.mode
    }
  },
)

function selectTheme(id: string) {
  themeStore.setTheme(id)
}

function close() {
  emit('update:visible', false)
}
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :draggable="false"
    header="主题风格"
    :style="{ width: '780px' }"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="mode-switch">
      <button type="button" class="mode-button" :class="{ active: mode === 'dark' }" @click="mode = 'dark'">
        <i class="fa-solid fa-moon"></i>
        暗色 {{ darkCount }}
      </button>
      <button type="button" class="mode-button" :class="{ active: mode === 'light' }" @click="mode = 'light'">
        <i class="fa-solid fa-sun"></i>
        浅色 {{ lightCount }}
      </button>
    </div>

    <div class="theme-grid">
      <button
        v-for="preset in filteredThemes"
        :key="preset.id"
        type="button"
        class="theme-card"
        :class="{ active: themeStore.currentId === preset.id }"
        @click="selectTheme(preset.id)"
      >
        <span class="theme-preview" :style="{ background: preset.background }">
          <span class="preview-sidebar" :style="{ background: preset.sidebarBg, borderColor: preset.border }"></span>
          <span class="preview-panel" :style="{ background: preset.panel, borderColor: preset.border }"></span>
          <span class="preview-accent" :style="{ background: preset.accent }"></span>
          <span class="preview-accent2" :style="{ background: preset.accent2 }"></span>
        </span>
        <span class="theme-card-foot">
          <span class="theme-name">{{ preset.name }}</span>
          <i v-if="themeStore.currentId === preset.id" class="fa-solid fa-circle-check"></i>
        </span>
      </button>
    </div>

    <template #footer>
      <Button text severity="secondary" label="关闭" @click="close" />
    </template>
  </Dialog>
</template>

<style scoped>
.mode-switch {
  display: inline-flex;
  gap: 6px;
  padding: 4px;
  margin-bottom: 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel);
}

.mode-button {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 8px 16px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--muted);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.16s ease, color 0.16s ease;
}

.mode-button.active {
  background: var(--accent-soft);
  color: var(--text);
}

.theme-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  max-height: 430px;
  padding: 2px;
  overflow-y: auto;
}

.theme-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--panel);
  cursor: pointer;
  text-align: left;
  transition: border-color 0.16s ease, transform 0.16s ease;
}

.theme-card:hover {
  border-color: var(--accent-border);
  transform: translateY(-1px);
}

.theme-card.active {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent-soft);
}

.theme-preview {
  position: relative;
  display: block;
  height: 74px;
  overflow: hidden;
  border: 1px solid var(--border);
  border-radius: 8px;
}

.preview-sidebar {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  width: 26%;
  border-right: 1px solid;
}

.preview-panel {
  position: absolute;
  top: 16px;
  right: 10px;
  width: 52%;
  height: 30px;
  border: 1px solid;
  border-radius: 6px;
}

.preview-accent,
.preview-accent2 {
  position: absolute;
  width: 9px;
  height: 9px;
  border-radius: 50%;
}

.preview-accent {
  right: 15px;
  bottom: 12px;
}

.preview-accent2 {
  right: 30px;
  bottom: 12px;
}

.theme-card-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.theme-name {
  overflow: hidden;
  color: var(--text);
  font-size: 12px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.theme-card-foot i {
  flex: none;
  color: var(--accent);
  font-size: 12px;
}
</style>
