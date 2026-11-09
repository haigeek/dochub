<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NModal,
  NSelect,
  NSpace,
  useMessage,
} from 'naive-ui'
import { api } from '@/api'
import type {
  OrganizationSummary,
  Project,
  ProjectAccess,
  UserSummary,
} from '@/types'

const props = defineProps<{
  show: boolean
  project: Project | null
  documentPath: string
}>()
const emit = defineEmits<{
  'update:show': [value: boolean]
}>()

const message = useMessage()
const loading = ref(false)
const saving = ref(false)
const users = ref<UserSummary[]>([])
const organizations = ref<OrganizationSummary[]>([])
const selectedUserIds = ref<string[]>([])
const selectedOrganizationIds = ref<string[]>([])

const userOptions = computed(() =>
  users.value.map((user) => ({
    label: `${user.username}${user.role === 'admin' ? '（管理员）' : ''}`,
    value: user.id,
  })),
)
const organizationOptions = computed(() =>
  organizations.value.map((organization) => ({
    label: organization.name,
    value: organization.id,
  })),
)

watch(
  () => [props.show, props.project?.id, props.documentPath] as const,
  async ([show]) => {
    if (!show) return
    await loadAccess()
  },
)

async function loadAccess() {
  if (!props.project || !props.documentPath) return
  loading.value = true
  try {
    const [directory, organizationDirectory, access] = await Promise.all([
      api<UserSummary[]>('/api/users/directory'),
      api<OrganizationSummary[]>('/api/organizations/directory'),
      api<ProjectAccess>(`/api/projects/${props.project.id}/access`),
    ])
    users.value = directory
    organizations.value = organizationDirectory
    const documentAccess = access.documents.find(
      (document) => document.path === props.documentPath,
    )
    selectedUserIds.value = documentAccess?.users.map((user) => user.id) ?? []
    selectedOrganizationIds.value =
      documentAccess?.organizations.map((organization) => organization.id) ?? []
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载文档权限失败')
  } finally {
    loading.value = false
  }
}

async function saveAccess() {
  if (!props.project || !props.documentPath) return
  saving.value = true
  try {
    await api<ProjectAccess>(`/api/projects/${props.project.id}/access/document`, {
      method: 'PUT',
      body: JSON.stringify({
        path: props.documentPath,
        userIds: selectedUserIds.value,
        organizationIds: selectedOrganizationIds.value,
      }),
    })
    message.success('文档权限已更新')
    emit('update:show', false)
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '保存文档权限失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <NModal :show="props.show" @update:show="emit('update:show', $event)">
    <NCard class="modal-card access-card" title="文档读取权限" :bordered="false">
      <NAlert type="info" :show-icon="false" class="access-hint">
        授权按文档路径对全部分支生效，组织成员与个人授权取并集，并允许读取正文引用的图片和附件。
        创建人和管理员无需额外授权。
      </NAlert>
      <div class="access-document-path">{{ props.documentPath }}</div>
      <NForm label-placement="top">
        <NFormItem label="可读取成员">
          <NSelect
            v-model:value="selectedUserIds"
            to="body"
            multiple
            filterable
            clearable
            :options="userOptions"
            :loading="loading"
            placeholder="选择可以读取该文档的成员"
          />
        </NFormItem>
        <NFormItem label="可读取组织机构">
          <NSelect
            v-model:value="selectedOrganizationIds"
            to="body"
            multiple
            filterable
            clearable
            :options="organizationOptions"
            :loading="loading"
            placeholder="选择可以读取该文档的组织机构"
          />
        </NFormItem>
      </NForm>
      <NSpace justify="end" class="modal-section-actions">
        <NButton @click="emit('update:show', false)">取消</NButton>
        <NButton type="primary" :loading="saving" :disabled="loading" @click="saveAccess">
          保存权限
        </NButton>
      </NSpace>
    </NCard>
  </NModal>
</template>
