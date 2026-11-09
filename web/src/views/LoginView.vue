<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { LockClosedOutline, PersonOutline } from '@vicons/ionicons5'
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NIcon,
  NInput,
  NTabPane,
  NTabs,
} from 'naive-ui'
import { useSession } from '@/session'
import { api } from '@/api'

const router = useRouter()
const route = useRoute()
const session = useSession()
const loading = ref(false)
const error = ref('')
const activeTab = ref('password')
interface OAuthProvider {
  id: string
  name: string
  loginUrl: string
  portalUrl?: string
  loginHint?: string
}
const authOptions = ref<{ providers: OAuthProvider[] }>({ providers: [] })
const oauthErrors: Record<string, string> = {
  invalid_state: '登录请求已失效，请重新发起单点登录。',
  login_required: '请先登录对应身份服务的门户，再返回此页重新发起登录。',
  authorization_expired: '授权已过期，请重新登录。',
  authorization_failed: '授权失败，请联系管理员检查应用配置。',
  invalid_identity: '身份服务返回的信息无效，请联系管理员。',
  unavailable: '单点登录暂时不可用，请稍后重试，或使用本地账号登录。',
}
const oauthError = computed(() => {
  const code = route.query.oauthError
  return typeof code === 'string' ? oauthErrors[code] ?? '单点登录失败，请重新尝试。' : ''
})
const returnPath = computed(() => {
  const value = route.query.redirect
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') ||
      /[\\\r\n]/.test(value)) return '/projects'
  try {
    const parsed = new URL(value, window.location.origin)
    if (parsed.origin !== window.location.origin || parsed.pathname === '/login' ||
        parsed.pathname.startsWith('/api/')) return '/projects'
    return parsed.pathname + parsed.search + parsed.hash
  } catch {
    return '/projects'
  }
})
function oauthLoginUrl(provider: OAuthProvider) {
  return `${provider.loginUrl}?${new URLSearchParams({ redirect: returnPath.value })}`
}
function providerTabName(provider: OAuthProvider) {
  return `provider:${provider.id}`
}

onMounted(async () => {
  try {
    authOptions.value = await api<typeof authOptions.value>('/api/auth/options')
  } catch {
    // 登录方式加载失败时仍允许使用本地账号，避免上游故障阻断管理员入口。
  }
})
const form = reactive({
  username: '',
  password: '',
})

async function submit() {
  error.value = ''
  if (!form.username.trim() || !form.password) {
    error.value = '请输入用户名和密码'
    return
  }
  loading.value = true
  try {
    await session.login(form.username, form.password)
    await router.replace(returnPath.value)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-intro">
      <div class="intro-kicker">DOCS, IN CONTEXT.</div>
      <h1>让源码里的知识<br />被团队真正看见。</h1>
      <p>连接代码仓库，直接浏览 Markdown 文档、目录结构和分支版本。内容跟着代码走，阅读始终清晰。</p>
      <div class="feature-row">
        <span>SSH 仓库同步</span>
        <span>多分支快照</span>
        <span>全文检索</span>
      </div>
    </section>

    <NCard class="login-card" :bordered="false">
      <div class="login-card-heading">
        <span class="brand-mark large">D</span>
        <h2>登录 DocHub</h2>
        <p>进入团队的源码文档空间</p>
      </div>
      <NAlert v-if="error || oauthError" type="error" :show-icon="false" class="login-alert">
        {{ error || oauthError }}
      </NAlert>
      <NTabs v-model:value="activeTab" type="line" animated class="login-method-tabs">
        <NTabPane name="password" tab="账号密码">
          <NForm @submit.prevent="submit">
            <NFormItem label="用户名">
              <NInput v-model:value="form.username" placeholder="请输入用户名" size="large">
                <template #prefix><NIcon :component="PersonOutline" /></template>
              </NInput>
            </NFormItem>
            <NFormItem label="密码">
              <NInput
                v-model:value="form.password"
                type="password"
                show-password-on="click"
                placeholder="请输入密码"
                size="large"
                @keyup.enter="submit"
              >
                <template #prefix><NIcon :component="LockClosedOutline" /></template>
              </NInput>
            </NFormItem>
            <NButton type="primary" size="large" block :loading="loading" attr-type="submit">
              进入文档中心
            </NButton>
          </NForm>
        </NTabPane>
        <NTabPane
          v-for="provider in authOptions.providers"
          :key="provider.id"
          :name="providerTabName(provider)"
          :tab="`${provider.name} 登录`"
        >
          <div class="oauth-provider">
            <p class="login-method-description">
              通过 {{ provider.name }} 完成身份认证，然后进入团队的源码文档空间。
            </p>
            <NButton tag="a" :href="oauthLoginUrl(provider)" type="primary" size="large" block>
              使用 {{ provider.name }} 登录
            </NButton>
            <p v-if="provider.loginHint || provider.portalUrl" class="login-footnote">
              {{ provider.loginHint }}
              <a
                v-if="provider.portalUrl"
                :href="provider.portalUrl"
                target="_blank"
                rel="noopener noreferrer"
              >打开 {{ provider.name }} 门户</a>
            </p>
          </div>
        </NTabPane>
      </NTabs>
      <p class="login-footnote">本地账号由管理员创建；单点登录首次使用时自动创建成员账号。文档访问需获得授权。</p>
    </NCard>
  </main>
</template>
