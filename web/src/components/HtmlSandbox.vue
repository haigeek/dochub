<script setup lang="ts">
defineProps<{
  src: string
  title: string
}>()
</script>

<template>
  <section class="html-document">
    <div class="html-document__toolbar">
      <span class="html-document__badge">HTML 隔离预览</span>
      <span>脚本运行在独立沙箱中，无法访问 DocHub 登录信息</span>
      <a
        class="html-document__open"
        :href="src"
        target="_blank"
        rel="noopener noreferrer"
      >
        新标签页打开
      </a>
    </div>
    <iframe
      class="html-document__frame"
      :src="src"
      :title="title"
      sandbox="allow-scripts allow-popups"
      referrerpolicy="no-referrer"
    />
  </section>
</template>
