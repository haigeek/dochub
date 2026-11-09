<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDynamicTags,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NPopconfirm,
  NSelect,
  NSpace,
  NSwitch,
  NTag,
  useMessage,
} from 'naive-ui'
import { AddOutline, TrashOutline } from '@vicons/ionicons5'
import { api } from '@/api'
import type {
  Branch,
  OrganizationSummary,
  Project,
  ProjectAccess,
  UserSummary,
} from '@/types'

const props = defineProps<{
  show: boolean
  project: Project | null
}>()
const emit = defineEmits<{
  'update:show': [value: boolean]
  changed: []
  deleted: []
}>()

const message = useMessage()
const saving = ref(false)
const adding = ref(false)
const accessLoading = ref(false)
const accessSaving = ref(false)
const branchToAdd = ref('')
const users = ref<UserSummary[]>([])
const organizations = ref<OrganizationSummary[]>([])
const projectUserIds = ref<string[]>([])
const projectOrganizationIds = ref<string[]>([])
const form = reactive({
  name: '',
  repoUrl: '',
  docRoots: [] as string[],
  isPublic: false,
})

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
  () => [props.show, props.project] as const,
  async ([show, project]) => {
    if (!show || !project) return
    form.name = project.name
    form.repoUrl = project.repoUrl
    form.docRoots = [...project.docRoots]
    form.isPublic = project.isPublic
    branchToAdd.value = ''
    await loadAccess(project.id)
  },
  { immediate: true },
)

async function loadAccess(projectID: string) {
  accessLoading.value = true
  try {
    const [directory, organizationDirectory, access] = await Promise.all([
      api<UserSummary[]>('/api/users/directory'),
      api<OrganizationSummary[]>('/api/organizations/directory'),
      api<ProjectAccess>(`/api/projects/${projectID}/access`),
    ])
    users.value = directory
    organizations.value = organizationDirectory
    projectUserIds.value = access.projectUsers.map((user) => user.id)
    projectOrganizationIds.value = access.projectOrganizations.map(
      (organization) => organization.id,
    )
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载项目权限失败')
  } finally {
    accessLoading.value = false
  }
}

function statusType(status: Branch['status']): 'success' | 'warning' | 'error' | 'default' {
  if (status === 'succeeded') return 'success'
  if (status === 'running') return 'warning'
  if (status === 'failed') return 'error'
  return 'default'
}

async function saveProject() {
  if (!props.project) return
  if (form.docRoots.length === 0) {
    message.error('请至少配置一个文档目录')
    return
  }
  saving.value = true
  try {
    await api<Project>(`/api/projects/${props.project.id}`, {
      method: 'PATCH',
      body: JSON.stringify(form),
    })
    message.success('项目配置已更新')
    emit('changed')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '更新项目失败')
  } finally {
    saving.value = false
  }
}

async function addBranch() {
  if (!props.project || !branchToAdd.value.trim()) return
  adding.value = true
  try {
    await api(`/api/projects/${props.project.id}/branches`, {
      method: 'POST',
      body: JSON.stringify({ name: branchToAdd.value.trim() }),
    })
    message.success('分支已添加，正在同步')
    branchToAdd.value = ''
    emit('changed')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '添加分支失败')
  } finally {
    adding.value = false
  }
}

async function removeBranch(branch: string) {
  if (!props.project) return
  try {
    await api<void>(
      `/api/projects/${props.project.id}/branches?branch=${encodeURIComponent(branch)}`,
      { method: 'DELETE' },
    )
    message.success('分支已移除')
    emit('changed')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '移除分支失败')
  }
}

async function deleteProject() {
  if (!props.project) return
  try {
    await api<void>(`/api/projects/${props.project.id}`, { method: 'DELETE' })
    message.success('项目已删除')
    emit('update:show', false)
    emit('deleted')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '删除项目失败')
  }
}

async function saveProjectAccess() {
  if (!props.project) return
  accessSaving.value = true
  try {
    await api<ProjectAccess>(`/api/projects/${props.project.id}/access/project`, {
      method: 'PUT',
      body: JSON.stringify({
        userIds: projectUserIds.value,
        organizationIds: projectOrganizationIds.value,
      }),
    })
    message.success('项目读取权限已更新')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '保存项目权限失败')
  } finally {
    accessSaving.value = false
  }
}
</script>

<template>
  <NModal :show="props.show" @update:show="emit('update:show', $event)">
    <NCard class="modal-card manage-card" title="项目管理" :bordered="false">
      <div v-if="props.project">
        <NForm label-placement="top">
          <NFormItem label="项目名称">
            <NInput v-model:value="form.name" />
          </NFormItem>
          <NFormItem label="可见范围">
            <NSpace align="center">
              <NSwitch v-model:value="form.isPublic" aria-label="公开项目" />
              <span>{{ form.isPublic ? '公开 · 所有已登录成员可读' : '私有 · 仅授权成员可读' }}</span>
            </NSpace>
          </NFormItem>
          <NFormItem label="SSH 仓库地址">
            <NInput v-model:value="form.repoUrl" />
          </NFormItem>
          <NFormItem label="文档目录">
            <NDynamicTags v-model:value="form.docRoots" :max="10" placeholder="输入目录后回车" />
            <span class="form-hint">支持多个目录，不能重复或互相包含</span>
          </NFormItem>
          <NAlert type="info" :show-icon="false">
            修改仓库地址或文档目录会清除现有快照，并自动重新同步所有已配置分支。
          </NAlert>
          <NSpace justify="end" class="modal-section-actions">
            <NButton type="primary" :loading="saving" @click="saveProject">保存配置</NButton>
          </NSpace>
        </NForm>

        <section class="manage-section">
          <h3>项目级读取权限</h3>
          <p class="manage-section-hint">
            获得项目级权限的成员可以读取该项目全部分支的全部文档；组织成员授权与个人授权取并集。创建人和管理员始终拥有完整权限。
            公开项目对所有已登录成员开放读取，以下授权在切回私有后继续生效。
          </p>
          <NSpace vertical>
            <NSelect
              v-model:value="projectUserIds"
              to="body"
              multiple
              filterable
              clearable
              :options="userOptions"
              :loading="accessLoading"
              placeholder="选择可以读取整个项目的成员"
            />
            <NSelect
              v-model:value="projectOrganizationIds"
              to="body"
              multiple
              filterable
              clearable
              :options="organizationOptions"
              :loading="accessLoading"
              placeholder="选择可以读取整个项目的组织机构"
            />
            <NSpace justify="end">
              <NButton
                type="primary"
                secondary
                :loading="accessSaving"
                :disabled="accessLoading"
                @click="saveProjectAccess"
              >
                保存项目权限
              </NButton>
            </NSpace>
          </NSpace>
        </section>

        <section class="manage-section">
          <h3>跟踪分支</h3>
          <div class="branch-manage-list">
            <div v-for="branch in props.project.branches" :key="branch.id" class="branch-manage-row">
              <div>
                <strong>{{ branch.name }}</strong>
                <small>
                  {{ branch.commitSha ? branch.commitSha.slice(0, 8) : '等待首次同步' }}
                </small>
              </div>
              <div class="branch-manage-actions">
                <NTag :type="statusType(branch.status)" size="small">
                  {{
                    branch.status === 'succeeded'
                      ? '已同步'
                      : branch.status === 'running'
                        ? '同步中'
                        : branch.status === 'failed'
                          ? '同步失败'
                          : '待同步'
                  }}
                </NTag>
                <NPopconfirm
                  v-if="props.project.branches.length > 1"
                  positive-text="移除"
                  negative-text="取消"
                  @positive-click="removeBranch(branch.name)"
                >
                  <template #trigger>
                    <NButton quaternary circle size="small">
                      <template #icon><TrashOutline /></template>
                    </NButton>
                  </template>
                  仅移除本地快照，不影响远端分支。
                </NPopconfirm>
              </div>
            </div>
          </div>
          <NSpace class="add-branch-row">
            <NInput
              v-model:value="branchToAdd"
              placeholder="输入新分支，例如 release/1.0"
              @keyup.enter="addBranch"
            />
            <NButton :loading="adding" type="primary" secondary @click="addBranch">
              <template #icon><AddOutline /></template>
              添加分支
            </NButton>
          </NSpace>
        </section>

        <section class="danger-zone">
          <div>
            <strong>删除项目</strong>
            <p>删除项目数据、本地镜像和所有文档快照。远端仓库不受影响。</p>
          </div>
          <NPopconfirm
            positive-text="永久删除"
            negative-text="取消"
            type="warning"
            @positive-click="deleteProject"
          >
            <template #trigger>
              <NButton type="error" secondary>删除项目</NButton>
            </template>
            确认删除该项目及全部本地文档数据？
          </NPopconfirm>
        </section>
      </div>
    </NCard>
  </NModal>
</template>
