<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Browser, Events } from '@wailsio/runtime'
import Button from 'primevue/button'
import Checkbox from 'primevue/checkbox'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Dialog from 'primevue/dialog'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Password from 'primevue/password'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import { useToast } from 'primevue/usetoast'
import { ProxyService } from '../../bindings/changeme'
import type {
  ClientNode,
  FrpcStatus,
  LocalProxy,
  PortInfo,
  ProxyServer,
  RemoteTunnelConfig,
  TrafficStats,
} from '../../bindings/changeme'
import TrafficDialog from '../components/TrafficDialog.vue'
import SpeedCell from '../components/SpeedCell.vue'
import { useProxyStore } from '../stores/proxy'

type TagSeverity = 'success' | 'secondary' | 'info' | 'warn' | 'danger' | 'contrast'

interface LocalProxyForm {
  id: string
  name: string
  host: string
  port: number | null
  remotePort: number | null
  serverId: string
  nodeId: string
  status: string
  message: string
  remoteStatus: string
  remoteMessage: string
}

interface MountForm {
  name: string
  host: string
  sshPort: number | null
  username: string
  authType: string
  password: string
  privateKey: string
}

const authOptions = [
  { label: '密码', value: 'password' },
  { label: '私钥', value: 'key' },
]

const store = useProxyStore()
const toast = useToast()
const router = useRouter()
const clientNodes = ref<ClientNode[]>([])

const dialogVisible = ref(false)
const editing = ref(false)
const saving = ref(false)
const checkingId = ref('')
const checkingRemoteId = ref('')
const detecting = ref(false)
const frpcStatuses = ref<Record<string, FrpcStatus>>({})
const startingId = ref('')
const stoppingId = ref('')
const tunnelVisible = ref(false)
const tunnelProxyId = ref('')
const tunnelProxyName = ref('')
const tunnelLogs = ref<string[]>([])
const tunnelLogRef = ref<HTMLElement | null>(null)
let stopLogListener: (() => void) | null = null

const mountVisible = ref(false)
const mountProxy = ref<LocalProxy | null>(null)
const mountLocal = ref(false)
const mountForm = reactive<MountForm>({
  name: '',
  host: '',
  sshPort: 22,
  username: 'root',
  authType: 'password',
  password: '',
  privateKey: '',
})
const mountTesting = ref(false)
const mountTestResult = ref('')
const mountTestOk = ref(false)
const mounting = ref(false)
const mountFailed = ref(false)
const mountLogVisible = ref(false)
const mountLogs = ref<string[]>([])
const mountLogRef = ref<HTMLElement | null>(null)
const mountNodeName = ref('')
const mountNodeId = ref('')
let stopNodeLogListener: (() => void) | null = null

const trafficVisible = ref(false)
const trafficTitle = ref('')
const trafficFetcher = ref<() => Promise<TrafficStats>>(() =>
  Promise.resolve({ rxRate: 0, txRate: 0, rxTotal: 0, txTotal: 0 }),
)

interface ServerDiscoveryItem {
  server: ProxyServer
  ports: PortInfo[]
  configs: RemoteTunnelConfig[]
  error: string
}

const discoverLoading = ref(false)
const discoverResults = ref<ServerDiscoveryItem[]>([])
const form = reactive<LocalProxyForm>({
  id: '',
  name: '',
  host: '',
  port: 80,
  remotePort: 6000,
  serverId: '',
  nodeId: '',
  status: '',
  message: '',
  remoteStatus: '',
  remoteMessage: '',
})

const serverOptions = computed(() =>
  store.servers.map((server) => ({
    label: `${server.name}（${server.host}）`,
    value: server.id,
  })),
)

const nodeOptions = computed(() =>
  clientNodes.value.map((node) => ({
    label: `${node.name}（${node.host}）`,
    value: node.id,
  })),
)

interface TableProxyRow extends LocalProxy {
  remoteOnly?: boolean
}

const filterServerId = ref('')

const serverFilterOptions = computed(() => [
  { label: '全部服务器', value: '' },
  ...store.servers.map((server) => ({
    label: `${server.name}（${server.host}）`,
    value: server.id,
  })),
])

// 表格同时展示本机代理配置和服务器上缓存的远程穿透配置。
const tableRows = computed<TableProxyRow[]>(() => {
  const rows: TableProxyRow[] = store.localProxies.map((proxy) => ({ ...proxy }))
  const localIds = new Set(rows.map((row) => row.id))

  for (const item of discoverResults.value) {
    const listening = new Set(item.ports.map((entry) => entry.port))
    for (const config of item.configs) {
      if (localIds.has(config.id)) {
        continue
      }
      const online = listening.has(config.remotePort)
      rows.push({
        id: config.id,
        name: config.name,
        host: config.localHost,
        port: config.localPort,
        remotePort: config.remotePort,
        serverId: item.server.id,
        nodeId: config.nodeId,
        status: online ? 'ok' : 'failed',
        message: `服务器缓存的穿透配置${config.owner ? ' · ' + config.owner : ''}`,
        remoteStatus: online ? 'ok' : 'failed',
        remoteMessage: online ? '远程端口正在监听' : '远程端口未监听',
        remoteOnly: true,
      })
    }
  }

  if (!filterServerId.value) {
    return rows
  }
  return rows.filter((row) => row.serverId === filterServerId.value)
})

onMounted(async () => {
  stopLogListener = Events.On('frpc:log', (event) => {
    if (tunnelVisible.value && event.data.proxyId === tunnelProxyId.value) {
      tunnelLogs.value.push(event.data.line)
    }
  })
  stopNodeLogListener = Events.On('node:deploy:log', (event) => {
    if (mountLogVisible.value && event.data.nodeId === mountNodeId.value) {
      mountLogs.value.push(event.data.line)
    }
  })

  await Promise.allSettled([
    store.loadLocalProxies(),
    store.loadServers(),
    loadClientNodes(),
    loadFrpcStatuses(),
  ])
  await autoDetectStatuses()
  // 配置好代理服务器后，自动发现服务器上已代理的远程端口。
  if (store.servers.length) {
    await runDiscovery()
  }
})

onBeforeUnmount(() => {
  stopLogListener?.()
  stopLogListener = null
  stopNodeLogListener?.()
  stopNodeLogListener = null
})

watch(
  () => tunnelLogs.value.length,
  async () => {
    await nextTick()
    if (tunnelLogRef.value) {
      tunnelLogRef.value.scrollTop = tunnelLogRef.value.scrollHeight
    }
  },
)

watch(
  () => mountLogs.value.length,
  async () => {
    await nextTick()
    if (mountLogRef.value) {
      mountLogRef.value.scrollTop = mountLogRef.value.scrollHeight
    }
  },
)

async function loadFrpcStatuses() {
  try {
    const list = await ProxyService.ListFrpcStatuses()
    const map: Record<string, FrpcStatus> = {}
    for (const item of list ?? []) {
      map[item.proxyId] = item
    }
    frpcStatuses.value = map
  } catch {
    // 非 Wails 环境或应用启动初期，忽略即可
  }
}

async function loadClientNodes() {
  try {
    clientNodes.value = (await ProxyService.ListNodes()) ?? []
  } catch {
    clientNodes.value = []
  }
}

// 自动检测本地服务、远程端口和节点 frpc 状态。
async function autoDetectStatuses() {
  if (!store.localProxies.length) {
    return
  }
  detecting.value = true
  try {
    const targets = [...store.localProxies]
    const nodeIds = [...new Set(targets.map((row) => row.nodeId).filter(Boolean))]

    const nodeTask = Promise.allSettled(
      nodeIds.map(async (nodeId) => {
        const node = clientNodes.value.find((item) => item.id === nodeId)
        if (!node) {
          return
        }
        const updated = await ProxyService.DetectNodeFrpc(node)
        const index = clientNodes.value.findIndex((item) => item.id === updated.id)
        if (index !== -1) {
          clientNodes.value[index] = updated
        }
      }),
    )

    const rowTask = Promise.allSettled(
      targets.map(async (row) => {
        await store.checkLocalProxy(row)
        const latest = store.localProxies.find((item) => item.id === row.id)
        if (latest?.serverId) {
          await store.checkRemoteProxy(latest)
        }
      }),
    )

    await Promise.all([nodeTask, rowTask])
  } finally {
    detecting.value = false
  }
}

function openCreate() {
  editing.value = false
  Object.assign(form, {
    id: '',
    name: '',
    host: '',
    port: 80,
    remotePort: 6000,
    serverId: '',
    nodeId: '',
    status: '',
    message: '',
    remoteStatus: '',
    remoteMessage: '',
  })
  dialogVisible.value = true
}

function openEdit(row: LocalProxy) {
  editing.value = true
  Object.assign(form, {
    id: row.id,
    name: row.name,
    host: row.host,
    port: row.port,
    remotePort: row.remotePort,
    serverId: row.serverId,
    status: row.status,
    message: row.message,
    remoteStatus: row.remoteStatus,
    remoteMessage: row.remoteMessage,
  })
  dialogVisible.value = true
}

async function save() {
  if (!form.name.trim()) {
    notifyWarn('请输入名称')
    return
  }
  if (!form.host.trim()) {
    notifyWarn('请输入本地 IP 或域名')
    return
  }
  if (!form.port || form.port < 1 || form.port > 65535) {
    notifyWarn('端口需在 1-65535 之间')
    return
  }
  if (!form.remotePort || form.remotePort < 1 || form.remotePort > 65535) {
    notifyWarn('远程访问端口需在 1-65535 之间')
    return
  }

  saving.value = true
  try {
    await store.saveLocalProxy({
      id: form.id,
      name: form.name.trim(),
      host: form.host.trim(),
      port: form.port,
      remotePort: form.remotePort,
      serverId: form.serverId || '',
      nodeId: form.nodeId || '',
      status: form.status,
      message: form.message,
      remoteStatus: form.remoteStatus,
      remoteMessage: form.remoteMessage,
    })
    if (form.nodeId) {
      toast.add({
        severity: 'warn',
        summary: '保存成功，需重新部署',
        detail: '已关联客户端节点，请到「客户端节点」点击部署，新配置才会生效',
        life: 5000,
      })
    } else {
      toast.add({
        severity: 'success',
        summary: '保存成功',
        detail: editing.value ? '本地代理配置已更新' : '本地代理已添加',
        life: 2600,
      })
    }
    dialogVisible.value = false
  } catch (err) {
    notifyError('保存失败', err)
  } finally {
    saving.value = false
  }
}

async function remove(row: LocalProxy) {
  if (!window.confirm(`确定删除「${row.name}」吗？`)) {
    return
  }
  try {
    await store.deleteLocalProxy(row.id)
    toast.add({ severity: 'success', summary: '已删除', detail: `「${row.name}」已移除`, life: 2400 })
  } catch (err) {
    notifyError('删除失败', err)
  }
}

async function check(row: LocalProxy) {
  checkingId.value = row.id
  try {
    const checked = await store.checkLocalProxy(row)
    if (checked.status === 'ok') {
      toast.add({ severity: 'success', summary: '检测成功', detail: checked.message, life: 3200 })
    } else {
      toast.add({ severity: 'error', summary: '检测失败', detail: checked.message, life: 5200 })
    }
  } catch (err) {
    notifyError('检测失败', err)
  } finally {
    checkingId.value = ''
  }
}

async function checkRemote(row: LocalProxy) {
  if (!row.serverId) {
    notifyWarn('请先编辑该代理并选择关联的代理服务器')
    return
  }
  checkingRemoteId.value = row.id
  try {
    const checked = await store.checkRemoteProxy(row)
    if (checked.remoteStatus === 'ok') {
      toast.add({ severity: 'success', summary: '远程端口可连接', detail: checked.remoteMessage, life: 3200 })
    } else {
      toast.add({ severity: 'error', summary: '远程端口不可连接', detail: checked.remoteMessage, life: 5200 })
    }
  } catch (err) {
    notifyError('远程检测失败', err)
  } finally {
    checkingRemoteId.value = ''
  }
}

function statusLabel(row: LocalProxy) {
  if (row.status === 'ok') {
    return '正常'
  }
  return row.status === 'failed' ? '异常' : '未检测'
}

function statusSeverity(row: LocalProxy): TagSeverity {
  if (row.status === 'ok') {
    return 'success'
  }
  return row.status === 'failed' ? 'danger' : 'secondary'
}

function remoteStatusLabel(row: LocalProxy) {
  if (row.remoteStatus === 'ok') {
    return '可连接'
  }
  return row.remoteStatus === 'failed' ? '不可连接' : '未检测'
}

function remoteStatusSeverity(row: LocalProxy): TagSeverity {
  if (row.remoteStatus === 'ok') {
    return 'success'
  }
  return row.remoteStatus === 'failed' ? 'danger' : 'secondary'
}

function serverName(id: string) {
  if (!id) {
    return '未关联'
  }
  return store.servers.find((server) => server.id === id)?.name ?? '已删除'
}

function serverHost(id: string) {
  return store.servers.find((server) => server.id === id)?.host ?? ''
}

function isLocalHost(host: string) {
  const value = host.trim().toLowerCase()
  return value === '127.0.0.1' || value === 'localhost' || value === '::1'
}

function nodeName(id: string) {
  if (!id) {
    return ''
  }
  return clientNodes.value.find((node) => node.id === id)?.name ?? '已删除'
}

function nodeFor(row: LocalProxy) {
  if (!row.nodeId) {
    return undefined
  }
  return clientNodes.value.find((node) => node.id === row.nodeId)
}

function isNodeManaged(row: LocalProxy) {
  return !!row.nodeId
}

function tunnelLabel(row: LocalProxy) {
  if (isNodeManaged(row)) {
    return nodeFor(row)?.status === 'active' ? '节点运行中' : '节点托管'
  }
  return frpcLabel(row)
}

function tunnelSeverity(row: LocalProxy): TagSeverity {
  if (isNodeManaged(row)) {
    return nodeFor(row)?.status === 'active' ? 'success' : 'warn'
  }
  return frpcSeverity(row)
}

function onTunnelTag(row: LocalProxy) {
  if (isNodeManaged(row)) {
    router.push('/client-nodes')
    return
  }
  openTunnelLogs(row)
}

async function openRemote(row: LocalProxy) {
  const host = serverHost(row.serverId)
  if (!host) {
    notifyWarn('请先编辑该代理并选择关联的代理服务器')
    return
  }
  const formattedHost = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
  const url = `http://${formattedHost}:${row.remotePort}`
  try {
    await Browser.OpenURL(url)
  } catch {
    window.open(url, '_blank', 'noopener')
  }
}

async function copyRemotePort(row: LocalProxy) {
  const text = String(row.remotePort)
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
  }
  toast.add({ severity: 'success', summary: '已复制', detail: text, life: 2000 })
}

function openTraffic(row: LocalProxy) {
  trafficTitle.value = `流量监控 · ${row.name}`
  trafficFetcher.value = () => ProxyService.GetLocalProxyTraffic(row.id)
  trafficVisible.value = true
}

function proxySpeedFetcher(id: string) {
  return () => ProxyService.GetLocalProxyTraffic(id)
}

// 读取各代理服务器当前监听的端口和缓存配置，供表格展示。
async function runDiscovery() {
  discoverLoading.value = true
  try {
    const results = await Promise.allSettled(
      store.servers.map(async (server) => {
        const found = await ProxyService.DiscoverServerServices(server)
        return {
          server,
          ports: found.ports ?? [],
          configs: found.configs ?? [],
          error: '',
        }
      }),
    )
    discoverResults.value = results.map((result, index) => {
      if (result.status === 'fulfilled') {
        return result.value
      }
      return {
        server: store.servers[index],
        ports: [],
        configs: [],
        error: errorText(result.reason),
      }
    })
  } finally {
    discoverLoading.value = false
  }
}

function openMount(row: LocalProxy) {
  mountProxy.value = row
  mountLocal.value = isLocalHost(row.host)
  // 本机挂载时自动复用已有本机节点，避免重复创建后代理被分散到不同节点。
  const localNode = clientNodes.value.find((node) => node.authType === 'local')
  mountNodeId.value = mountLocal.value && localNode ? localNode.id : ''
  Object.assign(mountForm, {
    name: `${row.name} 节点`,
    host: row.host,
    sshPort: 22,
    username: 'root',
    authType: 'password',
    password: '',
    privateKey: '',
  })
  mountTestResult.value = ''
  mountVisible.value = true
}

function mountPayload(): ClientNode {
  const row = mountProxy.value
  const local = mountLocal.value
  return {
    id: '',
    name: mountForm.name.trim() || (row ? `${row.name} 节点` : '客户端节点'),
    host: local ? '127.0.0.1' : mountForm.host.trim(),
    sshPort: local ? 22 : (mountForm.sshPort ?? 22),
    username: local ? 'local' : mountForm.username.trim(),
    authType: local ? 'local' : mountForm.authType,
    password: local ? '' : mountForm.password,
    privateKey: local ? '' : mountForm.privateKey,
    os: '',
    installed: false,
    configured: false,
    status: '',
    message: '',
  }
}

async function testMount() {
  mountTesting.value = true
  mountTestResult.value = ''
  try {
    mountTestResult.value = await ProxyService.TestNode(mountPayload())
    mountTestOk.value = true
  } catch (err) {
    mountTestResult.value = errorText(err)
    mountTestOk.value = false
  } finally {
    mountTesting.value = false
  }
}

async function confirmMount() {
  const row = mountProxy.value
  if (!row) {
    return
  }
  const selected = mountNodeId.value
    ? clientNodes.value.find((node) => node.id === mountNodeId.value)
    : undefined
  if (mountNodeId.value && !selected) {
    notifyWarn('所选节点不存在，请重新选择')
    return
  }
  if (!selected && !mountLocal.value) {
    if (!mountForm.host.trim()) {
      notifyWarn('请输入远程机器地址')
      return
    }
    if (!mountForm.username.trim()) {
      notifyWarn('请输入 SSH 用户名')
      return
    }
    if (!mountForm.sshPort || mountForm.sshPort < 1 || mountForm.sshPort > 65535) {
      notifyWarn('SSH 端口需在 1-65535 之间')
      return
    }
    if (mountForm.authType === 'password' && !mountForm.password) {
      notifyWarn('请输入 SSH 密码')
      return
    }
    if (mountForm.authType === 'key' && !mountForm.privateKey.trim()) {
      notifyWarn('请粘贴 SSH 私钥')
      return
    }
  }

  mounting.value = true
  try {
    let target = selected
    if (!target) {
      const payload = mountPayload()
      // 复用连接信息相同的已有节点，避免重复创建。
      const existing = clientNodes.value.find(
        (node) =>
          node.host === payload.host &&
          node.sshPort === payload.sshPort &&
          node.username === payload.username,
      )
      target = await ProxyService.SaveNode({
        ...payload,
        id: existing?.id ?? '',
        os: existing?.os ?? '',
        installed: existing?.installed ?? false,
        configured: existing?.configured ?? false,
        status: existing?.status ?? '',
        message: existing?.message ?? '',
      })
      const index = clientNodes.value.findIndex((node) => node.id === target?.id)
      if (index === -1) {
        clientNodes.value.push(target)
      } else {
        clientNodes.value[index] = target
      }
    }

    // 将当前本地代理绑定到节点，与该节点上的其他代理一起部署。
    await store.saveLocalProxy({ ...row, nodeId: target.id })

    mountVisible.value = false
    mountNodeId.value = target.id
    mountNodeName.value = target.name
    mountLogs.value = []
    mountFailed.value = false
    mountLogVisible.value = true

    const deployed = await ProxyService.DeployNodeFrpc(target.id)
    const deployedIndex = clientNodes.value.findIndex((node) => node.id === deployed.id)
    if (deployedIndex !== -1) {
      clientNodes.value[deployedIndex] = deployed
    }

    toast.add({
      severity: 'success',
      summary: '挂载完成',
      detail: deployed.configured
        ? `${deployed.name} 上的 frpc 已启动`
        : 'frpc 已安装，等待关联本地代理',
      life: 4000,
    })
  } catch (err) {
    mountFailed.value = true
    mountLogs.value.push('挂载失败: ' + errorText(err))
    toast.add({ severity: 'error', summary: '挂载失败', detail: errorText(err), life: 6000 })
  } finally {
    mounting.value = false
  }
}

function isFrpcRunning(row: LocalProxy) {
  return frpcStatuses.value[row.id]?.running === true
}

function frpcLabel(row: LocalProxy) {
  return isFrpcRunning(row) ? '运行中' : '未运行'
}

function frpcSeverity(row: LocalProxy): TagSeverity {
  return isFrpcRunning(row) ? 'success' : 'secondary'
}

async function openTunnelLogs(row: LocalProxy) {
  tunnelProxyId.value = row.id
  tunnelProxyName.value = row.name
  tunnelVisible.value = true
  try {
    tunnelLogs.value = (await ProxyService.GetFrpcLogs(row.id)) ?? []
  } catch {
    tunnelLogs.value = []
  }
}

async function startTunnel(row: LocalProxy) {
  if (!row.serverId) {
    notifyWarn('请先编辑该代理并选择关联的代理服务器')
    return
  }
  tunnelProxyId.value = row.id
  tunnelProxyName.value = row.name
  tunnelLogs.value = []
  tunnelVisible.value = true
  startingId.value = row.id
  try {
    const status = await ProxyService.StartFrpc(row.id)
    frpcStatuses.value[row.id] = status
    toast.add({ severity: 'success', summary: '穿透已启动', detail: `${row.name} 的 frpc 已运行`, life: 3000 })
  } catch (err) {
    tunnelLogs.value.push('启动失败: ' + errorText(err))
    toast.add({ severity: 'error', summary: '启动失败', detail: errorText(err), life: 6000 })
  } finally {
    startingId.value = ''
  }
}

async function stopTunnel(row: LocalProxy) {
  stoppingId.value = row.id
  try {
    const status = await ProxyService.StopFrpc(row.id)
    frpcStatuses.value[row.id] = status
    toast.add({ severity: 'success', summary: '穿透已停止', detail: row.name, life: 2600 })
  } catch (err) {
    notifyError('停止失败', err)
  } finally {
    stoppingId.value = ''
  }
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

function notifyWarn(detail: string) {
  toast.add({ severity: 'warn', summary: '请检查输入', detail, life: 2600 })
}

function notifyError(summary: string, err: unknown) {
  toast.add({ severity: 'error', summary, detail: errorText(err), life: 5000 })
}
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h1>配置本地代理</h1>
        <p>记录需要暴露到公网的内网服务，填写设备地址与端口即可。</p>
      </div>
      <div class="head-actions">
        <Select
          v-model="filterServerId"
          class="server-filter"
          :options="serverFilterOptions"
          option-label="label"
          option-value="value"
        />
        <span v-if="detecting" class="detect-hint">
          <i class="fa-solid fa-rotate fa-spin"></i>
          正在检测状态
        </span>
        <Button text rounded severity="secondary" title="重新检测状态" :loading="detecting" @click="autoDetectStatuses">
          <i v-if="!detecting" class="fa-solid fa-rotate"></i>
        </Button>
        <Button @click="openCreate">
          <i class="fa-solid fa-plus"></i>
          <span class="btn-label">添加本地代理</span>
        </Button>
      </div>
    </header>

    <div class="panel">
      <DataTable
        :value="tableRows"
        :loading="store.localLoading"
        dataKey="id"
        tableStyle="width: 100%; table-layout: fixed"
      >
        <Column header="名称" style="width: 11%">
          <template #body="{ data }">
            <div class="proxy-name">
              <span class="proxy-dot" :class="{ remote: data.remoteOnly }"></span>
              <span class="proxy-name-text">{{ data.name }}</span>
              <span v-if="data.remoteOnly" class="remote-badge">远程</span>
            </div>
          </template>
        </Column>
        <Column header="本地服务" style="width: 20%">
          <template #body="{ data }">
            <span class="mono local-service">{{ data.host }}:{{ data.port }}</span>
          </template>
        </Column>
        <Column header="远程端口" style="width: 15%">
          <template #body="{ data }">
            <div class="remote-cell">
              <button
                class="remote-link"
                type="button"
                :title="data.serverId ? `在浏览器打开 http://${serverHost(data.serverId)}:${data.remotePort}` : '请先关联代理服务器'"
                @click="openRemote(data)"
              >
                <span class="port-pill remote">{{ data.remotePort }}</span>
                <i class="fa-solid fa-arrow-up-right-from-square"></i>
              </button>
              <span class="server-label">
                {{ serverName(data.serverId) }}<template v-if="data.nodeId"> · {{ nodeName(data.nodeId) }}</template>
              </span>
            </div>
          </template>
        </Column>
        <Column header="状态" style="width: 15%">
          <template #body="{ data }">
            <div class="status-stack">
              <template v-if="data.remoteOnly">
                <Tag
                  :value="data.remoteStatus === 'ok' ? '已监听' : '未监听'"
                  :severity="data.remoteStatus === 'ok' ? 'success' : 'warn'"
                  :title="data.remoteMessage"
                />
              </template>
              <template v-else>
                <Tag
                  :value="`本地 ${statusLabel(data)}`"
                  :severity="statusSeverity(data)"
                  :title="data.message || '尚未检测本地服务'"
                />
                <Tag
                  :value="`远程 ${remoteStatusLabel(data)}`"
                  :severity="remoteStatusSeverity(data)"
                  :title="data.remoteMessage || '尚未检测远程端口'"
                />
                <button class="tag-button" type="button" title="查看穿透状态" @click="onTunnelTag(data)">
                  <Tag :value="`穿透 ${tunnelLabel(data)}`" :severity="tunnelSeverity(data)" />
                </button>
              </template>
            </div>
          </template>
        </Column>
        <Column header="网速" style="width: 11%">
          <template #body="{ data }">
            <SpeedCell v-if="!data.remoteOnly" :fetcher="proxySpeedFetcher(data.id)" />
            <span v-else class="muted" title="服务器缓存的配置，仅本机代理可测速">—</span>
          </template>
        </Column>
        <Column header="操作" style="width: 28%">
          <template #body="{ data }">
            <div class="row-actions">
              <template v-if="data.remoteOnly">
                <Button text rounded severity="secondary" title="复制远程端口" @click="copyRemotePort(data)">
                  <i class="fa-regular fa-copy"></i>
                </Button>
              </template>
              <template v-else>
              <Button text rounded severity="secondary" title="挂载后台（SSH 安装 frpc 服务）" @click="openMount(data)">
                <i class="fa-solid fa-cloud-arrow-up"></i>
              </Button>
              <Button
                v-if="!isNodeManaged(data)"
                text
                rounded
                :severity="isFrpcRunning(data) ? 'danger' : 'success'"
                :title="isFrpcRunning(data) ? '停止穿透' : '启动穿透'"
                :loading="startingId === data.id || stoppingId === data.id"
                @click="isFrpcRunning(data) ? stopTunnel(data) : startTunnel(data)"
              >
                <i
                  v-if="startingId !== data.id && stoppingId !== data.id"
                  :class="isFrpcRunning(data) ? 'fa-solid fa-stop' : 'fa-solid fa-play'"
                ></i>
              </Button>
              <Button text rounded severity="secondary" title="检测本地服务" :loading="checkingId === data.id" @click="check(data)">
                <i v-if="checkingId !== data.id" class="fa-solid fa-stethoscope"></i>
              </Button>
              <Button text rounded severity="secondary" title="检测远程端口" :loading="checkingRemoteId === data.id" @click="checkRemote(data)">
                <i v-if="checkingRemoteId !== data.id" class="fa-solid fa-satellite-dish"></i>
              </Button>
              <Button text rounded severity="secondary" title="查看上行 / 下行" @click="openTraffic(data)">
                <i class="fa-solid fa-chart-line"></i>
              </Button>
              <Button text rounded severity="secondary" title="编辑" @click="openEdit(data)">
                <i class="fa-solid fa-pen"></i>
              </Button>
              <Button text rounded severity="danger" title="删除" @click="remove(data)">
                <i class="fa-solid fa-trash-can"></i>
              </Button>
              </template>
            </div>
          </template>
        </Column>

        <template #empty>
          <div class="empty-state">
            <i class="fa-solid fa-server"></i>
            <strong>还没有本地代理</strong>
            <span>点击右上角「添加本地代理」开始配置</span>
          </div>
        </template>
      </DataTable>
    </div>

    <Dialog
      v-model:visible="dialogVisible"
      modal
      :draggable="false"
      :header="editing ? '编辑本地代理' : '添加本地代理'"
      :style="{ width: '460px' }"
    >
      <div class="form-grid">
        <div class="field">
          <label>名称</label>
          <InputText v-model="form.name" fluid placeholder="例如：NAS 管理后台" maxlength="40" />
        </div>
        <div class="field">
          <label>本地 IP 或域名</label>
          <InputText v-model="form.host" fluid placeholder="192.168.1.10 或 nas.local" />
          <span class="hint">填写内网设备地址，支持 IPv4、IPv6 或域名</span>
        </div>
        <div class="field">
          <label>关联代理服务器</label>
          <Select
            v-model="form.serverId"
            fluid
            :options="serverOptions"
            option-label="label"
            option-value="value"
            placeholder="选择用于穿透的公网服务器"
            show-clear
          />
          <span class="hint">
            {{ serverOptions.length ? '远程检测会尝试连接该服务器的远程端口' : '还没有代理服务器，请先到「代理服务器」页面添加' }}
          </span>
        </div>
        <div v-show="false" class="field">
          <label>客户端节点（可选）</label>
          <Select
            v-model="form.nodeId"
            fluid
            :options="nodeOptions"
            option-label="label"
            option-value="value"
            placeholder="选择后台运行 frpc 的 Linux 节点"
            show-clear
          />
          <span class="hint">
            {{ nodeOptions.length ? '选择后由该节点后台运行 frpc，桌面端只负责控制' : '还没有客户端节点，请先到「客户端节点」页面添加' }}
          </span>
        </div>
        <div class="form-row">
          <div class="field">
            <label>本地端口</label>
            <InputNumber v-model="form.port" fluid :min="1" :max="65535" :use-grouping="false" placeholder="80" />
          </div>
          <div class="field">
            <label>远程访问端口</label>
            <InputNumber
              v-model="form.remotePort"
              fluid
              :min="1"
              :max="65535"
              :use-grouping="false"
              placeholder="6000"
            />
          </div>
        </div>
        <span class="hint">远程访问端口需在代理服务器的安全组/防火墙中放行</span>
      </div>

      <template #footer>
        <Button text severity="secondary" label="取消" @click="dialogVisible = false" />
        <Button :loading="saving" @click="save">
          <i v-if="!saving" class="fa-solid fa-check"></i>
          <span class="btn-label">保存</span>
        </Button>
      </template>
    </Dialog>

    <Dialog
      v-model:visible="tunnelVisible"
      modal
      :draggable="false"
      :closable="startingId === ''"
      :close-on-escape="startingId === ''"
      :header="`穿透日志 · ${tunnelProxyName}`"
      :style="{ width: '720px' }"
    >
      <div class="log-status">
        <Tag v-if="frpcStatuses[tunnelProxyId]?.running" severity="success" value="运行中" />
        <Tag v-else severity="secondary" value="未运行" />
        <span class="muted">frpc 输出日志会实时显示在这里</span>
      </div>
      <pre ref="tunnelLogRef" class="log-view">{{ tunnelLogs.length ? tunnelLogs.join('\n') : '等待 frpc 输出...' }}</pre>
      <template #footer>
        <Button text severity="secondary" :disabled="startingId !== ''" label="关闭" @click="tunnelVisible = false" />
      </template>
    </Dialog>

    <Dialog
      v-model:visible="mountVisible"
      modal
      :draggable="false"
      header="挂载后台服务"
      :style="{ width: '580px' }"
    >
      <div class="form-grid">
        <div class="field">
          <label>挂载到节点</label>
          <Select
            v-model="mountNodeId"
            fluid
            :options="clientNodes"
            option-label="name"
            option-value="id"
            placeholder="新建节点，或选择已有节点"
            show-clear
          />
          <span class="hint">选择已有节点时，新代理会与该节点上已有代理一起部署，不会覆盖其他端口</span>
        </div>
        <template v-if="!mountNodeId">
          <div class="field">
            <label>节点名称</label>
            <InputText v-model="mountForm.name" fluid placeholder="例如：内网服务器" maxlength="40" />
          </div>
          <div class="local-toggle">
          <Checkbox v-model="mountLocal" inputId="mountLocal" binary />
          <label for="mountLocal">本机挂载（无需 SSH，安装为 Windows 后台任务）</label>
        </div>
        <template v-if="!mountLocal">
          <div class="form-row">
            <div class="field">
              <label>远程机器地址</label>
              <InputText v-model="mountForm.host" fluid placeholder="192.168.1.20 或 server.local" />
            </div>
            <div class="field">
              <label>SSH 端口</label>
              <InputNumber v-model="mountForm.sshPort" fluid :min="1" :max="65535" :use-grouping="false" />
            </div>
          </div>
          <div class="form-row">
            <div class="field">
              <label>SSH 用户名</label>
              <InputText v-model="mountForm.username" fluid placeholder="root" />
            </div>
            <div class="field">
              <label>认证方式</label>
              <Select
                v-model="mountForm.authType"
                fluid
                :options="authOptions"
                option-label="label"
                option-value="value"
              />
            </div>
          </div>
          <div v-if="mountForm.authType === 'password'" class="field">
            <label>SSH 密码</label>
            <Password v-model="mountForm.password" fluid :feedback="false" toggle-mask placeholder="输入远程机器密码" />
          </div>
          <div v-else class="field">
            <label>SSH 私钥</label>
            <Textarea
              v-model="mountForm.privateKey"
              fluid
              rows="5"
              class="key-area"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
            />
            <span class="hint">支持 OpenSSH 私钥（不支持带 passphrase 的私钥）</span>
          </div>
        </template>
        <span v-if="mountLocal" class="hint">
          将在当前电脑安装 frpc 并创建开机计划任务，无需 SSH。本地服务地址请填写本机可访问的 IP 和端口。
        </span>
        <span v-else class="hint">
          支持 Windows / Linux / macOS。确认后自动安装 frpc 后台服务，并在「客户端节点」中显示。
          本地服务地址请填写「从远程机器视角」可访问的 IP 和端口。
        </span>
        </template>
        <div v-if="mountTestResult" class="test-result" :class="{ ok: mountTestOk }">
          <i :class="mountTestOk ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-xmark'"></i>
          <span>{{ mountTestResult }}</span>
        </div>
      </div>

      <template #footer>
        <div class="dialog-footer">
          <Button v-if="!mountNodeId" text severity="secondary" :loading="mountTesting" @click="testMount">
            <i v-if="!mountTesting" class="fa-solid fa-plug"></i>
            <span class="btn-label">测试连接</span>
          </Button>
          <span v-else class="muted"></span>
          <div class="footer-actions">
            <Button text severity="secondary" :disabled="mounting" label="取消" @click="mountVisible = false" />
            <Button :loading="mounting" @click="confirmMount">
              <i v-if="!mounting" class="fa-solid fa-cloud-arrow-up"></i>
              <span class="btn-label">确定并安装</span>
            </Button>
          </div>
        </div>
      </template>
    </Dialog>

    <Dialog
      v-model:visible="mountLogVisible"
      modal
      :draggable="false"
      :closable="!mounting"
      :close-on-escape="!mounting"
      :header="`挂载后台 · ${mountNodeName}`"
      :style="{ width: '720px' }"
    >
      <div class="log-status">
        <Tag v-if="mounting" severity="warn" value="安装中" />
        <Tag v-else-if="mountFailed" severity="danger" value="安装失败" />
        <Tag v-else severity="success" value="安装完成" />
        <span class="muted">远程安装输出会实时显示在这里</span>
      </div>
      <pre ref="mountLogRef" class="log-view">{{ mountLogs.length ? mountLogs.join('\n') : '等待远程输出...' }}</pre>
      <template #footer>
        <Button text severity="secondary" :disabled="mounting" label="关闭" @click="mountLogVisible = false" />
      </template>
    </Dialog>

    <TrafficDialog v-model:visible="trafficVisible" :title="trafficTitle" :fetcher="trafficFetcher" />
  </section>
</template>

<style scoped>
.head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.server-filter {
  width: 190px;
}

.detect-hint {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}

.proxy-name {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-weight: 500;
}

.proxy-name-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.remote-badge {
  flex: none;
  padding: 1px 6px;
  border: 1px solid var(--accent-border);
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 10px;
  font-weight: 600;
  line-height: 1.4;
}

.local-service {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.proxy-dot {
  width: 7px;
  height: 7px;
  flex: none;
  border-radius: 50%;
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
  box-shadow: 0 0 8px var(--accent-soft);
}

.proxy-dot.remote {
  background: var(--muted);
  box-shadow: none;
}

.port-pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--panel-strong);
  font-family: "SFMono-Regular", Consolas, Menlo, monospace;
  font-size: 12px;
  color: var(--muted);
}

.port-pill.remote {
  color: var(--accent);
  border-color: var(--accent-border);
  background: var(--accent-soft);
}

.remote-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  min-width: 0;
}

.row-actions :deep(.p-button) {
  width: 30px;
  height: 30px;
  padding: 0;
}

.remote-link {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.remote-link i {
  font-size: 10px;
  color: var(--muted);
  transition: color 0.15s ease;
}

.remote-link:hover i {
  color: var(--accent);
}

.remote-link:hover .port-pill {
  color: var(--text);
  border-color: var(--accent-border);
}

.server-label {
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  color: var(--muted);
}

.status-stack {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
}

.status-stack :deep(.p-tag) {
  padding: 2px 7px;
  font-size: 11px;
}

.tag-button {
  display: inline-flex;
  padding: 0;
  border: none;
  background: transparent;
  cursor: pointer;
}

.log-status {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  font-size: 12px;
}

.log-view {
  height: 340px;
  margin: 0;
  padding: 14px 16px;
  overflow: auto;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel-strong);
  color: var(--text);
  font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
  font-size: 12px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-all;
  -webkit-user-select: text;
  user-select: text;
}

.local-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text);
}

.local-toggle label {
  cursor: pointer;
}

.dialog-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}

.footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.key-area {
  font-family: "SFMono-Regular", Consolas, Menlo, monospace;
  font-size: 12px;
}

.test-result {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 9px 12px;
  border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
  border-radius: 9px;
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  color: var(--danger);
  font-size: 12px;
  line-height: 1.5;
  word-break: break-all;
}

.test-result.ok {
  border-color: color-mix(in srgb, var(--success) 35%, transparent);
  background: color-mix(in srgb, var(--success) 8%, transparent);
  color: var(--success);
}

.test-result i {
  margin-top: 1px;
}

.btn-label {
  margin-left: 8px;
}
</style>
