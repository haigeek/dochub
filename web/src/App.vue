<script setup lang="ts">
import { computed } from 'vue'
import {
  darkTheme,
  NConfigProvider,
  NDialogProvider,
  NMessageProvider,
  type GlobalThemeOverrides,
} from 'naive-ui'
import { useTheme } from './theme'

const { isDark } = useTheme()
const naiveTheme = computed(() => (isDark.value ? darkTheme : null))
const themeOverrides = computed<GlobalThemeOverrides>(() => ({
  common: {
    primaryColor: isDark.value ? '#3e63dd' : '#5672cd',
    primaryColorHover: isDark.value ? '#5c73e7' : '#3a5ccc',
    primaryColorPressed: isDark.value ? '#3451b2' : '#3451b2',
    primaryColorSuppl: isDark.value ? '#3e63dd' : '#5672cd',
    borderRadius: '6px',
    fontFamily:
      'Inter, ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
  },
  Button: {
    borderRadiusMedium: '6px',
  },
}))
</script>

<template>
  <NConfigProvider :theme="naiveTheme" :theme-overrides="themeOverrides">
    <NDialogProvider>
      <NMessageProvider>
        <RouterView />
      </NMessageProvider>
    </NDialogProvider>
  </NConfigProvider>
</template>
