export interface User {
  id: string
  username: string
  role: 'admin' | 'member'
  createdAt: string
}

export interface Branch {
  id: string
  projectId: string
  name: string
  commitSha: string
  status: 'never' | 'running' | 'succeeded' | 'failed'
  lastError: string
  lastSyncedAt: string | null
}

export interface Project {
  id: string
  slug: string
  name: string
  repoUrl: string
  docRoots: string[]
  ownerId: string
  ownerName: string
  createdAt: string
  updatedAt: string
  branches: Branch[]
}

export interface DocumentItem {
  path: string
  title: string
}

export interface SidebarTreeNode {
  key: string
  label: string
  kind: 'folder' | 'document'
  children?: SidebarTreeNode[]
}

export interface DocumentContent extends DocumentItem {
  markdown: string
  plainText?: string
}

export interface SearchResult {
  path: string
  title: string
  excerpt: string
}

export interface SyncJob {
  id: string
  projectId: string
  branch: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  error: string
  createdAt: string
  finishedAt: string | null
}

export interface Heading {
  id: string
  text: string
  level: number
}
