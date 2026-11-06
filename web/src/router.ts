import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from './session'
import LoginView from './views/LoginView.vue'
import ProjectsView from './views/ProjectsView.vue'
import ProjectView from './views/ProjectView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/projects' },
    { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
    { path: '/projects', name: 'projects', component: ProjectsView },
    { path: '/projects/:slug', name: 'project', component: ProjectView },
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
  return true
})

export default router
