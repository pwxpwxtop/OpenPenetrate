<template>
  <section class="page">
    <header class="page-head">
      <div>
        <h1>{{ lang.toOr('promotion.pageTitle', '服务器推荐') }}</h1>
        <p>{{ lang.toOr('promotion.pageSubtitle', '选购一台有公网 IP 的云服务器即可部署 frps 服务端，点击卡片跳转到对应官网。') }}</p>
      </div>
    </header>

    <div class="promo-grid">
      <button
        v-for="item in providers"
        :key="item.key"
        type="button"
        class="promo-card"
        :disabled="opening === item.key"
        @click="openProvider(item)"
      >
        <div class="logo-wrap">
          <img :src="item.logo" :alt="item.name" class="logo-img" />
        </div>
        <div class="card-body">
          <div class="card-title-row">
            <span class="card-name">{{ item.name }}</span>
            <i class="fa-solid fa-arrow-up-right-from-square card-link-icon"></i>
          </div>
          <p class="card-desc">{{ item.desc }}</p>
          <span class="card-action">
            <i class="fa-solid fa-compass"></i>
            {{ opening === item.key ? lang.toOr('promotion.opening', '正在打开浏览器…') : lang.toOr('promotion.visitNow', '立即访问') }}
          </span>
        </div>
      </button>
    </div>

    <div class="promo-tip">
      <i class="fa-solid fa-circle-info"></i>
      <span>{{ lang.toOr('promotion.tip', '部署提示：选购服务器时建议选择带公网 IP 的实例，并在安全组中放行 frps 端口（默认 7000）。') }}</span>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Browser } from '@wailsio/runtime'
import { useToast } from 'primevue/usetoast'
import { useLanguageStore } from '../../stores/language'
import aliyunLogo from '../../../assets/promotion/aliyun.png'
// import huaweiyunLogo from '../../../assets/promotion/huaweiyun.png'
import tenxunyunLogo from '../../../assets/promotion/tenxunyun.png'
import tianyiyunLogo from '../../../assets/promotion/tianyiyun.png'
import baiduyunLogo from '../../../assets/promotion/baiduyun.png'

interface Provider {
  key: string
  name: string
  desc: string
  logo: string
  // TODO: 推广跳转链接由项目维护者提供，填入下方 url 即可
  url: string
}

const toast = useToast()
const lang = useLanguageStore()
const opening = ref('')

const providers = computed<Provider[]>(() => [
//   {
//     key: 'aliyun',
//     name: '阿里云',
//     desc: '弹性计算 ECS，多地域可选，新用户享专属优惠。',
//     logo: aliyunLogo,
//     // TODO: 替换为你的阿里云推广链接
//     url: '',
//   },
  {
    key: 'tencent',
    name: lang.toOr('promotion.tencentName', '腾讯云'),
    desc: lang.toOr('promotion.tencentDesc', '轻量应用服务器开箱即用，适合快速部署穿透服务端。'),
    logo: tenxunyunLogo,
    // TODO: 替换为你的腾讯云推广链接
    url: 'https://curl.qcloud.com/8Bba0raB',
  },
//   {
//     key: 'huawei',
//     name: '华为云',
//     desc: '弹性云服务器 ECS，网络性能稳定，企业级可靠。',
//     logo: huaweiyunLogo,
//     // TODO: 替换为你的华为云推广链接
//     url: '',
//   },
//   {
//     key: 'tianyi',
//     name: '天翼云',
//     desc: '天翼云主机，国内节点覆盖广，带宽资源充足。',
//     logo: tianyiyunLogo,
//     // TODO: 替换为你的天翼云推广链接
//     url: 'https://www.ctyun.cn/h5/auth/register?partnerType=cust&partnerAccountId=f25b80eb880740e680a124c6777644da',
//   },
  {
    key: 'baidu',
    name: lang.toOr('promotion.baiduName', '百度云'),
    desc: lang.toOr('promotion.baiduDesc', '百度智能云服务器 BCC，性能稳定，新用户享优惠活动。'),
    logo: baiduyunLogo,
    // TODO: 替换为你的百度云推广链接
    url: 'https://cloud.baidu.com/campaign/ambassador-product/index.html?ambassadorId=68d8bb6a19b443a3ae3af7b881f8bb8a',
  },
])

async function openProvider(item: Provider) {
  if (!item.url) {
    toast.add({
      severity: 'warn',
      summary: lang.toOr('promotion.urlNotConfigured', '链接未配置'),
      detail: `${item.name}${lang.toOr('promotion.urlNotConfiguredDetail', '的推广链接待配置，请在 PromotionServer.vue 中填写 url。')}`,
      life: 4200,
    })
    return
  }
  opening.value = item.key
  try {
    await Browser.OpenURL(item.url)
  } catch (err) {
    toast.add({ severity: 'error', summary: lang.toOr('promotion.browserOpenFailed', '打开浏览器失败'), detail: String(err), life: 5200 })
  } finally {
    opening.value = ''
  }
}
</script>

<style scoped>
.promo-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 16px;
}

.promo-card {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--panel);
  color: inherit;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.18s ease, background 0.18s ease, transform 0.18s ease, box-shadow 0.18s ease;
}

.promo-card:hover:not(:disabled) {
  border-color: var(--accent-border);
  background: var(--panel-strong);
  transform: translateY(-2px);
  box-shadow: 0 12px 30px var(--accent-soft);
}

.promo-card:disabled {
  opacity: 0.7;
  cursor: wait;
}

.logo-wrap {
  display: grid;
  place-items: center;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.92);
}

.logo-img {
  width: 100px;
  height: 100px;
  object-fit: contain;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.card-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.card-name {
  font-size: 15px;
  font-weight: 700;
  color: var(--text);
}

.card-link-icon {
  font-size: 12px;
  color: var(--muted);
  transition: color 0.18s ease, transform 0.18s ease;
}

.promo-card:hover .card-link-icon {
  color: var(--accent);
  transform: translate(1px, -1px);
}

.card-desc {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.65;
  color: var(--muted);
}

.card-action {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  margin-top: 2px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--accent);
}

.card-action i {
  font-size: 12px;
}

.promo-tip {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--panel);
  font-size: 12.5px;
  color: var(--muted);
}

.promo-tip i {
  color: var(--accent);
  font-size: 13px;
}
</style>
