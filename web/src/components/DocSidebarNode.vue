<script setup lang="ts">
import { computed } from 'vue'
import type { SidebarTreeNode } from '@/types'

defineOptions({ name: 'DocSidebarNode' })

const props = withDefaults(
  defineProps<{
    node: SidebarTreeNode
    selectedPath: string
    expandedKeys: string[]
    depth?: number
  }>(),
  {
    depth: 0,
  },
)

const emit = defineEmits<{
  select: [path: string]
  toggle: [key: string]
}>()

const expanded = computed(() => props.expandedKeys.includes(props.node.key))
const active = computed(
  () => props.node.kind === 'document' && props.node.key === props.selectedPath,
)

function handleClick() {
  if (props.node.kind === 'folder') {
    emit('toggle', props.node.key)
    return
  }
  emit('select', props.node.key)
}
</script>

<template>
  <div class="vp-doc-tree-node">
    <button
      type="button"
      :class="[
        'vp-doc-tree-node__button',
        `is-${node.kind}`,
        { active, expanded, root: depth === 0 },
      ]"
      :style="{ paddingLeft: `${8 + depth * 16}px` }"
      :aria-expanded="node.kind === 'folder' ? expanded : undefined"
      :aria-current="active ? 'page' : undefined"
      @click="handleClick"
    >
      <span
        class="vp-doc-tree-node__arrow"
        :class="{ placeholder: node.kind === 'document', expanded }"
        aria-hidden="true"
      />
      <span class="vp-doc-tree-node__label">{{ node.label }}</span>
    </button>

    <div v-if="node.kind === 'folder' && expanded" class="vp-doc-tree-node__children">
      <DocSidebarNode
        v-for="child in node.children"
        :key="child.key"
        :node="child"
        :selected-path="selectedPath"
        :expanded-keys="expandedKeys"
        :depth="depth + 1"
        @select="emit('select', $event)"
        @toggle="emit('toggle', $event)"
      />
    </div>
  </div>
</template>
