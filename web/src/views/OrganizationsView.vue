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
import type { Organization, User } from '@/types'

const message = useMessage()
const loading = ref(false)
const saving = ref(false)
const showEditor = ref(false)
const editingOrganizationID = ref('')
const organizations = ref<Organization[]>([])
const users = ref<User[]>([])
const form = ref({ name: '', userIds: [] as string[] })

const userOptions = computed(() =>
  users.value.map((user) => ({
    label: `${user.username}${user.role === 'admin' ? '（管理员）' : ''}`,
    value: user.id,
  })),
)

const columns = [
  {
    title: '组织机构',
    key: 'name',
    render: (row: Organization) => h('strong', null, row.name),
  },
  {
    title: '成员',
    key: 'members',
    render: (row: Organization) => {
      if (row.members.length === 0) {
        return h('span', { class: 'table-muted' }, '暂无成员')
      }
      return h(
        'div',
        { class: 'user-organization-tags' },
        row.members.map((member) =>
          h(
            NTag,
            { size: 'small', bordered: false },
            { default: () => member.username },
          ),
        ),
      )
    },
  },
  {
    title: '成员数',
    key: 'memberCount',
    width: 90,
    render: (row: Organization) => row.members.length,
  },
  {
    title: '更新时间',
    key: 'updatedAt',
    render: (row: Organization) => new Date(row.updatedAt).toLocaleString('zh-CN'),
  },
  {
    title: '操作',
    key: 'actions',
    render: (row: Organization) =>
      h(NSpace, { size: 8 }, {
        default: () => [
          h(
            NButton,
            {
              size: 'small',
              secondary: true,
              onClick: () => openEditor(row),
            },
            { default: () => '编辑' },
          ),
          h(
            NPopconfirm,
            {
              positiveText: '永久删除',
              negativeText: '取消',
              type: 'warning',
              onPositiveClick: () => deleteOrganization(row),
            },
            {
              trigger: () =>
                h(
                  NButton,
                  { size: 'small', secondary: true, type: 'error' },
                  { default: () => '删除' },
                ),
              default: () =>
                `删除“${row.name}”将同时移除成员关系和全部项目、文档授权，确认继续？`,
            },
          ),
        ],
      }),
  },
]

onMounted(loadAll)

async function loadAll() {
  loading.value = true
  try {
    const [organizationData, userData] = await Promise.all([
      api<Organization[]>('/api/admin/organizations'),
      api<User[]>('/api/users'),
    ])
    organizations.value = organizationData
    users.value = userData
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载组织机构失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingOrganizationID.value = ''
  form.value = { name: '', userIds: [] }
  showEditor.value = true
}

function openEditor(organization: Organization) {
  editingOrganizationID.value = organization.id
  form.value = {
    name: organization.name,
    userIds: organization.members.map((member) => member.id),
  }
  showEditor.value = true
}

async function saveOrganization() {
  const name = form.value.name.trim()
  if (!name) {
    message.warning('请填写组织机构名称')
    return
  }
  saving.value = true
  try {
    const path = editingOrganizationID.value
      ? `/api/admin/organizations/${editingOrganizationID.value}`
      : '/api/admin/organizations'
    await api<Organization>(path, {
      method: editingOrganizationID.value ? 'PUT' : 'POST',
      body: JSON.stringify({
        name,
        userIds: form.value.userIds,
      }),
    })
    message.success(editingOrganizationID.value ? '组织机构已更新' : '组织机构已创建')
    showEditor.value = false
    await loadAll()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '保存组织机构失败')
  } finally {
    saving.value = false
  }
}

async function deleteOrganization(organization: Organization) {
  try {
    await api<void>(`/api/admin/organizations/${organization.id}`, {
      method: 'DELETE',
    })
    message.success(`组织机构“${organization.name}”已删除`)
    await loadAll()
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '删除组织机构失败')
  }
}
</script>

<template>
  <NCard class="admin-card" :bordered="false">
    <template #header>
      <div class="admin-card-heading">
        <strong>组织机构</strong>
        <span>维护扁平组织机构及成员，成员实时获得组织级读取授权</span>
      </div>
    </template>
    <template #header-extra>
      <NSpace align="center">
        <span class="admin-card-count">共 {{ organizations.length }} 个组织</span>
        <NButton type="primary" @click="openCreate">新建组织</NButton>
      </NSpace>
    </template>

    <p class="manage-section-hint organization-page-hint">
      一个用户可加入多个组织。用户与组织的归属也可以在“用户管理”中按用户设置。
      删除组织会同时移除其成员关系及该组织的全部项目、文档授权。
    </p>
    <NDataTable
      :columns="columns"
      :data="organizations"
      :loading="loading"
      :bordered="false"
    />

    <NModal
      :show="showEditor"
      :mask-closable="!saving"
      @update:show="showEditor = $event"
    >
      <NCard
        class="modal-card access-card"
        :title="editingOrganizationID ? '编辑组织机构' : '新建组织机构'"
        :bordered="false"
      >
        <NForm label-placement="top">
          <NFormItem label="组织机构名称">
            <NInput
              v-model:value="form.name"
              maxlength="100"
              show-count
              placeholder="例如：基础设施研发部"
              @keyup.enter="saveOrganization"
            />
          </NFormItem>
          <NFormItem label="组织成员">
            <NSelect
              v-model:value="form.userIds"
              to="body"
              multiple
              filterable
              clearable
              :options="userOptions"
              placeholder="选择加入该组织的用户"
            />
          </NFormItem>
        </NForm>
        <NSpace justify="end" class="modal-section-actions">
          <NButton :disabled="saving" @click="showEditor = false">取消</NButton>
          <NButton type="primary" :loading="saving" @click="saveOrganization">
            保存
          </NButton>
        </NSpace>
      </NCard>
    </NModal>
  </NCard>
</template>
