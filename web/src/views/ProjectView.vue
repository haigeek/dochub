<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  NAlert,
  NButton,
  NDrawer,
  NDrawerContent,
  NEmpty,
  NInput,
  NModal,
  NSelect,
  NSpin,
  NTag,
  useMessage,
} from 'naive-ui'
import {
  ChevronBackOutline,
  DownloadOutline,
  EllipsisHorizontal,
  GitBranchOutline,
  ListOutline,
  MenuOutline,
  RefreshOutline,
  SearchOutline,
  TimeOutline,
} from '@vicons/ionicons5'
import AppHeader from '@/components/AppHeader.vue'
import DocSidebarNode from '@/components/DocSidebarNode.vue'
import MarkdownArticle from '@/components/MarkdownArticle.vue'
import ProjectManageModal from '@/components/ProjectManageModal.vue'
import UsersModal from '@/components/UsersModal.vue'
import { api, downloadFile } from '@/api'
import type {
  Branch,
  DocumentContent,
  DocumentItem,
  Heading,
  Project,
  SearchResult,
  SidebarTreeNode,
  SyncJob,
} from '@/types'

const route = useRoute()
const router = useRouter()
const message = useMessage()
const showManage = ref(false)
const showUsers = ref(false)
const showSearch = ref(false)
const showMobileTree = ref(false)
const showMobileTOC = ref(false)
const loading = ref(true)
const documentLoading = ref(false)
const syncing = ref(false)
const downloading = ref(false)
const project = ref<Project | null>(null)
const branchName = ref('')
const documents = ref<DocumentItem[]>([])
const document = ref<DocumentContent | null>(null)
const headings = ref<Heading[]>([])
const activeHeading = ref('')
const expandedKeys = ref<string[]>([])
const searchQuery = ref('')
const searchResults = ref<SearchResult[]>([])
const searching = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | undefined
let statusTimer: ReturnType<typeof setTimeout> | undefined
let unmounted = false

const currentBranch = computed(() =>
  project.value?.branches.find((branch) => branch.name === branchName.value) ?? null,
)
const lastSyncLabel = computed(() => formatSyncTime(currentBranch.value?.lastSyncedAt ?? null))
const branchOptions = computed(() =>
  project.value?.branches.map((branch) => ({
    label: `${branch.name}${branch.status === 'running' ? ' · 同步中' : ''}`,
    value: branch.name,
  })) ?? [],
)
const docRoots = computed(() => project.value?.docRoots?.length ? project.value.docRoots : ['docs'])
const sidebarRoots = computed<SidebarTreeNode[]>(() => {
  const roots = docRoots.value
  const hasRepositoryPaths = documents.value.some((item) =>
    roots.some((root) => item.path.startsWith(`${root}/`)),
  )
  if (roots.length === 1 && !hasRepositoryPaths) {
    return [{
      key: `dir:${roots[0]}`,
      label: roots[0],
      kind: 'folder',
      children: buildSidebarTree(documents.value),
    }]
  }
  return roots.map((root) => ({
    key: `dir:${root}`,
    label: root,
    kind: 'folder',
    children: buildSidebarTree(
      documents.value.filter((item) => item.path.startsWith(`${root}/`)),
      root,
    ),
  }))
})
onMounted(async () => {
  await loadWorkspace()
  beginInitialStatusPolling()
})

onBeforeUnmount(() => {
  unmounted = true
  clearTimeout(searchTimer)
  clearTimeout(statusTimer)
})

watch(
  () => route.params.slug,
  async () => {
    await loadWorkspace()
    beginInitialStatusPolling()
  },
)

watch(
  () => [route.query.branch, route.query.path] as const,
  async () => {
    if (!project.value) return
    const queryBranch = typeof route.query.branch === 'string' ? route.query.branch : ''
    const queryPath = typeof route.query.path === 'string' ? route.query.path : ''
    if (queryBranch === branchName.value && queryPath === document.value?.path) return
    await loadWorkspace(queryBranch, queryPath)
  },
)

watch(searchQuery, (value) => {
  clearTimeout(searchTimer)
  if (!value.trim() || !project.value || !branchName.value) {
    searchResults.value = []
    return
  }
  searchTimer = setTimeout(searchDocuments, 250)
})

async function loadWorkspace(overrideBranch?: string, preferredPath?: string) {
  const requestIdentifier = String(route.params.slug ?? '')
  if (!requestIdentifier) return
  loading.value = true
  try {
    const loadedProject = await api<Project>(`/api/projects/${encodeURIComponent(requestIdentifier)}`)
    if (unmounted || String(route.params.slug ?? '') !== requestIdentifier) return
    project.value = loadedProject
    const queryBranch = overrideBranch ??
      (typeof route.query.branch === 'string' ? route.query.branch : '')
    const targetBranch = loadedProject.branches.some((branch) => branch.name === queryBranch)
      ? queryBranch
      : loadedProject.branches[0]?.name ?? ''
    branchName.value = targetBranch
    if (!targetBranch) {
      documents.value = []
      document.value = null
      return
    }
    await loadTreeAndDocument(
      targetBranch,
      preferredPath ??
        (typeof route.query.path === 'string' ? route.query.path : undefined),
    )
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '加载项目失败')
  } finally {
    loading.value = false
  }
}

async function loadTreeAndDocument(branch: string, preferredPath?: string) {
  const loadedTree = await api<{ docRoots: string[]; documents: DocumentItem[] }>(
    `/api/projects/${project.value!.id}/tree?branch=${encodeURIComponent(branch)}`,
  )
  documents.value = loadedTree.documents
  const desiredPath =
    preferredPath && documents.value.some((item) => item.path === preferredPath)
      ? preferredPath
      : defaultDocumentPath(documents.value)
  // 所有配置的文档根目录默认展开，便于直接看到 Markdown 文件。
  expandedKeys.value = [
    ...docRoots.value.map((root) => `dir:${root}`),
    ...documentFolders(documents.value),
  ]
  if (!desiredPath) {
    document.value = null
    headings.value = []
    await replaceQuery(branch, '')
    return
  }
  await loadDocument(desiredPath, branch)
  await replaceQuery(branch, desiredPath)
}

async function loadDocument(docPath: string, branch = branchName.value) {
  if (!project.value || !branch) return
  documentLoading.value = true
  activeHeading.value = ''
  try {
    document.value = await api<DocumentContent>(
      `/api/projects/${project.value.id}/doc?branch=${encodeURIComponent(branch)}&path=${encodeURIComponent(docPath)}`,
    )
  } catch (reason) {
    document.value = null
    message.error(reason instanceof Error ? reason.message : '加载文档失败')
  } finally {
    documentLoading.value = false
  }
}

async function replaceQuery(branch: string, docPath: string) {
  const query: Record<string, string> = { branch }
  if (docPath) query.path = docPath
  await router.replace({ name: 'project', params: route.params, query })
}

async function changeBranch(value: string) {
  if (!value || value === branchName.value) return
  const previousPath = document.value?.path
  branchName.value = value
  document.value = null
  loading.value = true
  try {
    await loadTreeAndDocument(value, previousPath)
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '切换分支失败')
  } finally {
    loading.value = false
  }
}

async function openDocument(docPath: string, anchor = '') {
  if (!docPath || docPath === document.value?.path) return
  showMobileTree.value = false
  await loadDocument(docPath)
  await replaceQuery(branchName.value, docPath)
  if (anchor) {
    await nextTick()
    window.document.getElementById(safeDecodeAnchor(anchor))?.scrollIntoView({
      behavior: 'smooth',
      block: 'start',
    })
  } else {
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }
}

function toggleSidebarFolder(key: string) {
  if (expandedKeys.value.includes(key)) {
    expandedKeys.value = expandedKeys.value.filter((item) => item !== key)
    return
  }
  expandedKeys.value = [...expandedKeys.value, key]
}

async function syncCurrentBranch() {
  if (!project.value || !currentBranch.value || syncing.value) return
  syncing.value = true
  try {
    const job = await api<SyncJob>(`/api/projects/${project.value.id}/sync`, {
      method: 'POST',
      body: JSON.stringify({ branch: branchName.value }),
    })
    const result = await waitForJob(job.id)
    if (result.status === 'failed') {
      message.error(result.error || '同步失败，继续展示上一份文档快照')
    } else {
      message.success('文档已同步到最新提交')
      await loadWorkspace(branchName.value, document.value?.path)
    }
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '触发同步失败')
  } finally {
    syncing.value = false
  }
}

async function waitForJob(jobID: string): Promise<SyncJob> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    const job = await api<SyncJob>(`/api/jobs/${jobID}`)
    if (job.status === 'succeeded' || job.status === 'failed') return job
    await delay(1000)
  }
  throw new Error('同步耗时较长，请稍后在项目中查看状态')
}

function beginInitialStatusPolling() {
  clearTimeout(statusTimer)
  let attempts = 0
  const tick = async () => {
    if (unmounted || !project.value) return
    const needsSync = project.value.branches.some(
      (branch) => branch.status === 'never' || branch.status === 'running',
    )
    if (!needsSync || attempts >= 60) return
    attempts += 1
    try {
      project.value = await api<Project>(`/api/projects/${project.value.id}`)
      const stillRunning = project.value.branches.some(
        (branch) => branch.status === 'never' || branch.status === 'running',
      )
      if (!stillRunning) {
        await loadWorkspace(branchName.value, document.value?.path)
        return
      }
    } catch {
      // 状态轮询失败时继续保留当前页面，手动刷新仍可恢复。
    }
    statusTimer = setTimeout(tick, 1800)
  }
  statusTimer = setTimeout(tick, 900)
}

async function searchDocuments() {
  if (!project.value || !searchQuery.value.trim()) return
  searching.value = true
  try {
    searchResults.value = await api<SearchResult[]>(
      `/api/projects/${project.value.id}/search?branch=${encodeURIComponent(branchName.value)}&q=${encodeURIComponent(searchQuery.value.trim())}`,
    )
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '搜索失败')
  } finally {
    searching.value = false
  }
}

async function downloadCurrentDocument() {
  if (!project.value || !document.value || !branchName.value) return
  downloading.value = true
  try {
    const filename = document.value.path.split('/').pop() || 'document.md'
    await downloadFile(
      `/api/projects/${project.value.id}/doc/download?branch=${encodeURIComponent(branchName.value)}&path=${encodeURIComponent(document.value.path)}`,
      filename,
    )
    message.success('文档已下载')
  } catch (reason) {
    message.error(reason instanceof Error ? reason.message : '下载文档失败')
  } finally {
    downloading.value = false
  }
}

function openSearch() {
  showSearch.value = true
  searchQuery.value = ''
  searchResults.value = []
}

async function selectSearchResult(result: SearchResult) {
  showSearch.value = false
  await openDocument(result.path)
}

function scrollToHeading(id: string) {
  activeHeading.value = id
  window.document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  showMobileTOC.value = false
}

function assetURL(path: string) {
  if (!project.value) return ''
  const encoded = path.split('/').map(encodeURIComponent).join('/')
  return `/api/projects/${project.value.id}/assets/${encoded}?branch=${encodeURIComponent(branchName.value)}`
}

function defaultDocumentPath(items: DocumentItem[]) {
  return (
    items.find((item) => /(^|\/)readme\.md$/i.test(item.path))?.path ??
    items.find((item) => /(^|\/)index\.md$/i.test(item.path))?.path ??
    items[0]?.path ??
    ''
  )
}

function buildSidebarTree(items: DocumentItem[], prefix = ''): SidebarTreeNode[] {
  const folders = new Map<string, DocumentItem[]>()
  const files: SidebarTreeNode[] = []
  for (const item of items) {
    const remaining = prefix ? item.path.slice(prefix.length + 1) : item.path
    const slash = remaining.indexOf('/')
    if (slash === -1) {
      files.push({
        key: item.path,
        label: item.title,
        kind: 'document',
      })
      continue
    }
    const folder = remaining.slice(0, slash)
    const list = folders.get(folder) ?? []
    list.push(item)
    folders.set(folder, list)
  }
  const folderNodes: SidebarTreeNode[] = [...folders.entries()]
    .sort(([left], [right]) => left.localeCompare(right, 'zh-CN'))
    .map(([folder, children]) => {
      const folderPath = prefix ? `${prefix}/${folder}` : folder
      return {
        key: `dir:${folderPath}`,
        label: folder,
        kind: 'folder',
        children: buildSidebarTree(children, folderPath),
      }
    })
  return [
    ...folderNodes,
    ...files.sort((left, right) => String(left.label).localeCompare(String(right.label), 'zh-CN')),
  ]
}

function documentFolders(items: DocumentItem[]) {
  const values = new Set<string>()
  for (const item of items) {
    const parts = item.path.split('/')
    parts.pop()
    let current = ''
    for (const part of parts) {
      current = current ? `${current}/${part}` : part
      values.add(`dir:${current}`)
    }
  }
  return [...values]
}

function branchStatusLabel(branch: Branch | null): '已同步' | '同步中' | '同步失败' | '待同步' {
  if (branch?.status === 'succeeded') return '已同步'
  if (branch?.status === 'running') return '同步中'
  if (branch?.status === 'failed') return '同步失败'
  return '待同步'
}

function branchStatusType(branch: Branch | null): 'success' | 'warning' | 'error' | 'default' {
  if (branch?.status === 'succeeded') return 'success'
  if (branch?.status === 'running') return 'warning'
  if (branch?.status === 'failed') return 'error'
  return 'default'
}

function formatSyncTime(value: string | null) {
  if (!value) return '尚未同步'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '时间未知'
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

async function onProjectChanged() {
  const path = document.value?.path
  await loadWorkspace(branchName.value, path)
  beginInitialStatusPolling()
}

async function onProjectDeleted() {
  await router.replace('/projects')
}

function openManage() {
  showManage.value = true
}

function delay(milliseconds: number) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

function safeDecodeAnchor(value: string) {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}
</script>

<template>
  <div class="docs-page">
    <AppHeader back-to-projects @users="showUsers = true">
      <template #project-nav>
        <div v-if="project" class="project-heading">
          <button class="back-icon" title="返回项目" @click="router.push('/projects')">
            <ChevronBackOutline />
          </button>
          <div class="project-breadcrumb">
            <span>项目</span>
            <b>/</b>
            <strong>{{ project.name }}</strong>
          </div>
        </div>
        <NSelect
          v-if="project"
          class="branch-select header-branch-select"
          :value="branchName"
          :options="branchOptions"
          @update:value="changeBranch"
        />
      </template>
      <template #project-actions>
        <div v-if="project" class="toolbar-actions">
          <NButton
            v-if="document"
            class="desktop-download"
            quaternary
            :loading="downloading"
            @click="downloadCurrentDocument"
          >
            <template #icon><DownloadOutline /></template>
            下载
          </NButton>
          <NButton class="search-button" quaternary @click="openSearch">
            <template #icon><SearchOutline /></template>
            搜索
            <span class="search-shortcut">⌘ K</span>
          </NButton>
          <NButton
            class="sync-button"
            type="primary"
            :loading="syncing"
            :disabled="currentBranch?.status === 'running'"
            @click="syncCurrentBranch"
          >
            <template #icon><RefreshOutline /></template>
            同步
          </NButton>
          <NButton quaternary circle title="项目管理" @click="openManage">
            <template #icon><EllipsisHorizontal /></template>
          </NButton>
        </div>
      </template>
    </AppHeader>

    <div v-if="project && currentBranch?.status === 'failed'" class="branch-alert">
      <NAlert type="error" :show-icon="false" closable>
        当前分支同步失败，正在展示上一份可用快照。{{ currentBranch.lastError }}
      </NAlert>
    </div>

    <main v-if="project" class="docs-workspace">
      <aside class="docs-sidebar desktop-sidebar">
        <nav class="docs-sidebar-nav" aria-label="文档目录">
          <div class="sidebar-heading">
            <span>文档目录</span>
            <NTag :type="branchStatusType(currentBranch)" size="small">
              {{ branchStatusLabel(currentBranch) }}
            </NTag>
          </div>
          <DocSidebarNode
            v-for="root in sidebarRoots"
            :key="root.key"
            class="docs-tree"
            :node="root"
            :selected-path="document?.path ?? ''"
            :expanded-keys="expandedKeys"
            @select="openDocument"
            @toggle="toggleSidebarFolder"
          />
        </nav>
        <div class="sidebar-footer">
          <span class="sidebar-meta">
            <GitBranchOutline />
            {{ currentBranch?.commitSha.slice(0, 8) || '尚无提交' }}
          </span>
          <span class="sidebar-meta" :title="currentBranch?.lastSyncedAt ?? ''">
            <TimeOutline />
            最后同步：{{ lastSyncLabel }}
          </span>
        </div>
      </aside>

      <section class="doc-center" :class="{ 'has-toc': headings.length > 0 }">
        <div class="mobile-doc-actions">
          <NButton secondary size="small" @click="showMobileTree = true">
            <template #icon><MenuOutline /></template>
            目录
          </NButton>
          <NButton v-if="headings.length" secondary size="small" @click="showMobileTOC = true">
            <template #icon><ListOutline /></template>
            本页目录
          </NButton>
          <NButton v-if="document" secondary size="small" :loading="downloading" @click="downloadCurrentDocument">
            <template #icon><DownloadOutline /></template>
            下载
          </NButton>
          <span class="mobile-sync-time">
            <TimeOutline />
            {{ lastSyncLabel }}
          </span>
        </div>
        <NSpin :show="loading || documentLoading">
          <div v-if="document" class="doc-paper">
            <MarkdownArticle
              :markdown="document.markdown"
              :doc-path="document.path"
              :resolve-asset="assetURL"
              @headings="headings = $event"
              @navigate="openDocument"
              @active-heading="activeHeading = $event"
            />
          </div>
          <div v-else-if="!loading" class="docs-empty">
            <NEmpty
              :description="
                currentBranch?.status === 'failed'
                  ? '当前分支暂无可用文档快照'
                  : '正在等待文档同步完成'
              "
            >
              <template #extra>
                <NButton
                  v-if="currentBranch?.status === 'failed'"
                  type="primary"
                  :loading="syncing"
                  @click="syncCurrentBranch"
                >
                  重新同步
                </NButton>
              </template>
            </NEmpty>
          </div>
        </NSpin>
      </section>

      <aside v-if="headings.length" class="docs-toc desktop-toc">
        <div class="toc-heading">本页目录</div>
        <nav class="toc-nav" aria-label="本页目录">
          <button
            v-for="heading in headings"
            :key="heading.id"
            :class="[
              'toc-item',
              `level-${heading.level}`,
              { active: activeHeading === heading.id },
            ]"
            @click="scrollToHeading(heading.id)"
          >
            {{ heading.text }}
          </button>
        </nav>
      </aside>
    </main>

    <div v-if="loading && !project" class="fullscreen-loading">
      <NSpin size="large" />
    </div>

    <NDrawer v-model:show="showMobileTree" placement="left" :width="320">
      <NDrawerContent title="文档目录" closable>
        <DocSidebarNode
          v-for="root in sidebarRoots"
          :key="root.key"
          class="docs-tree"
          :node="root"
          :selected-path="document?.path ?? ''"
          :expanded-keys="expandedKeys"
          @select="openDocument"
          @toggle="toggleSidebarFolder"
        />
      </NDrawerContent>
    </NDrawer>

    <NDrawer v-model:show="showMobileTOC" placement="right" :width="300">
      <NDrawerContent title="本页目录" closable>
        <nav class="toc-nav" aria-label="本页目录">
          <button
            v-for="heading in headings"
            :key="heading.id"
            :class="['toc-item', `level-${heading.level}`, { active: activeHeading === heading.id }]"
            @click="scrollToHeading(heading.id)"
          >
            {{ heading.text }}
          </button>
        </nav>
      </NDrawerContent>
    </NDrawer>

    <NModal v-model:show="showSearch" class="search-modal">
      <div class="search-panel">
        <div class="search-input-row">
          <SearchOutline />
          <NInput
            v-model:value="searchQuery"
            size="large"
            placeholder="搜索标题、路径或正文内容…"
            :loading="searching"
            autofocus
          />
          <span class="esc-hint">ESC</span>
        </div>
        <div class="search-result-list">
          <button
            v-for="result in searchResults"
            :key="result.path"
            class="search-result"
            @click="selectSearchResult(result)"
          >
            <strong>{{ result.title }}</strong>
            <small>{{ result.path }}</small>
            <p>{{ result.excerpt }}</p>
          </button>
          <NEmpty
            v-if="searchQuery && !searching && searchResults.length === 0"
            description="没有找到匹配的文档"
          />
          <div v-if="!searchQuery" class="search-tip">
            输入关键词搜索当前分支，结果覆盖标题、路径和正文。
          </div>
        </div>
      </div>
    </NModal>

    <ProjectManageModal
      v-model:show="showManage"
      :project="project"
      @changed="onProjectChanged"
      @deleted="onProjectDeleted"
    />
    <UsersModal v-model:show="showUsers" />
  </div>
</template>
