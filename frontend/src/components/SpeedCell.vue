<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import type { TrafficStats } from '../../bindings/changeme'

const props = defineProps<{
  fetcher: () => Promise<TrafficStats>
  interval?: number
}>()

const rxRate = ref(0)
const txRate = ref(0)
const failed = ref(false)
let timer: number | undefined
let busy = false
let disposed = false

function formatRate(value: number) {
  if (!value) {
    return '0 B/s'
  }
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  let rate = value
  let unit = 0
  while (rate >= 1024 && unit < units.length - 1) {
    rate /= 1024
    unit++
  }
  return `${rate >= 100 ? rate.toFixed(0) : rate.toFixed(1)} ${units[unit]}`
}

async function refresh() {
  if (busy || disposed) {
    return
  }
  busy = true
  try {
    const stats = await props.fetcher()
    if (!disposed) {
      rxRate.value = stats.rxRate
      txRate.value = stats.txRate
      failed.value = false
    }
  } catch {
    if (!disposed) {
      failed.value = true
    }
  } finally {
    busy = false
  }
}

onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, props.interval ?? 5000)
})

onBeforeUnmount(() => {
  disposed = true
  if (timer) {
    window.clearInterval(timer)
  }
})
</script>

<template>
  <div class="speed-cell" title="每秒下行 / 上行">
    <span class="speed-line down">
      <i class="fa-solid fa-arrow-down"></i>
      {{ failed ? '—' : formatRate(rxRate) }}
    </span>
    <span class="speed-line up">
      <i class="fa-solid fa-arrow-up"></i>
      {{ failed ? '—' : formatRate(txRate) }}
    </span>
  </div>
</template>

<style scoped>
.speed-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
  font-size: 11px;
  line-height: 1.3;
}

.speed-line {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.speed-line i {
  font-size: 9px;
}

.speed-line.down {
  color: var(--accent);
}

.speed-line.up {
  color: var(--success);
}
</style>
