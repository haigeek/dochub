<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { NAvatar, NButton, NSpace, NText, useMessage } from 'naive-ui'
import {
  LogOutOutline,
  MoonOutline,
  SettingsOutline,
  SunnyOutline,
} from '@vicons/ionicons5'
import { useSession } from '@/session'
import { useTheme } from '@/theme'
import AccountModal from '@/components/AccountModal.vue'

const props = defineProps<{
  backToProjects?: boolean
}>()

const router = useRouter()
const message = useMessage()
const session = useSession()
const { isDark, toggle: toggleTheme } = useTheme()
const showPassword = ref(false)
const initial = computed(() => session.user.value?.username.slice(0, 1).toUpperCase() ?? 'D')

async function logout() {
  await session.logout()
  message.success('已退出登录')
  await router.replace('/login')
}

function passwordChanged() {
  if (session.user.value) {
    session.user.value = { ...session.user.value, hasPassword: true }
  }
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
        <NButton
          v-if="session.isAdmin.value"
          quaternary
          circle
          title="系统设置"
          @click="router.push({ name: 'admin-settings' })"
        >
          <template #icon><SettingsOutline /></template>
        </NButton>
        <button
          type="button"
          class="user-chip user-chip-button"
          title="账号设置"
          @click="showPassword = true"
        >
          <NAvatar
            round
            :size="30"
            color="var(--vp-c-brand-soft)"
            text-color="var(--vp-c-brand-strong)"
          >
            {{ initial }}
          </NAvatar>
          <NText depth="2">{{ session.user.value?.username }}</NText>
        </button>
        <NButton quaternary circle title="退出登录" @click="logout">
          <template #icon><LogOutOutline /></template>
        </NButton>
      </NSpace>
  </header>
    <AccountModal
    v-model:show="showPassword"
    :username="session.user.value?.username ?? ''"
    :has-password="session.user.value?.hasPassword ?? false"
    @changed="passwordChanged"
  />
</template>
