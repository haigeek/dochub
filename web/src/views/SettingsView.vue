<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NCollapse,
  NCollapseItem,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NPopconfirm,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
  useMessage,
} from 'naive-ui'
import { api } from '@/api'
import type { SSOConfig, SSOProvider } from '@/types'

const message = useMessage()
const loading = ref(false)
const saving = ref(false)
const mode = ref<'list' | 'edit'>('list')
const editingId = ref('')
const clientSecret = ref('')
const clearClientSecret = ref(false)
const providers = ref<SSOProvider[]>([])
const draft = ref<SSOConfig>(defaults())
const callbackPath = computed(
  () => `/api/auth/oauth2/${draft.value.authKey.trim() || 'pending'}/callback`,
)

const callbackURL = computed({
  get: () => draft.value.redirectUri || `${window.location.origin}${callbackPath.value}`,
  set: (value: string) => { draft.value.redirectUri = value },
})
const isNew = computed(() => !editingId.value)
const cardTitle = computed(() => {
  if (mode.value === 'list') return '单点登录'
  return isNew.value ? '新增单点登录站点' : '编辑单点登录站点'
})

onMounted(loadProviders)

function defaults(): SSOConfig {
  return {
    id: '',
    authKey: '',
    enabled: false,
    providerName: '统一身份认证',
    endpointBaseUrl: '',
    authorizePath: '',
    tokenPath: '',
    userInfoPath: '',
    clientId: '',
    hasClientSecret: false,
    redirectUri: '',
    portalUrl: '',
    issuer: '',
    scopes: [],
    tokenAuthMethod: 'client_secret_post',
    pkce: false,
    subjectField: 'sub',
    usernameField: 'name',
    updatedBy: '',
    updatedAt: null,
  }
}

async function loadProviders() {
  loading.value = true
  try {
    const data = await api<{ providers: SSOProvider[] }>('/api/admin/sso')
    providers.value = data.providers
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载单点登录配置失败')
  } finally {
    loading.value = false
  }
}

function createProvider() {
  editingId.value = ''
  draft.value = defaults()
  clientSecret.value = ''
  clearClientSecret.value = false
  mode.value = 'edit'
}

function editProvider(provider: SSOProvider) {
  editingId.value = provider.id
  draft.value = { ...provider, scopes: [...provider.scopes] }
  clientSecret.value = ''
  clearClientSecret.value = false
  mode.value = 'edit'
}

async function backToList() {
  await loadProviders()
  mode.value = 'list'
}

async function save() {
  if (!draft.value.authKey.trim()) {
    message.warning('请填写认证标识')
    return
  }
  if (draft.value.enabled && !draft.value.clientId.trim()) {
    message.warning('启用单点登录前请填写客户端 ID')
    return
  }
  if (draft.value.enabled && !draft.value.hasClientSecret && !clientSecret.value) {
    message.warning('启用单点登录前请填写客户端密钥')
    return
  }
  saving.value = true
  try {
    const path = isNew.value ? '/api/admin/sso' : `/api/admin/sso/${editingId.value}`
    const data = await api<SSOProvider>(path, {
      method: isNew.value ? 'POST' : 'PUT',
      body: JSON.stringify({
        ...draft.value,
        redirectUri: callbackURL.value,
        clientSecret: clientSecret.value,
        clearClientSecret: clearClientSecret.value,
      }),
    })
    editingId.value = data.id
    draft.value = { ...data, scopes: [...data.scopes] }
    clientSecret.value = ''
    clearClientSecret.value = false
    message.success('单点登录站点已保存')
    await loadProviders()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '保存单点登录配置失败')
  } finally {
    saving.value = false
  }
}

function updateAuthKey(value: string) {
  const previous = draft.value.authKey
  draft.value.authKey = value
  if (!draft.value.redirectUri || !previous) return
  try {
    const parsed = new URL(draft.value.redirectUri)
    if (parsed.pathname === `/api/auth/oauth2/${previous}/callback`) {
      parsed.pathname = `/api/auth/oauth2/${value.trim() || 'pending'}/callback`
      draft.value.redirectUri = parsed.toString()
    }
  } catch {
    // 手工填写的非标准回调地址保持不变。
  }
}

async function removeProvider() {
  if (!editingId.value) return
  saving.value = true
  try {
    await api<void>(`/api/admin/sso/${editingId.value}`, { method: 'DELETE' })
    message.success('单点登录站点已删除')
    await backToList()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '删除单点登录站点失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <NCard class="admin-card" :bordered="false" :loading="loading">
        <template #header>
          <div class="admin-card-heading">
            <strong>{{ cardTitle }}</strong>
            <span>统一 OAuth2 单点登录</span>
          </div>
        </template>
        <template #header-extra>
          <NButton v-if="mode === 'list'" type="primary" @click="createProvider">新增站点</NButton>
          <NButton v-else @click="backToList">返回站点列表</NButton>
        </template>

        <template v-if="mode === 'list'">
          <NAlert type="info" :show-icon="false" class="sso-alert">
            每个站点拥有独立回调地址和账号绑定。登录页会展示所有已启用站点。
          </NAlert>
          <div v-if="providers.length" class="sso-provider-list">
            <button
              v-for="provider in providers"
              :key="provider.id"
              class="sso-provider-item"
              @click="editProvider(provider)"
            >
              <div class="sso-provider-main">
                <div class="sso-provider-heading">
                  <strong>{{ provider.providerName }}</strong>
                  <NTag :type="provider.enabled ? 'success' : 'default'" size="small">
                    {{ provider.enabled ? '已启用' : '已停用' }}
                  </NTag>
                </div>
                <span>{{ provider.endpointBaseUrl || '尚未配置端点' }}</span>
                <small>认证标识：{{ provider.authKey }}</small>
                <small :title="provider.redirectUri">
                  回调地址：{{ provider.redirectUri || '尚未配置' }}
                </small>
              </div>
              <span class="sso-provider-enter">编辑 →</span>
            </button>
          </div>
          <NEmpty v-else description="还没有配置单点登录站点">
            <template #extra>
              <NButton type="primary" @click="createProvider">新增第一个站点</NButton>
            </template>
          </NEmpty>
        </template>

        <template v-else>
          <NForm label-placement="top">
            <div class="sso-enable-row">
              <div>
                <strong>启用此站点</strong>
                <p>关闭后登录页隐藏此入口，已有 DocHub 会话不受影响。</p>
              </div>
              <NSwitch v-model:value="draft.enabled" />
            </div>

            <div class="sso-form-grid">
              <NFormItem label="认证标识" required>
                <NInput
                  :value="draft.authKey"
                  placeholder="例如 collie"
                  @update:value="updateAuthKey"
                />
                <span class="form-hint">
                  用于生成 /api/auth/oauth2/&lt;认证标识&gt;/start 和 callback 路径，保存后修改需同步身份服务。
                </span>
              </NFormItem>
              <NFormItem label="显示名称">
                <NInput v-model:value="draft.providerName" placeholder="例如 Collie" />
              </NFormItem>
            </div>
            <NFormItem label="客户端 ID">
              <NInput v-model:value="draft.clientId" placeholder="App.id 或客户端 ID" />
            </NFormItem>

            <NFormItem :label="draft.hasClientSecret ? '客户端密钥（已配置，留空保持不变）' : '客户端密钥'">
              <NInput
                v-model:value="clientSecret"
                type="password"
                show-password-on="click"
                :disabled="clearClientSecret"
                placeholder="仅提交到后端，不回显"
              />
            </NFormItem>
            <NCheckbox v-if="draft.hasClientSecret" v-model:checked="clearClientSecret">
              清除当前客户端密钥
            </NCheckbox>

            <NFormItem label="端点基础地址">
              <NInput v-model:value="draft.endpointBaseUrl" placeholder="http://collie.example.com:10200" />
            </NFormItem>
            <div class="sso-form-grid three">
              <NFormItem label="授权 path">
                <NInput v-model:value="draft.authorizePath" placeholder="/collie/oauth2/authorize" />
              </NFormItem>
              <NFormItem label="Token path">
                <NInput v-model:value="draft.tokenPath" placeholder="/collie/oauth2/token" />
              </NFormItem>
              <NFormItem label="用户信息 path">
                <NInput v-model:value="draft.userInfoPath" placeholder="/collie/oauth2/userinfo" />
              </NFormItem>
            </div>

            <NFormItem label="回调地址">
              <div class="sso-callback-field">
                <NInput v-model:value="callbackURL" placeholder="请输入身份服务登记的完整回调地址" />
                <NButton :disabled="saving" @click="draft.redirectUri = ''">自动填充</NButton>
                <span class="form-hint">
                  默认按当前访问地址填充，可修改为部署环境的外部地址。
                  自定义路径需转发至 {{ callbackPath }}，不能填写其他站点的标准回调路径；
                  新建站点保存后请核对最终回调地址。
                </span>
              </div>
            </NFormItem>
            <div class="sso-form-grid">
              <NFormItem label="门户地址（可选）">
                <NInput v-model:value="draft.portalUrl" placeholder="https://portal.example.com" />
              </NFormItem>
              <NFormItem label="固定签发方（可选）">
                <NInput v-model:value="draft.issuer" placeholder="用于限制或标记身份来源" />
              </NFormItem>
            </div>

            <NCollapse class="sso-advanced">
              <NCollapseItem title="高级身份映射" name="advanced">
                <div class="sso-form-grid">
                  <NFormItem label="Scope">
                    <NInput
                      :value="draft.scopes.join(',')"
                      placeholder="profile,email"
                      @update:value="draft.scopes = $event.split(',').map((item) => item.trim()).filter(Boolean)"
                    />
                  </NFormItem>
                  <NFormItem label="客户端认证方式">
                    <NSelect
                      v-model:value="draft.tokenAuthMethod"
                      :options="[
                        { label: '表单 client_secret_post', value: 'client_secret_post' },
                        { label: 'Basic client_secret_basic', value: 'client_secret_basic' },
                      ]"
                    />
                  </NFormItem>
                  <NFormItem label="启用 PKCE (S256)">
                    <NSwitch v-model:value="draft.pkce" />
                  </NFormItem>
                  <NFormItem label="用户 ID 字段">
                    <NInput v-model:value="draft.subjectField" placeholder="sub 或 data.id" />
                  </NFormItem>
                  <NFormItem label="用户名显示字段">
                    <NInput v-model:value="draft.usernameField" placeholder="name 或 data.name" />
                  </NFormItem>
                </div>
              </NCollapseItem>
            </NCollapse>
          </NForm>
          <NSpace justify="space-between" class="sso-actions">
            <NPopconfirm v-if="!isNew" @positive-click="removeProvider">
              <template #trigger>
                <NButton type="error" secondary :loading="saving">删除站点</NButton>
              </template>
              删除后该站点的登录入口将不可用，已有 DocHub 账号和授权仍会保留。
            </NPopconfirm>
            <span v-else />
            <NSpace>
              <NButton @click="backToList">取消</NButton>
              <NButton type="primary" :loading="saving" @click="save">保存配置</NButton>
            </NSpace>
          </NSpace>
        </template>
      </NCard>
</template>
