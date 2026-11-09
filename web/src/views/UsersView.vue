<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NPopconfirm,
  NSelect,
  NSpace,
  NTag,
  useMessage,
} from 'naive-ui'
import { api } from '@/api'
import { useSession } from '@/session'
import type { Organization, User } from '@/types'

const message = useMessage()
const session = useSession()
const loading = ref(false)
const creating = ref(false)
const updatingRole = ref('')
const affiliationSaving = ref(false)
const showAffiliationEditor = ref(false)
const affiliationUser = ref<User | null>(null)
const selectedOrganizationIds = ref<string[]>([])
const users = ref<User[]>([])
const organizations = ref<Organization[]>([])
const form = ref({ username: '', password: '', role: 'member' as 'member' | 'admin' })

const organizationOptions = computed(() =>
  organizations.value.map((organization) => ({
    label: organization.name,
    value: organization.id,
  })),
)

const columns = [
  { title: '用户名', key: 'username' },
  {
    title: '角色',
    key: 'role',
    render: (row: User) => (row.role === 'admin' ? '管理员' : '成员'),
  },
  {
    title: '所属组织',
    key: 'organizations',
    render: (row: User) => {
      const names = organizations.value
        .filter((organization) =>
          organization.members.some((member) => member.id === row.id),
        )
        .map((organization) => organization.name)
      if (names.length === 0) {
        return h('span', { class: 'table-muted' }, '未分组')
      }
      return h(
        'div',
        { class: 'user-organization-tags' },
        names.map((name) => h(NTag, { size: 'small' }, { default: () => name })),
      )
    },
  },
  {
    title: '创建时间',
    key: 'createdAt',
    render: (row: User) => new Date(row.createdAt).toLocaleString('zh-CN'),
  },
  {
    title: '操作',
    key: 'actions',
    render: (row: User) => {
      const isSelf = row.id === session.user.value?.id
      const nextRole = row.role === 'admin' ? 'member' : 'admin'
      return h(NSpace, { size: 8 }, {
        default: () => [
          h(
            NButton,
            {
              size: 'small',
              secondary: true,
              onClick: () => openAffiliationEditor(row),
            },
            { default: () => '设置组织' },
          ),
          h(
            NPopconfirm,
            {
              positiveText: '确认',
              negativeText: '取消',
              onPositiveClick: () => updateUserRole(row),
            },
            {
              trigger: () =>
                h(
                  NButton,
                  {
                    size: 'small',
                    secondary: true,
                    type: row.role === 'admin' ? 'warning' : 'primary',
                    disabled: isSelf || updatingRole.value === row.id,
                    title: isSelf ? '不能修改自己的角色' : undefined,
                  },
                  {
                    default: () =>
                      isSelf
                        ? '当前账号'
                        : nextRole === 'admin'
                          ? '设为管理员'
                          : '设为成员',
                  },
                ),
              default: () =>
                `确定将 ${row.username} 设为${nextRole === 'admin' ? '管理员' : '成员'}吗？`,
            },
          ),
        ],
      })
    },
  },
]

onMounted(loadAll)

async function loadAll() {
  await Promise.all([loadUsers(), loadOrganizations()])
}

async function loadUsers() {
  loading.value = true
  try {
    users.value = await api<User[]>('/api/users')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载用户失败')
  } finally {
    loading.value = false
  }
}

async function loadOrganizations() {
  try {
    organizations.value = await api<Organization[]>('/api/admin/organizations')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载组织机构失败')
  }
}

async function createUser() {
  if (!form.value.username.trim() || form.value.password.length < 8) {
    message.warning('用户名必填，密码至少 8 位')
    return
  }
  creating.value = true
  try {
    await api<User>('/api/users', {
      method: 'POST',
      body: JSON.stringify({
        username: form.value.username.trim(),
        password: form.value.password,
        role: form.value.role,
      }),
    })
    message.success('用户已创建')
    form.value = { username: '', password: '', role: 'member' }
    await loadUsers()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '创建用户失败')
  } finally {
    creating.value = false
  }
}

async function updateUserRole(user: User) {
  const role: User['role'] = user.role === 'admin' ? 'member' : 'admin'
  updatingRole.value = user.id
  try {
    const updated = await api<User>(`/api/users/${user.id}/role`, {
      method: 'PATCH',
      body: JSON.stringify({ role }),
    })
    users.value = users.value.map((item) => (item.id === updated.id ? updated : item))
    message.success(`${updated.username} 已设为${role === 'admin' ? '管理员' : '成员'}`)
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '修改用户角色失败')
  } finally {
    updatingRole.value = ''
  }
}

function openAffiliationEditor(user: User) {
  affiliationUser.value = user
  selectedOrganizationIds.value = organizations.value
    .filter((organization) =>
      organization.members.some((member) => member.id === user.id),
    )
    .map((organization) => organization.id)
  showAffiliationEditor.value = true
}

async function saveUserOrganizations() {
  if (!affiliationUser.value) return
  affiliationSaving.value = true
  try {
    await api<void>(`/api/admin/users/${affiliationUser.value.id}/organizations`, {
      method: 'PUT',
      body: JSON.stringify({
        organizationIds: selectedOrganizationIds.value,
      }),
    })
    message.success(`${affiliationUser.value.username} 的组织归属已更新`)
    showAffiliationEditor.value = false
    await loadOrganizations()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '保存组织归属失败')
  } finally {
    affiliationSaving.value = false
  }
}
</script>

<template>
  <NCard class="admin-card" :bordered="false">
    <template #header>
      <div class="admin-card-heading">
        <strong>用户管理</strong>
        <span>管理系统账号和角色，并为用户设置所属组织机构</span>
      </div>
    </template>
    <template #header-extra>
      <span class="admin-card-count">共 {{ users.length }} 个账号</span>
    </template>

    <NDataTable :columns="columns" :data="users" :loading="loading" :bordered="false" />

    <div class="user-create-panel">
      <div class="user-create-heading">
        <h3>创建本地账号</h3>
        <p>单点登录用户首次登录时会自动创建成员账号，默认不属于任何组织。</p>
      </div>
      <NForm label-placement="top">
        <div class="user-form-grid">
          <NFormItem label="用户名">
            <NInput v-model:value="form.username" placeholder="团队用户名" />
          </NFormItem>
          <NFormItem label="初始密码">
            <NInput
              v-model:value="form.password"
              type="password"
              show-password-on="click"
              placeholder="至少 8 位"
            />
          </NFormItem>
          <NFormItem label="角色">
            <NSelect
              v-model:value="form.role"
              :options="[
                { label: '成员', value: 'member' },
                { label: '管理员', value: 'admin' },
              ]"
            />
          </NFormItem>
        </div>
        <NSpace justify="end">
          <NButton type="primary" :loading="creating" @click="createUser">创建用户</NButton>
        </NSpace>
      </NForm>
    </div>

    <NModal
      :show="showAffiliationEditor"
      :mask-closable="!affiliationSaving"
      @update:show="showAffiliationEditor = $event"
    >
      <NCard class="modal-card access-card" title="设置所属组织" :bordered="false">
        <div v-if="affiliationUser">
          <p class="manage-section-hint">
            为 <strong>{{ affiliationUser.username }}</strong> 选择一个或多个组织机构。
            组织变化会立即影响其通过组织获得的项目和文档读取权限。
          </p>
          <NForm label-placement="top">
            <NFormItem label="所属组织机构">
              <NSelect
                v-model:value="selectedOrganizationIds"
                to="body"
                multiple
                filterable
                clearable
                :options="organizationOptions"
                placeholder="不选择表示暂不加入任何组织"
              />
            </NFormItem>
          </NForm>
        </div>
        <NSpace justify="end" class="modal-section-actions">
          <NButton :disabled="affiliationSaving" @click="showAffiliationEditor = false">
            取消
          </NButton>
          <NButton
            type="primary"
            :loading="affiliationSaving"
            :disabled="!affiliationUser"
            @click="saveUserOrganizations"
          >
            保存归属
          </NButton>
        </NSpace>
      </NCard>
    </NModal>
  </NCard>
</template>
