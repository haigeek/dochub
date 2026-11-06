import { computed, ref } from 'vue'
import { api, updateCSRF } from './api'
import type { User } from './types'

const user = ref<User | null>(null)
const ready = ref(false)
let pending: Promise<void> | null = null

export function useSession() {
  const isAdmin = computed(() => user.value?.role === 'admin')

  async function load(force = false) {
    if (ready.value && !force) return
    if (pending && !force) return pending
    pending = api<{ user: User; csrfToken: string }>('/api/auth/me')
      .then((data) => {
        user.value = data.user
        updateCSRF(data.csrfToken)
      })
      .catch(() => {
        user.value = null
      })
      .finally(() => {
        ready.value = true
        pending = null
      })
    return pending
  }

  async function login(username: string, password: string) {
    const data = await api<{ user: User; csrfToken: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    })
    user.value = data.user
    updateCSRF(data.csrfToken)
    ready.value = true
  }

  async function logout() {
    try {
      await api<void>('/api/auth/logout', { method: 'POST' })
    } finally {
      user.value = null
      updateCSRF('')
      ready.value = true
    }
  }

  return { user, ready, isAdmin, load, login, logout }
}
