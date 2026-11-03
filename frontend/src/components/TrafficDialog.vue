<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import Dialog from 'primevue/dialog'
import Button from 'primevue/button'
import type { TrafficStats } from '../../bindings/changeme'

const props = defineProps<{
  visible: boolean
  title: string
  fetcher: () => Promise<TrafficStats>
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const stats = ref<TrafficStats | null>(null)
const error = ref('')
const loading = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null
let generation = 0

function formatBytes(bytes: number) {
  if (!bytes || bytes <= 0) {
    return '0 B'
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index++
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

function formatRate(bytes: number) {
  return `${formatBytes(bytes)}/s`
}

async function tick(id: number) {
  if (id !== generation || !props.visible) {
    return
  }
  loading.value = true
  try {
    stats.value = await props.fetcher()
    error.value = ''
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
    if (id === generation && props.visible) {
      timer = setTimeout(() => tick(id), 1000)
    }
  }
}

function start() {
  stop()
  const id = ++generation
  stats.value = null
  error.value = ''
  tick(id)
}

function stop() {
  generation++
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      start()
    } else {
      stop()
    }
  },
)

onBeforeUnmount(stop)
</script>

<template>
  <Dialog
    :visible="visible"
    modal
    :draggable="false"
    :header="title"
    :style="{ width: '460px' }"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="traffic-grid">
      <div class="traffic-card down">
        <span class="traffic-label">
          <i class="fa-solid fa-arrow-down"></i>
          下行 / 下载
        </span>
        <strong>{{ stats ? formatRate(stats.rxRate) : '--' }}</strong>
        <span class="traffic-total">累计 {{ stats ? formatBytes(stats.rxTotal) : '--' }}</span>
      </div>
      <div class="traffic-card up">
        <span class="traffic-label">
          <i class="fa-solid fa-arrow-up"></i>
          上行 / 上传
        </span>
        <strong>{{ stats ? formatRate(stats.txRate) : '--' }}</strong>
        <span class="traffic-total">累计 {{ stats ? formatBytes(stats.txTotal) : '--' }}</span>
      </div>
    </div>

    <div class="traffic-foot">
      <span v-if="loading" class="muted"><i class="fa-solid fa-rotate fa-spin"></i> 正在采样...</span>
      <span v-else-if="error" class="traffic-error" :title="error">{{ error }}</span>
      <span v-else class="muted">约 1 秒采样一次</span>
    </div>

    <template #footer>
      <Button text severity="secondary" label="关闭" @click="emit('update:visible', false)" />
    </template>
  </Dialog>
</template>

<style scoped>
.traffic-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}

.traffic-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: var(--panel);
}

.traffic-card.down strong {
  color: var(--accent);
}

.traffic-card.up strong {
  color: var(--success);
}

.traffic-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
}

.traffic-card strong {
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.3px;
}

.traffic-total {
  font-size: 11px;
  color: var(--muted);
}

.traffic-foot {
  margin-top: 14px;
  font-size: 12px;
  color: var(--muted);
}

.traffic-error {
  display: block;
  overflow: hidden;
  color: var(--danger);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
