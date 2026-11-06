<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  NButton,
  NButtonGroup,
  NEmpty,
  NInput,
  NSpin,
  NTag,
  useMessage,
} from 'naive-ui'
import {
  AddOutline,
  ChevronForwardOutline,
  GitBranchOutline,
  GridOutline,
  ListOutline,
  SearchOutline,
  TimeOutline,
} from '@vicons/ionicons5'
import AppHeader from '@/components/AppHeader.vue'
import ProjectFormModal from '@/components/ProjectFormModal.vue'
import UsersModal from '@/components/UsersModal.vue'
import { api } from '@/api'
import type { Project } from '@/types'

const router = useRouter()
const message = useMessage()
const projects = ref<Project[]>([])
const loading = ref(true)
const showCreate = ref(false)
const showUsers = ref(false)
const viewMode = ref<'list' | 'grid'>('list')
const searchQuery = ref('')

const filteredProjects = computed(() => {
  const keyword = normalize(searchQuery.value)
  if (!keyword) return projects.value
  return projects.value.filter((project) => {
    const searchable = [
      project.name,
      project.slug,
      project.repoUrl,
      project.ownerName,
      statusLabel(project),
      ...project.branches.map((branch) => branch.name),
    ]
      .map(normalize)
      .join(' ')
    return searchable.includes(keyword)
  })
})

onMounted(loadProjects)

async function loadProjects() {
  loading.value = true
  try {
    projects.value = await api<Project[]>('/api/projects')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载项目失败')
  } finally {
    loading.value = false
  }
}

function projectSaved(project: Project) {
  void router.push({ name: 'project', params: { slug: project.slug } })
}

function statusLabel(project: Project) {
  if (project.branches.some((branch) => branch.status === 'running')) return '同步中'
  if (project.branches.some((branch) => branch.status === 'failed')) return '部分异常'
  if (project.branches.every((branch) => branch.status === 'succeeded')) return '已就绪'
  return '待同步'
}

function statusType(project: Project): 'success' | 'warning' | 'error' | 'default' {
  const label = statusLabel(project)
  if (label === '已就绪') return 'success'
  if (label === '同步中') return 'warning'
  if (label === '部分异常') return 'error'
  return 'default'
}

function normalize(value: string) {
  return value.normalize('NFKC').trim().toLocaleLowerCase('zh-CN')
}

function formatProjectTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '时间未知'
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}
</script>

<template>
  <div class="page-shell">
    <AppHeader @users="showUsers = true" />
    <main class="projects-main">
      <section class="projects-hero">
        <div>
          <span class="eyebrow">KNOWLEDGE AT SOURCE</span>
          <h1>项目文档</h1>
          <p>把散落在源码目录里的 Markdown 文档，整理成团队随时可读的知识入口。</p>
        </div>
        <div class="projects-hero-actions">
          <NButton type="primary" @click="showCreate = true">
            <template #icon><AddOutline /></template>
            接入项目
          </NButton>
        </div>
      </section>

      <div class="projects-filter">
        <div class="projects-filter-main">
          <NInput
            v-model:value="searchQuery"
            class="project-search"
            clearable
            placeholder="搜索项目名称、仓库地址或分支"
          >
            <template #prefix><SearchOutline /></template>
          </NInput>
          <span class="project-filter-count">
            {{ searchQuery.trim() ? `${filteredProjects.length} 个匹配项目` : `共 ${projects.length} 个项目` }}
          </span>
        </div>
        <NButtonGroup class="view-switch">
          <NButton
            :type="viewMode === 'list' ? 'primary' : 'default'"
            aria-label="列表视图"
            @click="viewMode = 'list'"
          >
            <template #icon><ListOutline /></template>
            列表
          </NButton>
          <NButton
            :type="viewMode === 'grid' ? 'primary' : 'default'"
            aria-label="卡片视图"
            @click="viewMode = 'grid'"
          >
            <template #icon><GridOutline /></template>
            卡片
          </NButton>
        </NButtonGroup>
      </div>

      <NSpin :show="loading">
        <div v-if="filteredProjects.length && viewMode === 'list'" class="project-list">
          <div class="project-list-header">
            <span>项目</span>
            <span>分支</span>
            <span>状态</span>
            <span>最近更新</span>
            <span />
          </div>
          <button
            v-for="project in filteredProjects"
            :key="project.id"
            class="project-list-row"
            @click="router.push({ name: 'project', params: { slug: project.slug } })"
          >
            <div class="project-list-main">
              <span class="project-icon">{{ project.name.slice(0, 1).toUpperCase() }}</span>
              <span class="project-list-copy">
                <strong>{{ project.name }}</strong>
                <small>{{ project.repoUrl }}</small>
              </span>
            </div>
            <div class="project-list-branches">
              <span v-for="branch in project.branches.slice(0, 3)" :key="branch.id">
                {{ branch.name }}
              </span>
              <span v-if="project.branches.length > 3">+{{ project.branches.length - 3 }}</span>
            </div>
            <NTag :type="statusType(project)" size="small">{{ statusLabel(project) }}</NTag>
            <span class="project-list-date" :title="project.updatedAt">
              {{ formatProjectTime(project.updatedAt) }}
            </span>
            <ChevronForwardOutline class="project-list-arrow" />
          </button>
        </div>
        <div v-else-if="filteredProjects.length" class="project-grid">
          <button
            v-for="project in filteredProjects"
            :key="project.id"
            class="project-card"
            @click="router.push({ name: 'project', params: { slug: project.slug } })"
          >
            <div class="project-card-topline">
              <span class="project-icon">{{ project.name.slice(0, 1).toUpperCase() }}</span>
              <NTag :type="statusType(project)" size="small">{{ statusLabel(project) }}</NTag>
            </div>
            <h2>{{ project.name }}</h2>
            <p class="project-path">{{ project.repoUrl }}</p>
            <div class="project-meta">
              <span><GitBranchOutline /> {{ project.branches.length }} 个分支</span>
              <span><TimeOutline /> {{ new Date(project.updatedAt).toLocaleDateString('zh-CN') }}</span>
            </div>
            <div class="project-branches">
              <span v-for="branch in project.branches.slice(0, 4)" :key="branch.id">{{ branch.name }}</span>
              <span v-if="project.branches.length > 4">+{{ project.branches.length - 4 }}</span>
            </div>
          </button>
        </div>
        <div v-else-if="!loading" class="empty-state">
          <NEmpty
            :description="projects.length ? '没有匹配的项目' : '还没有接入任何项目'"
          >
            <template #extra>
              <NButton v-if="projects.length" @click="searchQuery = ''">清除搜索</NButton>
              <NButton v-else type="primary" @click="showCreate = true">接入第一个仓库</NButton>
            </template>
          </NEmpty>
        </div>
      </NSpin>
    </main>

    <ProjectFormModal v-model:show="showCreate" @saved="projectSaved" />
    <UsersModal v-model:show="showUsers" />
  </div>
</template>
