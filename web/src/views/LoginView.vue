<script setup lang="ts">
import { reactive, ref } from 'vue'
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
} from 'naive-ui'
import { useSession } from '@/session'

const router = useRouter()
const route = useRoute()
const session = useSession()
const loading = ref(false)
const error = ref('')
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
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/projects'
    await router.replace(redirect)
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
      <NAlert v-if="error" type="error" :show-icon="false" class="login-alert">
        {{ error }}
      </NAlert>
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
            show-password-on-click
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
      <p class="login-footnote">账号由管理员创建。登录后即可浏览团队全部文档。</p>
    </NCard>
  </main>
</template>
