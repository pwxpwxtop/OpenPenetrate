import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ProxyService } from '../../bindings/changeme'
import type { LocalProxy, ProxyServer } from '../../bindings/changeme'

export const useProxyStore = defineStore('proxy', () => {
  const localProxies = ref<LocalProxy[]>([])
  const servers = ref<ProxyServer[]>([])
  const localLoading = ref(false)
  const serverLoading = ref(false)

  async function loadLocalProxies() {
    localLoading.value = true
    try {
      localProxies.value = (await ProxyService.ListLocalProxies()) ?? []
    } finally {
      localLoading.value = false
    }
  }

  async function saveLocalProxy(proxy: LocalProxy) {
    const saved = await ProxyService.SaveLocalProxy(proxy)
    const index = localProxies.value.findIndex((item) => item.id === saved.id)
    if (index === -1) {
      localProxies.value.push(saved)
    } else {
      localProxies.value[index] = saved
    }
    return saved
  }

  async function deleteLocalProxy(id: string) {
    await ProxyService.DeleteLocalProxy(id)
    localProxies.value = localProxies.value.filter((item) => item.id !== id)
  }

  async function checkLocalProxy(proxy: LocalProxy) {
    const checked = await ProxyService.CheckLocalProxy(proxy)
    const index = localProxies.value.findIndex((item) => item.id === checked.id)
    if (index !== -1) {
      localProxies.value[index] = checked
    }
    return checked
  }

  async function checkRemoteProxy(proxy: LocalProxy) {
    const checked = await ProxyService.CheckRemoteProxy(proxy)
    const index = localProxies.value.findIndex((item) => item.id === checked.id)
    if (index !== -1) {
      localProxies.value[index] = checked
    }
    return checked
  }

  async function loadServers() {
    serverLoading.value = true
    try {
      servers.value = (await ProxyService.ListServers()) ?? []
    } finally {
      serverLoading.value = false
    }
  }

  async function saveServer(server: ProxyServer) {
    const saved = await ProxyService.SaveServer(server)
    const index = servers.value.findIndex((item) => item.id === saved.id)
    if (index === -1) {
      servers.value.push(saved)
    } else {
      servers.value[index] = saved
    }
    return saved
  }

  function applyServer(server: ProxyServer) {
    const index = servers.value.findIndex((item) => item.id === server.id)
    if (index !== -1) {
      servers.value[index] = server
    }
  }

  async function checkServerPort(server: ProxyServer) {
    const checked = await ProxyService.CheckServerPort(server)
    applyServer(checked)
    return checked
  }

  async function deleteServer(id: string) {
    await ProxyService.DeleteServer(id)
    servers.value = servers.value.filter((item) => item.id !== id)
  }

  return {
    localProxies,
    servers,
    localLoading,
    serverLoading,
    loadLocalProxies,
    saveLocalProxy,
    deleteLocalProxy,
    checkLocalProxy,
    checkRemoteProxy,
    loadServers,
    saveServer,
    applyServer,
    checkServerPort,
    deleteServer,
  }
})
