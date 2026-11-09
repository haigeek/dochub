<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NDynamicTags,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSwitch,
  NSpace,
  useMessage,
} from 'naive-ui'
import { api } from '@/api'
import type { Project } from '@/types'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  'update:show': [value: boolean]
  saved: [project: Project]
}>()

const message = useMessage()
const loading = ref(false)
const error = ref('')
const form = reactive({
  name: '',
  repoUrl: '',
  docRoots: ['docs'],
  branches: ['main'],
  isPublic: false,
})

watch(
  () => props.show,
  (show) => {
    if (!show) return
    error.value = ''
    form.name = ''
    form.repoUrl = ''
    form.docRoots = ['docs']
    form.branches = ['main']
    form.isPublic = false
  },
)

async function submit() {
  error.value = ''
  if (!form.name.trim() || !form.repoUrl.trim() || form.docRoots.length === 0 || form.branches.length === 0) {
    error.value = '请填写项目名称、SSH 仓库地址、至少一个文档目录和分支'
    return
  }
  loading.value = true
  try {
    const project = await api<Project>('/api/projects', {
      method: 'POST',
      body: JSON.stringify({
        name: form.name.trim(),
        repoUrl: form.repoUrl.trim(),
        docRoots: form.docRoots.map((value) => value.trim()).filter(Boolean),
        branches: form.branches,
        isPublic: form.isPublic,
      }),
    })
    message.success('项目已创建，正在同步源码')
    emit('update:show', false)
    emit('saved', project)
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '创建项目失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <NModal
    :show="props.show"
    :mask-closable="!loading"
    @update:show="emit('update:show', $event)"
  >
    <NCard class="modal-card project-form-card" :bordered="false">
      <div class="modal-title">
        <div>
          <span class="eyebrow">NEW PROJECT</span>
          <h2>接入源码仓库</h2>
          <p>系统会使用服务器 SSH 凭据克隆仓库并建立文档快照。</p>
        </div>
      </div>
      <NAlert v-if="error" type="error" :show-icon="false" class="modal-alert">{{ error }}</NAlert>
      <NForm label-placement="top" @submit.prevent="submit">
        <NFormItem label="项目名称" required>
          <NInput v-model:value="form.name" placeholder="例如：服务端研发手册" />
        </NFormItem>
        <NFormItem label="可见范围">
          <NSpace align="center">
            <NSwitch v-model:value="form.isPublic" aria-label="公开项目" />
            <span>{{ form.isPublic ? '公开 · 所有已登录成员可读' : '私有 · 仅授权成员可读' }}</span>
          </NSpace>
        </NFormItem>
        <NFormItem label="SSH 仓库地址" required>
          <NInput
            v-model:value="form.repoUrl"
            placeholder="git@git.example.com:team/project.git"
          />
        </NFormItem>
        <div class="form-grid">
          <NFormItem label="文档目录" required>
            <NDynamicTags v-model:value="form.docRoots" :max="10" placeholder="输入 docs 后回车" />
            <span class="form-hint">支持多个目录，例如 docs、design、api-guide</span>
          </NFormItem>
          <NFormItem label="跟踪分支" required>
            <NDynamicTags v-model:value="form.branches" :max="30" />
          </NFormItem>
        </div>
        <div class="modal-actions">
          <NSpace>
            <NButton :disabled="loading" @click="emit('update:show', false)">取消</NButton>
            <NButton type="primary" attr-type="submit" :loading="loading">创建并同步</NButton>
          </NSpace>
        </div>
      </NForm>
    </NCard>
  </NModal>
</template>
