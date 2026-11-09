<script setup lang="ts">
import { ref } from 'vue'
import { NButton, NIcon } from 'naive-ui'
import {
  BusinessOutline,
  ChevronBackOutline,
  ChevronForwardOutline,
  PeopleOutline,
  SettingsOutline,
} from '@vicons/ionicons5'
import AppHeader from '@/components/AppHeader.vue'

const collapsed = ref(false)
</script>

<template>
  <div class="page-shell">
    <AppHeader />
    <div class="admin-shell" :class="{ 'is-collapsed': collapsed }">
      <aside class="admin-sidebar">
        <div class="admin-sidebar-header">
          <div v-show="!collapsed" class="admin-sidebar-heading">
            <strong>管理后台</strong>
          </div>
          <NButton
            quaternary
            circle
            size="small"
            class="admin-sidebar-toggle"
            :title="collapsed ? '展开菜单' : '收起菜单'"
            :aria-label="collapsed ? '展开菜单' : '收起菜单'"
            @click="collapsed = !collapsed"
          >
            <template #icon>
              <ChevronForwardOutline v-if="collapsed" />
              <ChevronBackOutline v-else />
            </template>
          </NButton>
        </div>

        <nav class="admin-nav" aria-label="管理后台导航">
          <RouterLink
            :to="{ name: 'admin-users' }"
            class="admin-nav-item"
            :title="collapsed ? '用户管理' : undefined"
          >
            <NIcon :component="PeopleOutline" />
            <span v-show="!collapsed" class="admin-nav-label">用户管理</span>
          </RouterLink>
          <RouterLink
            :to="{ name: 'admin-organizations' }"
            class="admin-nav-item"
            :title="collapsed ? '组织机构' : undefined"
          >
            <NIcon :component="BusinessOutline" />
            <span v-show="!collapsed" class="admin-nav-label">组织机构</span>
          </RouterLink>
          <RouterLink
            :to="{ name: 'admin-settings' }"
            class="admin-nav-item"
            :title="collapsed ? '系统设置' : undefined"
          >
            <NIcon :component="SettingsOutline" />
            <span v-show="!collapsed" class="admin-nav-label">系统设置</span>
          </RouterLink>
        </nav>
      </aside>

      <main class="admin-main">
        <RouterView />
      </main>
    </div>
  </div>
</template>
