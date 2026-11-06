<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { NAvatar, NButton, NSpace, NText, useMessage } from 'naive-ui'
import { LogOutOutline, MoonOutline, PeopleOutline, SunnyOutline } from '@vicons/ionicons5'
import { useSession } from '@/session'
import { useTheme } from '@/theme'

const props = defineProps<{
  backToProjects?: boolean
}>()

const emit = defineEmits<{
  users: []
}>()

const router = useRouter()
const message = useMessage()
const session = useSession()
const { isDark, toggle: toggleTheme } = useTheme()
const initial = computed(() => session.user.value?.username.slice(0, 1).toUpperCase() ?? 'D')

async function logout() {
  await session.logout()
  message.success('已退出登录')
  await router.replace('/login')
}
</script>

<template>
  <header class="app-header">
    <button v-if="props.backToProjects" class="brand brand-button" @click="router.push('/projects')">
      <span class="brand-mark">D</span>
      <span>
        <b>DocHub</b>
        <small>源码文档中心</small>
      </span>
    </button>
    <RouterLink v-else to="/projects" class="brand">
      <span class="brand-mark">D</span>
      <span>
        <b>DocHub</b>
        <small>源码文档中心</small>
      </span>
    </RouterLink>

    <slot name="project-nav" />
    <div class="header-spacer" />
    <slot name="project-actions" />
    <NSpace align="center" :size="12">
      <NButton
        quaternary
        circle
        :title="isDark ? '切换到日间模式' : '切换到夜间模式'"
        @click="toggleTheme"
      >
        <template #icon>
          <SunnyOutline v-if="isDark" />
          <MoonOutline v-else />
        </template>
      </NButton>
      <NButton v-if="session.isAdmin.value" quaternary circle title="用户管理" @click="emit('users')">
        <template #icon><PeopleOutline /></template>
      </NButton>
      <div class="user-chip">
        <NAvatar
          round
          :size="30"
          color="var(--vp-c-brand-soft)"
          text-color="var(--vp-c-brand-strong)"
        >
          {{ initial }}
        </NAvatar>
        <NText depth="2">{{ session.user.value?.username }}</NText>
      </div>
      <NButton quaternary circle title="退出登录" @click="logout">
        <template #icon><LogOutOutline /></template>
      </NButton>
    </NSpace>
  </header>
</template>
