<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Browser, Events } from '@wailsio/runtime'
import Button from 'primevue/button'
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
import type { ClientNode, LocalProxy, ProxyServer, TrafficStats } from '../../bindings/changeme'
import TrafficDialog from '../components/TrafficDialog.vue'
import SpeedCell from '../components/SpeedCell.vue'
import { useLanguageStore } from '../stores/language'

type TagSeverity = 'success' | 'secondary' | 'info' | 'warn' | 'danger' | 'contrast'

interface NodeForm {
  id: string
  name: string
  host: string
  sshPort: number | null
  username: string
  authType: string
  password: string
  privateKey: string
  os: string
  installed: boolean
  configured: boolean
  status: string
  message: string
}

const lang = useLanguageStore()

const authOptions = computed(() => [
  { label: lang.toOr('clientNode.authPassword', '密码'), value: 'password' },
  { label: lang.toOr('clientNode.authKey', '私钥'), value: 'key' },
  { label: lang.toOr('clientNode.authLocal', '本机'), value: 'local' },
])

const toast = useToast()
const nodes = ref<ClientNode[]>([])
const localProxies = ref<LocalProxy[]>([])
const servers = ref<ProxyServer[]>([])
const loading = ref(false)
const detecting = ref(false)
const connectionStates = ref<Record<string, { ok: boolean; message: string }>>({})
const testingForm = ref(false)
const testResult = ref('')
const testOk = ref(false)
const trafficVisible = ref(false)
const trafficTitle = ref('')
const trafficFetcher = ref<() => Promise<TrafficStats>>(() =>
  Promise.resolve({ rxRate: 0, txRate: 0, rxTotal: 0, txTotal: 0 }),
)

const dialogVisible = ref(false)
const editing = ref(false)
const saving = ref(false)
const form = reactive<NodeForm>(emptyForm())

const busyId = ref('')
const busyAction = ref('')
const deployFailed = ref(false)

const logVisible = ref(false)
const logTitle = ref('')
const logMode = ref<'deploy' | 'runtime'>('deploy')
const logNodeId = ref('')
const logs = ref<string[]>([])
const logRef = ref<HTMLElement | null>(null)
let stopLogListener: (() => void) | null = null

onMounted(async () => {
  stopLogListener = Events.On('node:deploy:log', (event) => {
    if (logVisible.value && logMode.value === 'deploy' && event.data.nodeId === logNodeId.value) {
      logs.value.push(event.data.line)
    }
  })
  await Promise.all([loadNodes(), loadLocalProxies(), loadServers()])
  await autoDetect()
})

onBeforeUnmount(() => {
  stopLogListener?.()
  stopLogListener = null
})

watch(
  () => logs.value.length,
  async () => {
    await nextTick()
    if (logRef.value) {
      logRef.value.scrollTop = logRef.value.scrollHeight
    }
  },
)

function emptyForm(): NodeForm {
  return {
    id: '',
    name: '',
    host: '',
    sshPort: 22,
    username: 'root',
    authType: 'password',
    password: '',
    privateKey: '',
    os: '',
    installed: false,
    configured: false,
    status: '',
    message: '',
  }
}

function toPayload(): ClientNode {
  return {
    id: form.id,
    name: form.name.trim(),
    host: form.host.trim(),
    sshPort: form.sshPort ?? 22,
    username: form.username.trim(),
    authType: form.authType,
    password: form.password,
    privateKey: form.privateKey,
    os: form.os,
    installed: form.installed,
    configured: form.configured,
    status: form.status,
    message: form.message,
  }
}

async function loadNodes() {
  loading.value = true
  try {
    nodes.value = (await ProxyService.ListNodes()) ?? []
  } catch (err) {
    notifyError(lang.toOr('clientNode.loadFailed', '加载失败'), err)
  } finally {
    loading.value = false
  }
}

async function loadLocalProxies() {
  try {
    localProxies.value = (await ProxyService.ListLocalProxies()) ?? []
  } catch {
    localProxies.value = []
  }
}

async function loadServers() {
  try {
    servers.value = (await ProxyService.ListServers()) ?? []
  } catch {
    servers.value = []
  }
}

function mountedProxies(nodeId: string) {
  return localProxies.value.filter((proxy) => proxy.nodeId === nodeId)
}

function visibleProxies(nodeId: string) {
  return mountedProxies(nodeId).slice(0, 3)
}

function extraProxyCount(nodeId: string) {
  return Math.max(0, mountedProxies(nodeId).length - 3)
}

function remoteAddress(proxy: LocalProxy) {
  const host = servers.value.find((server) => server.id === proxy.serverId)?.host ?? ''
  if (!host) {
    return `:${proxy.remotePort}`
  }
  const formatted = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
  return `${formatted}:${proxy.remotePort}`
}

const mountDetailVisible = ref(false)
const mountDetailNode = ref<ClientNode | null>(null)

function openMountDetail(node: ClientNode) {
  mountDetailNode.value = node
  mountDetailVisible.value = true
}

async function openProxyRemote(proxy: LocalProxy) {
  const host = servers.value.find((server) => server.id === proxy.serverId)?.host ?? ''
  if (!host) {
    toast.add({
      severity: 'warn',
      summary: lang.toOr('clientNode.cannotOpen', '无法跳转'),
      detail: lang.toOr('clientNode.proxyNotLinked', '该代理未关联代理服务器'),
      life: 3000,
    })
    return
  }
  const formatted = host.includes(':') && !host.startsWith('[') ? `[${host}]` : host
  const url = `http://${formatted}:${proxy.remotePort}`
  try {
    await Browser.OpenURL(url)
  } catch {
    window.open(url, '_blank', 'noopener')
  }
}

async function autoDetect() {
  if (!nodes.value.length) {
    return
  }
  detecting.value = true
  try {
    const targets = [...nodes.value]
    const results = await Promise.allSettled(targets.map((node) => ProxyService.DetectNodeFrpc(node)))
    results.forEach((result, index) => {
      const node = targets[index]
      if (result.status === 'fulfilled') {
        applyNode(result.value)
        connectionStates.value[node.id] = { ok: true, message: lang.toOr('clientNode.sshConnectionOk', 'SSH 连接正常') }
      } else {
        connectionStates.value[node.id] = { ok: false, message: errorText(result.reason) }
      }
    })
  } finally {
    detecting.value = false
  }
}

function applyNode(node: ClientNode) {
  const index = nodes.value.findIndex((item) => item.id === node.id)
  if (index !== -1) {
    nodes.value[index] = node
  }
}

function openCreate() {
  editing.value = false
  testResult.value = ''
  Object.assign(form, emptyForm())
  dialogVisible.value = true
}

function openEdit(row: ClientNode) {
  editing.value = true
  testResult.value = ''
  Object.assign(form, {
    id: row.id,
    name: row.name,
    host: row.host,
    sshPort: row.sshPort,
    username: row.username,
    authType: row.authType,
    password: row.password,
    privateKey: row.privateKey,
    os: row.os,
    installed: row.installed,
    configured: row.configured,
    status: row.status,
    message: row.message,
  })
  dialogVisible.value = true
  // 打开编辑弹窗时自动检测一次连接状态。
  nextTick(() => {
    testForm()
  })
}

async function save() {
  if (!form.name.trim()) {
    notifyWarn(lang.toOr('clientNode.nameRequired', '请输入名称'))
    return
  }
  if (!form.host.trim()) {
    notifyWarn(lang.toOr('clientNode.hostRequired', '请输入节点地址'))
    return
  }
  if (!form.username.trim()) {
    notifyWarn(lang.toOr('clientNode.usernameRequired', '请输入 SSH 用户名'))
    return
  }
  if (!form.sshPort || form.sshPort < 1 || form.sshPort > 65535) {
    notifyWarn(lang.toOr('clientNode.sshPortRange', 'SSH 端口需在 1-65535 之间'))
    return
  }
  if (form.authType === 'password' && !form.password) {
    notifyWarn(lang.toOr('clientNode.passwordRequired', '请输入 SSH 密码'))
    return
  }
  if (form.authType === 'key' && !form.privateKey.trim()) {
    notifyWarn(lang.toOr('clientNode.privateKeyRequired', '请粘贴 SSH 私钥'))
    return
  }

  saving.value = true
  try {
    const saved = await ProxyService.SaveNode(toPayload())
    const index = nodes.value.findIndex((item) => item.id === saved.id)
    if (index === -1) {
      nodes.value.push(saved)
    } else {
      nodes.value[index] = saved
    }
    // 保存后自动检测一次连接状态。
    ProxyService.TestNode(saved)
      .then((message) => {
        connectionStates.value[saved.id] = { ok: true, message }
      })
      .catch((err) => {
        connectionStates.value[saved.id] = { ok: false, message: errorText(err) }
      })
    toast.add({
      severity: 'success',
      summary: lang.toOr('clientNode.saveSuccess', '保存成功'),
      detail: editing.value
        ? lang.toOr('clientNode.nodeUpdated', '客户端节点已更新')
        : lang.toOr('clientNode.nodeAdded', '客户端节点已添加'),
      life: 2600,
    })
    dialogVisible.value = false
  } catch (err) {
    notifyError(lang.toOr('clientNode.saveFailed', '保存失败'), err)
  } finally {
    saving.value = false
  }
}

async function testNode(row: ClientNode) {
  busyId.value = row.id
  busyAction.value = 'test'
  try {
    const result = await ProxyService.TestNode(row)
    connectionStates.value[row.id] = { ok: true, message: result }
    toast.add({ severity: 'success', summary: lang.toOr('clientNode.connectSuccess', '连接成功'), detail: result, life: 5000 })
  } catch (err) {
    connectionStates.value[row.id] = { ok: false, message: errorText(err) }
    toast.add({ severity: 'error', summary: lang.toOr('clientNode.connectFailed', '连接失败'), detail: errorText(err), life: 6000 })
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function testForm() {
  testingForm.value = true
  testResult.value = ''
  try {
    testResult.value = await ProxyService.TestNode(toPayload())
    testOk.value = true
    if (form.id) {
      connectionStates.value[form.id] = { ok: true, message: testResult.value }
    }
  } catch (err) {
    testResult.value = errorText(err)
    testOk.value = false
    if (form.id) {
      connectionStates.value[form.id] = { ok: false, message: testResult.value }
    }
  } finally {
    testingForm.value = false
  }
}

function openTraffic(node: ClientNode) {
  trafficTitle.value = lang.toOr('clientNode.speedTestTitle', '节点测速') + ' · ' + node.name
  trafficFetcher.value = () => ProxyService.GetNodeTraffic(node.id)
  trafficVisible.value = true
}

function nodeSpeedFetcher(id: string) {
  return () => ProxyService.GetNodeTraffic(id)
}

async function deployNode(row: ClientNode) {
  logNodeId.value = row.id
  logTitle.value = lang.toOr('clientNode.deployFrpcTitle', '部署 frpc') + ' · ' + row.name
  logMode.value = 'deploy'
  logVisible.value = true
  logs.value = []
  deployFailed.value = false
  busyId.value = row.id
  busyAction.value = 'deploy'
  try {
    const updated = await ProxyService.DeployNodeFrpc(row.id)
    applyNode(updated)
    toast.add({
      severity: 'success',
      summary: lang.toOr('clientNode.deploySuccess', '部署完成'),
      detail: updated.configured
        ? `${updated.name} 上的 frpc 已启动`
        : lang.toOr('clientNode.frpcInstalledNotLinked', 'frpc 已安装，请先在「配置本地代理」中关联此节点'),
      life: 4000,
    })
  } catch (err) {
    deployFailed.value = true
    logs.value.push(lang.toOr('clientNode.deployFailed', '部署失败') + ': ' + errorText(err))
    toast.add({ severity: 'error', summary: lang.toOr('clientNode.deployFailed', '部署失败'), detail: errorText(err), life: 6000 })
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function startNode(row: ClientNode) {
  busyId.value = row.id
  busyAction.value = 'start'
  try {
    const updated = await ProxyService.StartNodeFrpc(row.id)
    applyNode(updated)
    toast.add({ severity: 'success', summary: lang.toOr('clientNode.frpcStarted', 'frpc 已启动'), detail: row.name, life: 2600 })
  } catch (err) {
    notifyError(lang.toOr('clientNode.startFailed', '启动失败'), err)
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function stopNode(row: ClientNode) {
  busyId.value = row.id
  busyAction.value = 'stop'
  try {
    const updated = await ProxyService.StopNodeFrpc(row.id)
    applyNode(updated)
    toast.add({ severity: 'success', summary: lang.toOr('clientNode.frpcStopped', 'frpc 已停止'), detail: row.name, life: 2600 })
  } catch (err) {
    notifyError(lang.toOr('clientNode.stopFailed', '停止失败'), err)
  } finally {
    busyId.value = ''
    busyAction.value = ''
  }
}

async function showLogs(row: ClientNode) {
  logNodeId.value = row.id
  logTitle.value = lang.toOr('clientNode.runtimeLogs', '运行日志') + ' · ' + row.name
  logMode.value = 'runtime'
  logVisible.value = true
  logs.value = [lang.toOr('clientNode.readingLogs', '正在读取 journalctl ...')]
  try {
    const output = await ProxyService.GetNodeRuntimeLogs(row.id)
    logs.value = output ? output.split('\n') : [lang.toOr('clientNode.noLogs', '暂无日志')]
  } catch (err) {
    logs.value = [lang.toOr('clientNode.readLogFailed', '读取日志失败') + ': ' + errorText(err)]
  }
}

async function removeNode(row: ClientNode) {
  if (!window.confirm(lang.toOr('clientNode.confirmDeletePrefix', '确定删除「') + row.name + lang.toOr('clientNode.confirmDeleteSuffix', '」吗？'))) {
    return
  }
  try {
    await ProxyService.DeleteNode(row.id)
    nodes.value = nodes.value.filter((item) => item.id !== row.id)
    await loadLocalProxies()
    toast.add({
      severity: 'success',
      summary: lang.toOr('clientNode.deleted', '已删除'),
      detail: lang.toOr('clientNode.nodeRemovedPrefix', '「') + row.name + lang.toOr('clientNode.nodeRemovedSuffix', '」已移除'),
      life: 2400,
    })
  } catch (err) {
    notifyError(lang.toOr('clientNode.deleteFailed', '删除失败'), err)
  }
}

function statusLabel(node: ClientNode) {
  if (node.status === 'active') {
    return lang.toOr('clientNode.statusRunning', '运行中')
  }
  if (node.installed && !node.configured) {
    return lang.toOr('clientNode.statusPendingConfig', '待配置')
  }
  return node.installed ? lang.toOr('clientNode.statusInstalled', '已安装') : lang.toOr('clientNode.statusNotInstalled', '未安装')
}

function statusSeverity(node: ClientNode): TagSeverity {
  if (node.status === 'active') {
    return 'success'
  }
  if (node.installed && !node.configured) {
    return 'warn'
  }
  return node.installed ? 'warn' : 'secondary'
}

function connState(node: ClientNode) {
  return connectionStates.value[node.id]
}

function osName(os: string) {
  if (os === 'windows') {
    return 'Windows'
  }
  if (os === 'darwin') {
    return 'macOS'
  }
  if (os === 'linux') {
    return 'Linux'
  }
  return lang.toOr('clientNode.osUnknown', '系统未知')
}

function connLabel(node: ClientNode) {
  const state = connectionStates.value[node.id]
  if (!state) {
    return lang.toOr('clientNode.notDetected', '未检测')
  }
  return state.ok ? lang.toOr('clientNode.connectable', '可连接') : lang.toOr('clientNode.notConnectable', '不可连接')
}

function connClass(node: ClientNode) {
  const state = connectionStates.value[node.id]
  if (!state) {
    return ''
  }
  return state.ok ? 'ok' : 'failed'
}

function isBusy(node: ClientNode, action: string) {
  return busyId.value === node.id && busyAction.value === action
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

function notifyWarn(detail: string) {
  toast.add({ severity: 'warn', summary: lang.toOr('clientNode.checkInput', '请检查输入'), detail, life: 2600 })
}

function notifyError(summary: string, err: unknown) {
  toast.add({ severity: 'error', summary, detail: errorText(err), life: 5000 })
}
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h1>{{ lang.toOr('clientNode.pageTitle', '客户端节点') }}</h1>
        <p>{{ lang.toOr('clientNode.pageDescription', '通过 SSH 在 Linux 服务器安装 frpc 后台服务，由节点持续运行，桌面端只负责控制。') }}</p>
      </div>
      <div class="head-actions">
        <span v-if="detecting" class="detect-hint">
          <i class="fa-solid fa-rotate fa-spin"></i>
          {{ lang.toOr('clientNode.detectingNodes', '正在检测节点') }}
        </span>
        <Button @click="openCreate">
          <i class="fa-solid fa-plus"></i>
          <span class="btn-label">{{ lang.toOr('clientNode.addNode', '添加节点') }}</span>
        </Button>
      </div>
    </header>

    <div class="panel">
      <DataTable :value="nodes" :loading="loading" dataKey="id" tableStyle="width: 100%; table-layout: fixed">
        <Column :header="lang.toOr('clientNode.name', '名称')" style="width: 10%">
          <template #body="{ data }">
            <div class="node-name">
              <span class="node-icon"><i class="fa-solid fa-server"></i></span>
              <span>{{ data.name }}</span>
            </div>
          </template>
        </Column>
        <Column :header="lang.toOr('clientNode.address', '地址')" style="width: 18%">
          <template #body="{ data }">
            <div class="node-cell">
              <span class="mono node-address">{{ data.host }}:{{ data.sshPort }}</span>
              <span class="conn-status" :class="connClass(data)" :title="connState(data)?.message || lang.toOr('clientNode.notYetDetected', '尚未检测')">
                {{ osName(data.os) }} · {{ connLabel(data) }}
              </span>
            </div>
          </template>
        </Column>
        <Column :header="lang.toOr('clientNode.login', '登录')" style="width: 10%">
          <template #body="{ data }">
            <span>{{ data.username }}</span>
            <span class="auth-tag">{{ data.authType === 'key' ? lang.toOr('clientNode.authKey', '私钥') : lang.toOr('clientNode.authPassword', '密码') }}</span>
          </template>
        </Column>
        <Column :header="lang.toOr('clientNode.frpcStatus', 'frpc 状态')" style="width: 8%">
          <template #body="{ data }">
            <Tag :value="statusLabel(data)" :severity="statusSeverity(data)" :title="data.message || lang.toOr('clientNode.notYetDetected', '尚未检测')" />
          </template>
        </Column>
        <Column :header="lang.toOr('clientNode.mountedPorts', '挂载端口')" style="width: 19%">
          <template #body="{ data }">
            <div v-if="mountedProxies(data.id).length" class="mounted-ports">
              <button
                v-for="proxy in visibleProxies(data.id)"
                :key="proxy.id"
                type="button"
                class="mounted-port"
                :title="`${proxy.name} → ${lang.toOr('clientNode.local', '本地')} ${proxy.host}:${proxy.port}`"
                @click="openProxyRemote(proxy)"
              >
                {{ remoteAddress(proxy) }}
              </button>
              <button
                v-if="extraProxyCount(data.id) > 0"
                type="button"
                class="mounted-more"
                :title="lang.toOr('clientNode.viewAllPortsPrefix', '查看全部 ') + mountedProxies(data.id).length + lang.toOr('clientNode.viewAllPortsSuffix', ' 个挂载端口')"
                @click="openMountDetail(data)"
              >
                +{{ extraProxyCount(data.id) }}
              </button>
            </div>
            <span v-else class="muted">—</span>
          </template>
        </Column>
        <Column :header="lang.toOr('clientNode.speed', '网速')" style="width: 9%">
          <template #body="{ data }">
            <SpeedCell :fetcher="nodeSpeedFetcher(data.id)" />
          </template>
        </Column>
        <Column :header="lang.toOr('clientNode.actions', '操作')" style="width: 26%">
          <template #body="{ data }">
            <div class="row-actions">
              <Button text rounded severity="secondary" :title="lang.toOr('clientNode.testSshTitle', '测试 SSH 连接')" :loading="isBusy(data, 'test')" @click="testNode(data)">
                <i v-if="!isBusy(data, 'test')" class="fa-solid fa-plug"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('clientNode.deployUpdateFrpcTitle', '部署 / 更新 frpc')" :loading="isBusy(data, 'deploy')" @click="deployNode(data)">
                <i v-if="!isBusy(data, 'deploy')" class="fa-solid fa-download"></i>
              </Button>
              <Button
                v-if="data.configured && data.status !== 'active'"
                text
                rounded
                severity="success"
                :title="lang.toOr('clientNode.startFrpcTitle', '启动 frpc')"
                :loading="isBusy(data, 'start')"
                @click="startNode(data)"
              >
                <i v-if="!isBusy(data, 'start')" class="fa-solid fa-play"></i>
              </Button>
              <Button
                v-else-if="data.configured"
                text
                rounded
                severity="danger"
                :title="lang.toOr('clientNode.stopFrpcTitle', '停止 frpc')"
                :loading="isBusy(data, 'stop')"
                @click="stopNode(data)"
              >
                <i v-if="!isBusy(data, 'stop')" class="fa-solid fa-stop"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('clientNode.viewLogsTitle', '查看运行日志')" @click="showLogs(data)">
                <i class="fa-solid fa-file-lines"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('clientNode.speedTitle', '测速（每秒上行/下行）')" @click="openTraffic(data)">
                <i class="fa-solid fa-gauge-high"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('clientNode.edit', '编辑')" @click="openEdit(data)">
                <i class="fa-solid fa-pen"></i>
              </Button>
              <Button text rounded severity="danger" :title="lang.toOr('clientNode.delete', '删除')" @click="removeNode(data)">
                <i class="fa-solid fa-trash-can"></i>
              </Button>
            </div>
          </template>
        </Column>

        <template #empty>
          <div class="empty-state">
            <i class="fa-solid fa-server"></i>
            <strong>{{ lang.toOr('clientNode.emptyTitle', '还没有客户端节点') }}</strong>
            <span>{{ lang.toOr('clientNode.emptyDescription', '添加一台 Linux 服务器，桌面端会自动安装并托管 frpc') }}</span>
          </div>
        </template>
      </DataTable>
    </div>

    <Dialog
      v-model:visible="dialogVisible"
      modal
      :draggable="false"
      :header="editing ? lang.toOr('clientNode.editNodeTitle', '编辑客户端节点') : lang.toOr('clientNode.addNodeTitle', '添加客户端节点')"
      :style="{ width: '580px' }"
    >
      <div class="form-grid">
        <div class="field">
          <label>{{ lang.toOr('clientNode.name', '名称') }}</label>
          <InputText v-model="form.name" fluid :placeholder="lang.toOr('clientNode.namePlaceholder', '例如：内网应用服务器')" maxlength="40" />
        </div>

        <div class="form-row">
          <div class="field">
            <label>{{ lang.toOr('clientNode.hostLabel', '节点地址 / IP') }}</label>
            <InputText v-model="form.host" fluid :placeholder="lang.toOr('clientNode.hostPlaceholder', '192.168.1.20 或 node.local')" />
          </div>
          <div class="field">
            <label>{{ lang.toOr('clientNode.sshPortLabel', 'SSH 端口') }}</label>
            <InputNumber v-model="form.sshPort" fluid :min="1" :max="65535" :use-grouping="false" />
          </div>
        </div>

        <div class="form-row">
          <div class="field">
            <label>{{ lang.toOr('clientNode.sshUsernameLabel', 'SSH 用户名') }}</label>
            <InputText v-model="form.username" fluid placeholder="root" />
          </div>
          <div class="field">
            <label>{{ lang.toOr('clientNode.authTypeLabel', '认证方式') }}</label>
            <Select v-model="form.authType" fluid :options="authOptions" option-label="label" option-value="value" />
          </div>
        </div>

        <div v-if="form.authType === 'password'" class="field">
          <label>{{ lang.toOr('clientNode.sshPasswordLabel', 'SSH 密码') }}</label>
          <Password v-model="form.password" fluid :feedback="false" toggle-mask :placeholder="lang.toOr('clientNode.passwordPlaceholder', '输入服务器登录密码')" />
        </div>
        <div v-else class="field">
          <label>{{ lang.toOr('clientNode.sshPrivateKeyLabel', 'SSH 私钥') }}</label>
          <Textarea v-model="form.privateKey" fluid rows="5" class="key-area" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
          <span class="hint">{{ lang.toOr('clientNode.privateKeyHint', '支持 OpenSSH 私钥（不支持带 passphrase 的私钥）') }}</span>
        </div>

        <span class="hint">
          {{ lang.toOr('clientNode.nodeRoleHint', '节点负责运行 frpc；本地代理中的 IP 和端口是「从节点视角」访问内网服务的地址。') }}
        </span>

        <div v-if="testResult" class="test-result" :class="{ ok: testOk }">
          <i :class="testOk ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-xmark'"></i>
          <span>{{ testResult }}</span>
        </div>
      </div>

      <template #footer>
        <div class="dialog-footer">
          <Button text severity="secondary" :loading="testingForm" @click="testForm">
            <i v-if="!testingForm" class="fa-solid fa-plug"></i>
            <span class="btn-label">{{ lang.toOr('clientNode.testConnection', '测试连接') }}</span>
          </Button>
          <div class="footer-actions">
            <Button text severity="secondary" :label="lang.toOr('clientNode.cancel', '取消')" @click="dialogVisible = false" />
            <Button :loading="saving" @click="save">
              <i v-if="!saving" class="fa-solid fa-check"></i>
              <span class="btn-label">{{ lang.toOr('clientNode.save', '保存') }}</span>
            </Button>
          </div>
        </div>
      </template>
    </Dialog>

    <Dialog
      v-model:visible="logVisible"
      modal
      :draggable="false"
      :closable="!busyId"
      :close-on-escape="!busyId"
      :header="logTitle"
      :style="{ width: '720px' }"
    >
      <div class="log-status">
        <Tag v-if="busyAction === 'deploy'" severity="warn" :value="lang.toOr('clientNode.deploying', '部署中')" />
        <Tag v-else-if="logMode === 'runtime'" severity="info" :value="lang.toOr('clientNode.runtimeLogs', '运行日志')" />
        <Tag v-else-if="deployFailed" severity="danger" :value="lang.toOr('clientNode.deployFailed', '部署失败')" />
        <Tag v-else severity="success" :value="lang.toOr('clientNode.deploySuccess', '部署完成')" />
        <span class="muted">{{ lang.toOr('clientNode.remoteOutputHint', '远程命令输出会实时显示在这里') }}</span>
      </div>
      <pre ref="logRef" class="log-view">{{ logs.length ? logs.join('\n') : lang.toOr('clientNode.waitingOutput', '等待远程输出...') }}</pre>

      <template #footer>
        <Button text severity="secondary" :disabled="!!busyId" :label="lang.toOr('clientNode.close', '关闭')" @click="logVisible = false" />
      </template>
    </Dialog>

    <Dialog
      v-model:visible="mountDetailVisible"
      modal
      :draggable="false"
      :header="lang.toOr('clientNode.mountDetailTitle', '挂载详情') + ' · ' + (mountDetailNode?.name ?? '')"
      :style="{ width: '560px' }"
    >
      <div v-if="mountDetailNode" class="mount-detail-list">
        <button
          v-for="proxy in mountedProxies(mountDetailNode.id)"
          :key="proxy.id"
          type="button"
          class="mount-detail-row"
          :title="lang.toOr('clientNode.openInBrowser', '在浏览器打开') + ' ' + remoteAddress(proxy)"
          @click="openProxyRemote(proxy)"
        >
          <span class="mount-detail-name">{{ proxy.name }}</span>
          <span class="muted mono">{{ proxy.host }}:{{ proxy.port }}</span>
          <i class="fa-solid fa-arrow-right"></i>
          <span class="mono mount-detail-remote">{{ remoteAddress(proxy) }}</span>
          <i class="fa-solid fa-arrow-up-right-from-square"></i>
        </button>
        <p v-if="!mountedProxies(mountDetailNode.id).length" class="muted">{{ lang.toOr('clientNode.noMountedProxies', '暂无挂载的代理') }}</p>
      </div>
      <template #footer>
        <Button text severity="secondary" :label="lang.toOr('clientNode.close', '关闭')" @click="mountDetailVisible = false" />
      </template>
    </Dialog>

    <TrafficDialog v-model:visible="trafficVisible" :title="trafficTitle" :fetcher="trafficFetcher" />
  </section>
</template>

<style scoped>
.head-actions {
  display: flex;
  align-items: center;
  gap: 14px;
}

.detect-hint {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}

.node-name {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-weight: 500;
}

.node-name > span:last-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  flex: none;
  border-radius: 7px;
  font-size: 11px;
  color: var(--accent);
  background: rgba(79, 140, 255, 0.12);
  border: 1px solid rgba(79, 140, 255, 0.2);
}

.node-address {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.conn-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--muted);
}

.conn-status::before {
  content: "";
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  opacity: 0.75;
}

.conn-status.ok {
  color: var(--success);
}

.conn-status.failed {
  color: var(--danger);
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

.auth-tag {
  margin-left: 8px;
  padding: 1px 7px;
  border: 1px solid var(--border);
  border-radius: 999px;
  font-size: 11px;
  color: var(--muted);
}

.mounted-ports {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  min-width: 0;
}

.mounted-port,
.mounted-more {
  padding: 1px 7px;
  border: 1px solid var(--accent-border);
  border-radius: 999px;
  background: var(--accent-soft);
  color: var(--text);
  font-family: "SFMono-Regular", Consolas, Menlo, monospace;
  font-size: 10px;
  line-height: 1.5;
  white-space: nowrap;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease;
}

.mounted-port:hover,
.mounted-more:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.mounted-more {
  border-style: dashed;
  color: var(--muted);
}

.mount-detail-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 360px;
  overflow-y: auto;
}

.mount-detail-row {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 12px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel);
  color: var(--text);
  font-size: 11px;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.mount-detail-row:hover {
  border-color: var(--accent-border);
  background: var(--panel-strong);
}

.mount-detail-name {
  overflow: hidden;
  min-width: 80px;
  font-weight: 600;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mount-detail-remote {
  margin-left: auto;
  color: var(--accent);
}

.mount-detail-row i {
  flex: none;
  font-size: 9px;
  color: var(--muted);
}

.row-actions :deep(.p-button) {
  width: 30px;
  height: 30px;
  padding: 0;
}

.btn-label {
  margin-left: 8px;
}

.key-area {
  font-family: "SFMono-Regular", Consolas, Menlo, monospace;
  font-size: 12px;
}

.log-status {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  font-size: 12px;
}

.log-view {
  height: 360px;
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
</style>
