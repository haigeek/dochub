<script setup lang="ts">
import { ref, watch } from 'vue'
import {
  NButton,
  NCard,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSelect,
  NSpace,
  useMessage,
} from 'naive-ui'
import { api } from '@/api'
import type { User } from '@/types'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [value: boolean] }>()
const message = useMessage()
const loading = ref(false)
const users = ref<User[]>([])
const form = ref({ username: '', password: '', role: 'member' as 'member' | 'admin' })

const columns = [
  { title: '用户名', key: 'username' },
  {
    title: '角色',
    key: 'role',
    render: (row: User) => (row.role === 'admin' ? '管理员' : '成员'),
  },
  {
    title: '创建时间',
    key: 'createdAt',
    render: (row: User) => new Date(row.createdAt).toLocaleString('zh-CN'),
  },
]

watch(
  () => props.show,
  async (show) => {
    if (!show) return
    await loadUsers()
  },
)

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

async function createUser() {
  if (!form.value.username.trim() || form.value.password.length < 8) {
    message.warning('用户名必填，密码至少 8 位')
    return
  }
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
  }
}
</script>

<template>
  <NModal :show="props.show" @update:show="emit('update:show', $event)">
    <NCard class="modal-card users-card" title="用户管理" :bordered="false">
      <NDataTable :columns="columns" :data="users" :loading="loading" :bordered="false" />
      <div class="user-create-panel">
        <h3>创建账号</h3>
        <NForm label-placement="top">
          <div class="user-form-grid">
            <NFormItem label="用户名">
              <NInput v-model:value="form.username" placeholder="团队用户名" />
            </NFormItem>
            <NFormItem label="初始密码">
              <NInput
                v-model:value="form.password"
                type="password"
                show-password-on-click
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
            <NButton type="primary" @click="createUser">创建用户</NButton>
          </NSpace>
        </NForm>
      </div>
    </NCard>
  </NModal>
</template>
