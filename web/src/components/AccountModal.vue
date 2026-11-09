<script setup lang="ts">
import { ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NPopconfirm,
  NSelect,
  NSpace,
  NTabPane,
  NTabs,
  useMessage,
} from 'naive-ui'
import { api } from '@/api'
import type { AccessToken } from '@/types'

const props = defineProps<{
  show: boolean
  username: string
  hasPassword: boolean
}>()
const emit = defineEmits<{
  'update:show': [value: boolean]
  changed: []
}>()

const message = useMessage()
const tab = ref('password')
const passwordSaving = ref(false)
const passwordError = ref('')
const passwordForm = ref({
  currentPassword: '',
  newPassword: '',
  confirmPassword: '',
})
const tokens = ref<AccessToken[]>([])
const tokensLoading = ref(false)
const tokenSaving = ref(false)
const tokenForm = ref({
  name: '',
  expiresInDays: 90,
})

const expiryOptions = [
  { label: '30 天', value: 30 },
  { label: '90 天', value: 90 },
  { label: '365 天', value: 365 },
  { label: '长期有效', value: 0 },
]

watch(
  () => props.show,
  async (show) => {
    if (!show) return
    tab.value = 'password'
    passwordError.value = ''
    passwordForm.value = { currentPassword: '', newPassword: '', confirmPassword: '' }
    tokenForm.value = {
      name: '',
      expiresInDays: 90,
    }
    await loadTokens()
  },
)

async function loadTokens() {
  tokensLoading.value = true
  try {
    tokens.value = await api<AccessToken[]>('/api/account/tokens')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '读取 Agent 凭证失败')
  } finally {
    tokensLoading.value = false
  }
}

async function changePassword() {
  passwordError.value = ''
  if (passwordForm.value.newPassword.length < 8) {
    passwordError.value = '新密码至少 8 位'
    return
  }
  if (passwordForm.value.newPassword !== passwordForm.value.confirmPassword) {
    passwordError.value = '两次输入的新密码不一致'
    return
  }
  passwordSaving.value = true
  try {
    await api<void>('/api/auth/password', {
      method: 'PUT',
      body: JSON.stringify({
        currentPassword: passwordForm.value.currentPassword,
        newPassword: passwordForm.value.newPassword,
      }),
    })
    message.success(props.hasPassword ? '密码已修改' : '本地密码已设置')
    emit('changed')
    passwordForm.value = { currentPassword: '', newPassword: '', confirmPassword: '' }
  } catch (reason) {
    passwordError.value = reason instanceof Error ? reason.message : '保存密码失败'
  } finally {
    passwordSaving.value = false
  }
}

async function createToken() {
  if (!tokenForm.value.name.trim()) {
    message.warning('请填写凭证名称')
    return
  }
  tokenSaving.value = true
  try {
    await api<AccessToken>('/api/account/tokens', {
      method: 'POST',
      body: JSON.stringify({
        name: tokenForm.value.name.trim(),
        expiresInDays: tokenForm.value.expiresInDays,
      }),
    })
    message.success('Agent 凭证已创建')
    tokenForm.value.name = ''
    await loadTokens()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '创建 Agent 凭证失败')
  } finally {
    tokenSaving.value = false
  }
}

async function copyToken(token: string) {
  try {
    await navigator.clipboard.writeText(token)
    message.success('凭证已复制')
  } catch {
    message.error('复制失败，请手动选择凭证文本')
  }
}

async function revokeToken(id: string) {
  try {
    await api<void>(`/api/account/tokens/${id}`, { method: 'DELETE' })
    message.success('Agent 凭证已撤销')
    await loadTokens()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '撤销 Agent 凭证失败')
  }
}

function formatTime(value: string | null, fallback: string) {
  return value ? new Date(value).toLocaleString('zh-CN') : fallback
}
</script>

<template>
  <NModal
    :show="props.show"
    :mask-closable="!passwordSaving && !tokenSaving"
    @update:show="emit('update:show', $event)"
  >
    <NCard class="modal-card account-card" title="账号设置" :bordered="false">
      <div class="account-identity">
        <span>当前登录名</span>
        <code>{{ props.username }}</code>
        <small>设置本地密码后，可使用该登录名登录 DocHub 和配置 Basic Auth。</small>
      </div>
      <NTabs v-model:value="tab" type="line" animated>
        <NTabPane name="password" tab="密码">
          <NAlert v-if="passwordError" type="error" :show-icon="false" class="modal-alert">
            {{ passwordError }}
          </NAlert>
          <NAlert v-if="!props.hasPassword" type="info" :show-icon="false" class="modal-alert">
            当前账号通过单点登录创建，尚未设置本地密码。设置后也可使用 Basic Auth 访问有权限的资源。
          </NAlert>
          <NForm label-placement="top" @submit.prevent="changePassword">
            <NFormItem v-if="props.hasPassword" label="当前密码" required>
              <NInput
                v-model:value="passwordForm.currentPassword"
                type="password"
                show-password-on="click"
                autocomplete="current-password"
                placeholder="请输入当前密码"
              />
            </NFormItem>
            <NFormItem :label="props.hasPassword ? '新密码' : '设置密码'" required>
              <NInput
                v-model:value="passwordForm.newPassword"
                type="password"
                show-password-on="click"
                autocomplete="new-password"
                placeholder="至少 8 位，最多 72 字节"
              />
            </NFormItem>
            <NFormItem label="确认新密码" required>
              <NInput
                v-model:value="passwordForm.confirmPassword"
                type="password"
                show-password-on="click"
                autocomplete="new-password"
                placeholder="再次输入新密码"
              />
            </NFormItem>
            <NSpace justify="end">
              <NButton type="primary" attr-type="submit" :loading="passwordSaving">
                {{ props.hasPassword ? '修改密码' : '设置密码' }}
              </NButton>
            </NSpace>
          </NForm>
        </NTabPane>

        <NTabPane name="tokens" tab="Agent 凭证">
          <NAlert type="info" :show-icon="false" class="modal-alert">
            凭证只用于读取文档，不能登录 Web 或执行写操作。令牌加密保存，登录后仍可查看；
            请勿公开发送，泄露后应立即撤销。
          </NAlert>

          <NForm label-placement="top" @submit.prevent="createToken">
            <div class="token-form-grid">
              <NFormItem label="凭证名称" required>
                <NInput v-model:value="tokenForm.name" placeholder="例如 Cursor、Claude Code" />
              </NFormItem>
              <NFormItem label="有效期">
                <NSelect v-model:value="tokenForm.expiresInDays" :options="expiryOptions" />
              </NFormItem>
            </div>
            <NSpace justify="end">
              <NButton type="primary" attr-type="submit" :loading="tokenSaving">创建凭证</NButton>
            </NSpace>
          </NForm>

          <div class="token-list-heading">
            <strong>已创建的凭证</strong>
            <span>共 {{ tokens.length }} 个</span>
          </div>
          <div v-if="tokens.length" class="token-list">
            <div v-for="token in tokens" :key="token.id" class="token-item">
              <div class="token-item-main">
                <div>
                  <strong>{{ token.name }}</strong>
                  <code>{{ token.prefix }}…</code>
                </div>
                <span>权限：跟随当前账号</span>
                <div class="token-secret">
                  <code>{{ token.token }}</code>
                  <NButton size="tiny" secondary @click="copyToken(token.token)">复制</NButton>
                </div>
                <small>
                  到期：{{ formatTime(token.expiresAt, '长期有效') }} ·
                  最近使用：{{ formatTime(token.lastUsedAt, '尚未使用') }}
                </small>
              </div>
              <NPopconfirm @positive-click="revokeToken(token.id)">
                <template #trigger>
                  <NButton size="small" type="error" secondary>撤销</NButton>
                </template>
                撤销后，使用该凭证的 Agent 会立即失去访问权限。
              </NPopconfirm>
            </div>
          </div>
          <NEmpty v-else :show-icon="false" description="还没有创建 Agent 凭证" />
        </NTabPane>
      </NTabs>
    </NCard>
  </NModal>
</template>
