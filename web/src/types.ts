export interface User {
  id: string
  username: string
  role: 'admin' | 'member'
  hasPassword: boolean
  createdAt: string
}

export interface UserSummary {
  id: string
  username: string
  role: 'admin' | 'member'
}

export interface OrganizationSummary {
  id: string
  name: string
}

export interface Organization {
  id: string
  name: string
  members: UserSummary[]
  createdAt: string
  updatedAt: string
}

export interface AccessToken {
  id: string
  name: string
  prefix: string
  token: string
  expiresAt: string | null
  lastUsedAt: string | null
  createdAt: string
}

export interface DocumentAccess {
  path: string
  users: UserSummary[]
  organizations: OrganizationSummary[]
}

export interface ProjectAccess {
  projectUsers: UserSummary[]
  projectOrganizations: OrganizationSummary[]
  documents: DocumentAccess[]
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
  isPublic: boolean
  id: string
  slug: string
  name: string
  description: string
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
  format: 'markdown' | 'html'
}

export interface SidebarTreeNode {
  key: string
  label: string
  kind: 'folder' | 'document'
  children?: SidebarTreeNode[]
}

export interface DocumentContent extends DocumentItem {
  markdown?: string
  html?: string
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

export interface SSOConfig {
  id: string
  authKey: string
  enabled: boolean
  providerName: string
  endpointBaseUrl: string
  authorizePath: string
  tokenPath: string
  userInfoPath: string
  clientId: string
  hasClientSecret: boolean
  redirectUri: string
  portalUrl: string
  issuer: string
  scopes: string[]
  tokenAuthMethod: 'client_secret_post' | 'client_secret_basic'
  pkce: boolean
  subjectField: string
  usernameField: string
  updatedBy: string
  updatedAt: string | null
}

export interface SSOProvider extends SSOConfig {
  callbackPath: string
}
