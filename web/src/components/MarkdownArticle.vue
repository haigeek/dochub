<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { Heading } from '@/types'
import { renderMarkdown } from '@/lib/markdown'

const props = defineProps<{
  markdown: string
  docPath: string
  resolveAsset: (path: string) => string
}>()
const emit = defineEmits<{
  headings: [headings: Heading[]]
  navigate: [path: string, anchor: string]
  activeHeading: [id: string]
}>()

const root = ref<HTMLElement | null>(null)
let observer: IntersectionObserver | null = null

const rendered = computed(() =>
  renderMarkdown(props.markdown, props.docPath, props.resolveAsset),
)

watch(
  rendered,
  async (value) => {
    emit('headings', value.headings)
    await nextTick()
    setupHeadingObserver()
    await renderMermaid()
  },
  { immediate: true },
)

onMounted(() => {
  setupHeadingObserver()
})

onBeforeUnmount(() => observer?.disconnect())

function handleClick(event: MouseEvent) {
  const target = event.target as HTMLElement
  const link = target.closest<HTMLAnchorElement>('a[data-doc-path]')
  if (!link?.dataset.docPath) return
  event.preventDefault()
  emit('navigate', link.dataset.docPath, link.dataset.docAnchor ?? '')
}

function setupHeadingObserver() {
  observer?.disconnect()
  if (!root.value) return
  const headings = root.value.querySelectorAll<HTMLElement>('h2[id], h3[id], h4[id]')
  if (headings[0]?.id) emit('activeHeading', headings[0].id)
  observer = new IntersectionObserver(
    (entries) => {
      const visible = entries
        .filter((entry) => entry.isIntersecting)
        .sort((left, right) => left.boundingClientRect.top - right.boundingClientRect.top)
      if (visible[0]?.target.id) emit('activeHeading', visible[0].target.id)
    },
    { rootMargin: '-80px 0px -70% 0px', threshold: 0 },
  )
  headings.forEach((heading) => observer?.observe(heading))
}

async function renderMermaid() {
  if (!root.value?.querySelector('.mermaid')) return
  try {
    const mermaid = (await import('mermaid')).default
    mermaid.initialize({
      startOnLoad: false,
      theme: 'neutral',
      securityLevel: 'strict',
      fontFamily: 'inherit',
    })
    await mermaid.run({ nodes: root.value.querySelectorAll<HTMLElement>('.mermaid') })
  } catch {
    // 渲染失败时保留 Mermaid 源文本，避免影响正文其余内容。
  }
}
</script>

<template>
  <article ref="root" class="markdown-body" @click="handleClick" v-html="rendered.html" />
</template>
