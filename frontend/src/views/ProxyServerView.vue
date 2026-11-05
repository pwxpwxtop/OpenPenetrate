<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
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
import type { ProxyServer, TrafficStats } from '../../bindings/changeme'
import TrafficDialog from '../components/TrafficDialog.vue'
import SpeedCell from '../components/SpeedCell.vue'
import { useProxyStore } from '../stores/proxy'
import { useLanguageStore } from '../stores/language'

type TagSeverity = 'success' | 'secondary' | 'info' | 'warn' | 'danger' | 'contrast'

interface ServerForm {
  id: string
  name: string
  host: string
  sshPort: number | null
  username: string
  authType: string
  password: string
  privateKey: string
  frpsPort: number | null
  token: string
  installed: boolean
  status: string
  portStatus: string
  portMessage: string
}

const lang = useLanguageStore()

const authOptions = computed(() => [
  { label: lang.toOr('proxyServer.authPassword', '密码'), value: 'password' },
  { label: lang.toOr('proxyServer.authKey', '私钥'), value: 'key' },
])

const store = useProxyStore()
const toast = useToast()

const dialogVisible = ref(false)
const editing = ref(false)
const saving = ref(false)
const testingForm = ref(false)
const testResult = ref('')
const testOk = ref(false)
const discoveringForm = ref(false)
const discoverResult = ref('')
const discoverOk = ref(false)
const form = reactive<ServerForm>(emptyForm())

const testingId = ref('')
const checkingPortId = ref('')
const detecting = ref(false)
const trafficVisible = ref(false)
const trafficTitle = ref('')
const trafficFetcher = ref<() => Promise<TrafficStats>>(() =>
  Promise.resolve({ rxRate: 0, txRate: 0, rxTotal: 0, txTotal: 0 }),
)
const logVisible = ref(false)
const installing = ref(false)
const installFailed = ref(false)
const installingName = ref('')
const installLogs = ref<string[]>([])
const logRef = ref<HTMLElement | null>(null)
let stopLogListener: (() => void) | null = null

onMounted(async () => {
  try {
    await store.loadServers()
  } catch (err) {
    notifyError(lang.toOr('proxyServer.loadFailed', '加载失败'), err)
    return
  }
  await autoDetectFrps()
})

// 页面打开后自动通过 SSH 检测各服务器上 frps 的安装与运行状态。
async function autoDetectFrps() {
  if (!store.servers.length) {
    return
  }
  detecting.value = true
  try {
    const results = await Promise.allSettled(store.servers.map((server) => ProxyService.DetectFrps(server)))
    for (const result of results) {
      if (result.status === 'fulfilled') {
        store.applyServer(result.value)
      }
    }
  } finally {
    detecting.value = false
  }
}

watch(
  () => installLogs.value.length,
  async () => {
    await nextTick()
    if (logRef.value) {
      logRef.value.scrollTop = logRef.value.scrollHeight
    }
  },
)

function emptyForm(): ServerForm {
  return {
    id: '',
    name: '',
    host: '',
    sshPort: 22,
    username: 'root',
    authType: 'password',
    password: '',
    privateKey: '',
    frpsPort: 7000,
    token: randomToken(),
    installed: false,
    status: '',
    portStatus: '',
    portMessage: '',
  }
}

function randomToken() {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

function toPayload(): ProxyServer {
  return {
    id: form.id,
    name: form.name.trim(),
    host: form.host.trim(),
    sshPort: form.sshPort ?? 22,
    username: form.username.trim(),
    authType: form.authType,
    password: form.password,
    privateKey: form.privateKey,
    frpsPort: form.frpsPort ?? 7000,
    token: form.token.trim(),
    installed: form.installed,
    status: form.status,
    portStatus: form.portStatus,
    portMessage: form.portMessage,
  }
}

function openCreate() {
  editing.value = false
  testResult.value = ''
  discoverResult.value = ''
  Object.assign(form, emptyForm())
  dialogVisible.value = true
}

function openEdit(row: ProxyServer) {
  editing.value = true
  testResult.value = ''
  discoverResult.value = ''
  Object.assign(form, {
    id: row.id,
    name: row.name,
    host: row.host,
    sshPort: row.sshPort,
    username: row.username,
    authType: row.authType,
    password: row.password,
    privateKey: row.privateKey,
    frpsPort: row.frpsPort,
    token: row.token,
    installed: row.installed,
    status: row.status,
    portStatus: row.portStatus,
    portMessage: row.portMessage,
  })
  dialogVisible.value = true
}

async function save() {
  if (!form.name.trim()) {
    notifyWarn(lang.toOr('proxyServer.inputName', '请输入名称'))
    return
  }
  if (!form.host.trim()) {
    notifyWarn(lang.toOr('proxyServer.inputHost', '请输入公网服务器地址'))
    return
  }
  if (!form.username.trim()) {
    notifyWarn(lang.toOr('proxyServer.inputSshUser', '请输入 SSH 用户名'))
    return
  }
  if (!form.sshPort || form.sshPort < 1 || form.sshPort > 65535) {
    notifyWarn(lang.toOr('proxyServer.sshPortRange', 'SSH 端口需在 1-65535 之间'))
    return
  }
  if (!form.frpsPort || form.frpsPort < 1 || form.frpsPort > 65535) {
    notifyWarn(lang.toOr('proxyServer.frpsPortRange', 'frps 端口需在 1-65535 之间'))
    return
  }
  if (form.authType === 'password' && !form.password) {
    notifyWarn(lang.toOr('proxyServer.inputSshPassword', '请输入 SSH 密码'))
    return
  }
  if (form.authType === 'key' && !form.privateKey.trim()) {
    notifyWarn(lang.toOr('proxyServer.pasteSshKey', '请粘贴 SSH 私钥'))
    return
  }

  saving.value = true
  try {
    await store.saveServer(toPayload())
    toast.add({
      severity: 'success',
      summary: lang.toOr('proxyServer.saveSuccess', '保存成功'),
      detail: editing.value
        ? lang.toOr('proxyServer.configUpdated', '代理服务器配置已更新')
        : lang.toOr('proxyServer.serverAdded', '代理服务器已添加'),
      life: 2600,
    })
    dialogVisible.value = false
  } catch (err) {
    notifyError(lang.toOr('proxyServer.saveFailed', '保存失败'), err)
  } finally {
    saving.value = false
  }
}

async function testForm() {
  testingForm.value = true
  testResult.value = ''
  try {
    testResult.value = await ProxyService.TestServer(toPayload())
    testOk.value = true
  } catch (err) {
    testResult.value = errorText(err)
    testOk.value = false
  } finally {
    testingForm.value = false
  }
}

// 配置发现：读取服务器上已有的 frps 配置，避免同一台机器被重复安装覆盖。
async function discoverForm() {
  discoveringForm.value = true
  discoverResult.value = ''
  try {
    const found = await ProxyService.DiscoverFrps(toPayload())
    if (found.installed || found.configPath) {
      if (found.bindPort > 0) {
        form.frpsPort = found.bindPort
      }
      if (found.token) {
        form.token = found.token
      }
      form.installed = true
      form.status = found.status || 'installed'
      discoverOk.value = true
      discoverResult.value = found.message
      toast.add({
        severity: 'success',
        summary: lang.toOr('proxyServer.foundExistingConfig', '发现已有 frps 配置'),
        detail: lang.toOr('proxyServer.autoFilled', '已自动填充监听端口与 Token，可直接保存复用'),
        life: 4000,
      })
    } else {
      discoverOk.value = false
      discoverResult.value = found.message || lang.toOr('proxyServer.noFrpsFound', '未发现已安装的 frps')
    }
  } catch (err) {
    discoverOk.value = false
    discoverResult.value = errorText(err)
  } finally {
    discoveringForm.value = false
  }
}

async function testServer(row: ProxyServer) {
  testingId.value = row.id
  try {
    const result = await ProxyService.TestServer(row)
    toast.add({ severity: 'success', summary: lang.toOr('proxyServer.connectSuccess', '连接成功'), detail: result, life: 5000 })
  } catch (err) {
    toast.add({ severity: 'error', summary: lang.toOr('proxyServer.connectFailed', '连接失败'), detail: errorText(err), life: 6000 })
  } finally {
    testingId.value = ''
  }
}

function openTraffic(row: ProxyServer) {
  trafficTitle.value = `${lang.toOr('proxyServer.trafficTitle', '流量监控')} · ${row.name}`
  trafficFetcher.value = () => ProxyService.GetServerTraffic(row)
  trafficVisible.value = true
}

function serverSpeedFetcher(server: ProxyServer) {
  return () => ProxyService.GetServerTraffic(server)
}

async function checkPort(row: ProxyServer) {
  checkingPortId.value = row.id
  try {
    const checked = await store.checkServerPort(row)
    if (checked.portStatus === 'ok') {
      toast.add({ severity: 'success', summary: lang.toOr('proxyServer.portReachable', '端口可访问'), detail: checked.portMessage, life: 3200 })
    } else {
      toast.add({ severity: 'error', summary: lang.toOr('proxyServer.portUnreachable', '端口不可访问'), detail: checked.portMessage, life: 5200 })
    }
  } catch (err) {
    notifyError(lang.toOr('proxyServer.portCheckFailed', '端口检测失败'), err)
  } finally {
    checkingPortId.value = ''
  }
}

function portStatusLabel(server: ProxyServer) {
  if (server.portStatus === 'ok') {
    return lang.toOr('proxyServer.statusAccessible', '可访问')
  }
  return server.portStatus === 'failed'
    ? lang.toOr('proxyServer.statusInaccessible', '不可访问')
    : lang.toOr('proxyServer.statusNotChecked', '未检测')
}

function portStatusClass(server: ProxyServer) {
  if (server.portStatus === 'ok') {
    return 'ok'
  }
  return server.portStatus === 'failed' ? 'failed' : ''
}

function formatHostPort(server: ProxyServer) {
  const host = server.host.includes(':') && !server.host.startsWith('[') ? `[${server.host}]` : server.host
  return `${host}:${server.frpsPort}`
}

async function openServer(row: ProxyServer) {
  const url = `http://${formatHostPort(row)}`
  try {
    await Browser.OpenURL(url)
  } catch {
    window.open(url, '_blank', 'noopener')
  }
}

async function copyAddress(row: ProxyServer) {
  const text = formatHostPort(row)
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
  toast.add({ severity: 'success', summary: lang.toOr('proxyServer.copied', '已复制'), detail: text, life: 2200 })
}

// 安装前发现已有配置；返回 true 表示已沿用现有配置并跳过安装。
async function adoptExistingFrps(row: ProxyServer): Promise<boolean> {
  try {
    const found = await ProxyService.DiscoverFrps(row)
    // 只有真实存在且正在运行的配置才跳过安装；仅残留二进制不算已安装。
    if (!found.configPath || found.status !== 'active') {
      return false
    }
    const detail = found.message || lang.toOr('proxyServer.foundFrpsOnServer', '发现服务器上已有 frps')
    const useExisting = window.confirm(
      `${detail}\n\n${lang.toOr('proxyServer.adoptConfirm', '是否沿用现有配置并跳过安装？')}\n${lang.toOr('proxyServer.adoptConfirmHint', '点击“取消”将继续重新安装，可能覆盖现有 frps 配置。')}`,
    )
    if (!useExisting) {
      return false
    }
    const saved = await store.saveServer({
      ...row,
      installed: true,
      status: found.status || 'installed',
      frpsPort: found.bindPort > 0 ? found.bindPort : row.frpsPort,
      token: found.token || row.token,
    })
    store.applyServer(saved)
    toast.add({
      severity: 'success',
      summary: lang.toOr('proxyServer.adoptedExisting', '已沿用现有配置'),
      detail: lang.toOr('proxyServer.skipReinstall', '跳过重复安装') + (found.configPath ? ' · ' + found.configPath : ''),
      life: 5000,
    })
    return true
  } catch {
    // 发现失败不阻塞正常安装流程
    return false
  }
}

async function installServer(row: ProxyServer) {
  if (await adoptExistingFrps(row)) {
    return
  }

  installLogs.value = []
  installFailed.value = false
  installingName.value = row.name
  logVisible.value = true
  installing.value = true

  stopLogListener = Events.On('frps:install:log', (event) => {
    installLogs.value.push(event.data)
  })

  try {
    const updated = await ProxyService.InstallFrps(row)
    store.applyServer(updated)
    toast.add({
      severity: 'success',
      summary: lang.toOr('proxyServer.installDone', '安装完成'),
      detail: `${updated.name}${lang.toOr('proxyServer.frpsStartedSuffix', ' 上的 frps 已启动')}`,
      life: 4000,
    })
  } catch (err) {
    installFailed.value = true
    installLogs.value.push(`${lang.toOr('proxyServer.installFailed', '安装失败')}: ${errorText(err)}`)
    toast.add({ severity: 'error', summary: lang.toOr('proxyServer.installFailed', '安装失败'), detail: errorText(err), life: 6000 })
  } finally {
    installing.value = false
    stopLogListener?.()
    stopLogListener = null
  }
}

async function removeServer(row: ProxyServer) {
  if (!window.confirm(`${lang.toOr('proxyServer.deleteConfirmPrefix', '确定删除「')}${row.name}${lang.toOr('proxyServer.deleteConfirmSuffix', '」吗？')}`)) {
    return
  }
  try {
    await store.deleteServer(row.id)
    toast.add({
      severity: 'success',
      summary: lang.toOr('proxyServer.deleted', '已删除'),
      detail: `${lang.toOr('proxyServer.removedPrefix', '「')}${row.name}${lang.toOr('proxyServer.removedSuffix', '」已移除')}`,
      life: 2400,
    })
  } catch (err) {
    notifyError(lang.toOr('proxyServer.deleteFailed', '删除失败'), err)
  }
}

function statusLabel(server: ProxyServer): string {
  if (server.status === 'active') {
    return lang.toOr('proxyServer.statusRunning', '运行中')
  }
  return server.installed ? lang.toOr('proxyServer.statusInstalled', '已安装') : lang.toOr('proxyServer.statusNotInstalled', '未安装')
}

function statusSeverity(server: ProxyServer): TagSeverity {
  if (server.status === 'active') {
    return 'success'
  }
  return server.installed ? 'warn' : 'secondary'
}

function errorText(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

function notifyWarn(detail: string) {
  toast.add({ severity: 'warn', summary: lang.toOr('proxyServer.checkInput', '请检查输入'), detail, life: 2600 })
}

function notifyError(summary: string, err: unknown) {
  toast.add({ severity: 'error', summary, detail: errorText(err), life: 5000 })
}
</script>

<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h1>{{ lang.toOr('proxyServer.pageTitle', '代理服务器') }}</h1>
        <p>{{ lang.toOr('proxyServer.pageDesc', '添加公网服务器并通过 SSH 连接，一键安装 frps 服务端。') }}</p>
      </div>
      <div class="head-actions">
        <span v-if="detecting" class="detect-hint">
          <i class="fa-solid fa-rotate fa-spin"></i>
          {{ lang.toOr('proxyServer.detectingFrps', '正在检测 frps') }}
        </span>
        <Button @click="openCreate">
          <i class="fa-solid fa-plus"></i>
          <span class="btn-label">{{ lang.toOr('proxyServer.addServer', '添加服务器') }}</span>
        </Button>
      </div>
    </header>

    <div class="panel">
      <DataTable
        :value="store.servers"
        :loading="store.serverLoading"
        dataKey="id"
        tableStyle="width: 100%; table-layout: fixed"
      >
        <Column :header="lang.toOr('proxyServer.colName', '名称')" style="width: 13%">
          <template #body="{ data }">
            <div class="server-name">
              <span class="server-icon"><i class="fa-solid fa-cloud"></i></span>
              <span>{{ data.name }}</span>
            </div>
          </template>
        </Column>
        <Column :header="lang.toOr('proxyServer.colServer', '服务器')" style="width: 22%">
          <template #body="{ data }">
            <div class="server-cell">
              <span class="mono">{{ data.host }}:{{ data.sshPort }}</span>
              <span class="server-meta">{{ data.username }} · {{ data.authType === 'key' ? lang.toOr('proxyServer.authKey', '私钥') : lang.toOr('proxyServer.authPassword', '密码') }}</span>
            </div>
          </template>
        </Column>
        <Column :header="lang.toOr('proxyServer.colFrpsPort', 'frps 端口')" style="width: 15%">
          <template #body="{ data }">
            <div class="port-cell">
              <div class="port-line">
                <button class="port-link" type="button" :title="lang.toOr('proxyServer.openInBrowser', '在浏览器打开')" @click="openServer(data)">
                  <span class="port-pill">{{ data.frpsPort }}</span>
                  <i class="fa-solid fa-arrow-up-right-from-square"></i>
                </button>
                <button class="copy-btn" type="button" :title="lang.toOr('proxyServer.copyAddress', '复制公网地址')" @click="copyAddress(data)">
                  <i class="fa-regular fa-copy"></i>
                </button>
              </div>
              <span class="port-status" :class="portStatusClass(data)" :title="data.portMessage || lang.toOr('proxyServer.notDetectedYet', '尚未检测')">
                {{ portStatusLabel(data) }}
              </span>
            </div>
          </template>
        </Column>
        <Column :header="lang.toOr('proxyServer.colStatus', '状态')" style="width: 10%">
          <template #body="{ data }">
            <Tag :value="statusLabel(data)" :severity="statusSeverity(data)" />
          </template>
        </Column>
        <Column :header="lang.toOr('proxyServer.colSpeed', '网速')" style="width: 12%">
          <template #body="{ data }">
            <SpeedCell :fetcher="serverSpeedFetcher(data)" />
          </template>
        </Column>
        <Column :header="lang.toOr('proxyServer.colActions', '操作')" style="width: 28%">
          <template #body="{ data }">
            <div class="row-actions">
              <Button text rounded severity="secondary" :title="lang.toOr('proxyServer.testSsh', '测试 SSH 连接')" :loading="testingId === data.id" @click="testServer(data)">
                <i v-if="testingId !== data.id" class="fa-solid fa-plug"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('proxyServer.checkFrpsPort', '检测 frps 端口')" :loading="checkingPortId === data.id" @click="checkPort(data)">
                <i v-if="checkingPortId !== data.id" class="fa-solid fa-network-wired"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('proxyServer.viewTraffic', '查看上行 / 下行')" @click="openTraffic(data)">
                <i class="fa-solid fa-chart-line"></i>
              </Button>
              <Button
                text
                rounded
                severity="secondary"
                :title="data.installed ? lang.toOr('proxyServer.reinstallFrps', '重新安装 frps') : lang.toOr('proxyServer.installFrps', '安装 frps')"
                @click="installServer(data)"
              >
                <i class="fa-solid fa-download"></i>
              </Button>
              <Button text rounded severity="secondary" :title="lang.toOr('proxyServer.edit', '编辑')" @click="openEdit(data)">
                <i class="fa-solid fa-pen"></i>
              </Button>
              <Button text rounded severity="danger" :title="lang.toOr('proxyServer.delete', '删除')" @click="removeServer(data)">
                <i class="fa-solid fa-trash-can"></i>
              </Button>
            </div>
          </template>
        </Column>

        <template #empty>
          <div class="empty-state">
            <i class="fa-solid fa-cloud"></i>
            <strong>{{ lang.toOr('proxyServer.emptyTitle', '还没有代理服务器') }}</strong>
            <span>{{ lang.toOr('proxyServer.emptyDesc', '添加一台公网 Linux 服务器，自动部署 frps') }}</span>
          </div>
        </template>
      </DataTable>
    </div>

    <Dialog
      v-model:visible="dialogVisible"
      modal
      :draggable="false"
      :header="editing ? lang.toOr('proxyServer.editTitle', '编辑代理服务器') : lang.toOr('proxyServer.addTitle', '添加代理服务器')"
      :style="{ width: '580px' }"
    >
      <div class="form-grid">
        <div class="field">
          <label>{{ lang.toOr('proxyServer.colName', '名称') }}</label>
          <InputText v-model="form.name" fluid :placeholder="lang.toOr('proxyServer.phName', '例如：香港中转')" maxlength="40" />
        </div>

        <div class="form-row">
          <div class="field">
            <label>{{ lang.toOr('proxyServer.labelPublicHost', '公网地址 / IP') }}</label>
            <InputText v-model="form.host" fluid :placeholder="lang.toOr('proxyServer.phHost', '203.0.113.10 或 example.com')" />
          </div>
          <div class="field">
            <label>{{ lang.toOr('proxyServer.labelSshPort', 'SSH 端口') }}</label>
            <InputNumber v-model="form.sshPort" fluid :min="1" :max="65535" :use-grouping="false" />
          </div>
        </div>

        <div class="form-row">
          <div class="field">
            <label>{{ lang.toOr('proxyServer.labelSshUser', 'SSH 用户名') }}</label>
            <InputText v-model="form.username" fluid placeholder="root" />
          </div>
          <div class="field">
            <label>{{ lang.toOr('proxyServer.labelAuthType', '认证方式') }}</label>
            <Select v-model="form.authType" fluid :options="authOptions" option-label="label" option-value="value" />
          </div>
        </div>

        <div v-if="form.authType === 'password'" class="field">
          <label>{{ lang.toOr('proxyServer.labelSshPassword', 'SSH 密码') }}</label>
          <Password v-model="form.password" fluid :feedback="false" toggle-mask :placeholder="lang.toOr('proxyServer.phPassword', '输入服务器登录密码')" />
        </div>
        <div v-else class="field">
          <label>{{ lang.toOr('proxyServer.labelSshKey', 'SSH 私钥') }}</label>
          <Textarea
            v-model="form.privateKey"
            fluid
            rows="5"
            class="key-area"
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          />
          <span class="hint">{{ lang.toOr('proxyServer.hintPrivateKey', '支持 OpenSSH 私钥（不支持带 passphrase 的私钥）') }}</span>
        </div>

        <div class="form-row">
          <div class="field">
            <label>{{ lang.toOr('proxyServer.labelFrpsPort', 'frps 监听端口') }}</label>
            <InputNumber v-model="form.frpsPort" fluid :min="1" :max="65535" :use-grouping="false" />
            <span class="hint">{{ lang.toOr('proxyServer.hintFirewall', '请确保云服务器安全组已放行该 TCP 端口') }}</span>
          </div>
          <div class="field">
            <label>{{ lang.toOr('proxyServer.labelToken', '认证 Token') }}</label>
            <div class="token-row">
              <InputText v-model="form.token" fluid class="mono" :placeholder="lang.toOr('proxyServer.phToken', 'frpc 连接凭证')" />
              <Button text rounded severity="secondary" :title="lang.toOr('proxyServer.randomToken', '随机生成')" @click="form.token = randomToken()">
                <i class="fa-solid fa-rotate"></i>
              </Button>
            </div>
            <span class="hint">{{ lang.toOr('proxyServer.hintToken', 'frpc 与 frps 需使用相同 Token') }}</span>
          </div>
        </div>

        <div v-if="testResult" class="test-result" :class="{ ok: testOk }">
          <i :class="testOk ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-xmark'"></i>
          <span>{{ testResult }}</span>
        </div>
        <div v-if="discoverResult" class="test-result" :class="{ ok: discoverOk }">
          <i :class="discoverOk ? 'fa-solid fa-magnifying-glass' : 'fa-solid fa-circle-info'"></i>
          <span>{{ discoverResult }}</span>
        </div>
      </div>

      <template #footer>
        <div class="dialog-footer">
          <div class="footer-actions">
            <Button text severity="secondary" :loading="discoveringForm" @click="discoverForm">
              <i v-if="!discoveringForm" class="fa-solid fa-magnifying-glass"></i>
              <span class="btn-label">{{ lang.toOr('proxyServer.discoverConfig', '配置发现') }}</span>
            </Button>
            <Button text severity="secondary" :loading="testingForm" @click="testForm">
              <i v-if="!testingForm" class="fa-solid fa-plug"></i>
              <span class="btn-label">{{ lang.toOr('proxyServer.testConnection', '测试连接') }}</span>
            </Button>
          </div>
          <div class="footer-actions">
            <Button text severity="secondary" :label="lang.toOr('proxyServer.cancel', '取消')" @click="dialogVisible = false" />
            <Button :loading="saving" @click="save">
              <i v-if="!saving" class="fa-solid fa-check"></i>
              <span class="btn-label">{{ lang.toOr('proxyServer.save', '保存') }}</span>
            </Button>
          </div>
        </div>
      </template>
    </Dialog>

    <Dialog
      v-model:visible="logVisible"
      modal
      :draggable="false"
      :closable="!installing"
      :close-on-escape="!installing"
      :header="lang.toOr('proxyServer.installFrps', '安装 frps') + (installingName ? ' · ' + installingName : '')"
      :style="{ width: '720px' }"
    >
      <div class="log-status">
        <Tag v-if="installing" severity="warn" :value="lang.toOr('proxyServer.tagInstalling', '安装中')" />
        <Tag v-else-if="installFailed" severity="danger" :value="lang.toOr('proxyServer.installFailed', '安装失败')" />
        <Tag v-else severity="success" :value="lang.toOr('proxyServer.installDone', '安装完成')" />
        <span class="muted">{{ lang.toOr('proxyServer.logHint', '远程命令输出会实时显示在这里') }}</span>
      </div>
      <pre ref="logRef" class="log-view">{{ installLogs.length ? installLogs.join('\n') : lang.toOr('proxyServer.waitingOutput', '等待远程输出...') }}</pre>

      <template #footer>
        <Button text severity="secondary" :disabled="installing" :label="lang.toOr('proxyServer.close', '关闭')" @click="logVisible = false" />
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

.server-name {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  font-weight: 500;
}

.server-name > span:last-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-icon {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: 7px;
  font-size: 11px;
  color: var(--accent);
  background: var(--accent-soft);
  border: 1px solid var(--accent-border);
}

.server-cell {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.server-cell .mono {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.server-meta {
  font-size: 11px;
  color: var(--muted);
}

.row-actions :deep(.p-button) {
  width: 30px;
  height: 30px;
  padding: 0;
}

.port-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 5px;
}

.port-line {
  display: flex;
  align-items: center;
  gap: 6px;
}

.port-link {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 0;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.port-link i {
  font-size: 10px;
  color: var(--muted);
  transition: color 0.15s ease;
}

.port-link:hover .port-pill {
  border-color: rgba(79, 140, 255, 0.55);
  color: #cfe0ff;
}

.port-link:hover i {
  color: var(--accent);
}

.copy-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 3px;
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 11px;
  cursor: pointer;
  transition: color 0.15s ease;
}

.copy-btn:hover {
  color: var(--accent);
}

.port-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--muted);
}

.port-status::before {
  content: "";
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  opacity: 0.75;
}

.port-status.ok {
  color: var(--success);
}

.port-status.failed {
  color: var(--danger);
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

.btn-label {
  margin-left: 8px;
}

.token-row {
  display: flex;
  align-items: center;
  gap: 4px;
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
