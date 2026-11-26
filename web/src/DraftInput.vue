<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElInput } from 'element-plus/es/components/input/index'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ change: [value: string]; pending: [changed: boolean] }>()
const draft = ref(props.modelValue)
watch(() => props.modelValue, value => { draft.value = value })
</script>

<template>
  <ElInput v-model="draft" @input="emit('pending', $event !== modelValue)" @change="emit('change', $event)" />
</template>
