import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from './session'
import AdminLayout from './views/AdminLayout.vue'
import LoginView from './views/LoginView.vue'
import OrganizationsView from './views/OrganizationsView.vue'
import ProjectsView from './views/ProjectsView.vue'
import ProjectView from './views/ProjectView.vue'
import SettingsView from './views/SettingsView.vue'
import UsersView from './views/UsersView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/projects' },
    { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
    { path: '/projects', name: 'projects', component: ProjectsView },
    { path: '/projects/:slug', name: 'project', component: ProjectView },
    {
      path: '/admin',
      component: AdminLayout,
      meta: { adminOnly: true },
      children: [
        { path: '', redirect: { name: 'admin-users' } },
        { path: 'users', name: 'admin-users', component: UsersView },
        { path: 'organizations', name: 'admin-organizations', component: OrganizationsView },
        { path: 'settings', name: 'admin-settings', component: SettingsView },
      ],
    },
    { path: '/settings', redirect: { name: 'admin-settings' } },
    { path: '/:pathMatch(.*)*', redirect: '/projects' },
  ],
})

router.beforeEach(async (to) => {
  const session = useSession()
  await session.load()
  if (to.meta.public) {
    if (session.user.value) return { name: 'projects' }
    return true
  }
  if (!session.user.value) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.meta.adminOnly && !session.isAdmin.value) {
    return { name: 'projects' }
  }
  return true
})

export default router
