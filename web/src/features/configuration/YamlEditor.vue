<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Annotation, Compartment, EditorState } from '@codemirror/state'
import { EditorView, drawSelection, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers, placeholder } from '@codemirror/view'
import { defaultKeymap, history, historyKeymap, isolateHistory, redo, redoDepth, undo, undoDepth } from '@codemirror/commands'
import { openSearchPanel, search, searchKeymap } from '@codemirror/search'
import { HighlightStyle, indentOnInput, syntaxHighlighting } from '@codemirror/language'
import { yaml } from '@codemirror/lang-yaml'
import { tags } from '@lezer/highlight'

const yamlColors = HighlightStyle.define([
  { tag: tags.propertyName, color: 'var(--code-key)' },
  { tag: tags.comment, color: 'var(--code-comment)' },
  { tag: [tags.number, tags.bool, tags.null], color: 'var(--code-number)' },
  { tag: [tags.string, tags.special(tags.string)], color: 'var(--code-string)' },
  { tag: [tags.atom, tags.keyword, tags.meta], color: 'var(--code-keyword)' },
  { tag: tags.invalid, color: 'var(--error)', textDecoration: 'underline' },
])

const props = withDefaults(defineProps<{ modelValue: string; disabled: boolean; ariaLabel?: string }>(), { ariaLabel: '完整 YAML 源码' })
const emit = defineEmits<{
  'update:modelValue': [value: string]
  position: [state: { line: number; column: number; canUndo: boolean; canRedo: boolean }]
}>()
const host = ref<HTMLDivElement>()
const external = Annotation.define<boolean>()
const locked = new Compartment()
let view: EditorView | undefined

function editability() {
  return [EditorState.readOnly.of(props.disabled), EditorView.editable.of(!props.disabled)]
}

function reportPosition(state: EditorState) {
  const cursor = state.selection.main.head
  const line = state.doc.lineAt(cursor)
  emit('position', {
    line: line.number,
    column: cursor - line.from + 1,
    canUndo: undoDepth(state) > 0,
    canRedo: redoDepth(state) > 0,
  })
}

defineExpose({
  undo() {
    if (view && undo(view)) view.focus()
  },
  redo() {
    if (view && redo(view)) view.focus()
  },
  search() {
    if (view) openSearchPanel(view)
  },
})

onMounted(() => {
  view = new EditorView({
    parent: host.value,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        yaml(), syntaxHighlighting(yamlColors), lineNumbers(), highlightActiveLineGutter(),
        highlightActiveLine(), placeholder('在这里粘贴或编辑 YAML'), drawSelection(), history(), indentOnInput(), EditorView.lineWrapping,
        search({ top: true }),
        EditorState.phrases.of({
          Find: '搜索', Replace: '替换为', next: '下一个', previous: '上一个', all: '全选',
          'match case': '区分大小写', regexp: '正则', 'by word': '全词',
          replace: '替换', 'replace all': '全部替换', close: '关闭搜索',
        }),
        keymap.of([...searchKeymap, ...defaultKeymap, ...historyKeymap]), locked.of(editability()),
        EditorView.contentAttributes.of({
          'aria-label': props.ariaLabel,
          'aria-multiline': 'true',
          'spellcheck': 'false',
          'autocapitalize': 'off',
          'autocomplete': 'off',
          'autocorrect': 'off',
        }),
        EditorView.updateListener.of(update => {
          if (update.docChanged && !update.transactions.some(tr => tr.annotation(external))) {
            emit('update:modelValue', update.state.doc.toString())
          }
          if (update.docChanged || update.selectionSet) reportPosition(update.state)
        }),
      ],
    }),
  })
  reportPosition(view.state)
})

watch(() => props.modelValue, value => {
  if (!view) return
  const current = view.state.doc.toString()
  if (value === current) return

  let start = 0
  const limit = Math.min(current.length, value.length)
  while (start < limit && current[start] === value[start]) start++
  let oldEnd = current.length
  let newEnd = value.length
  while (oldEnd > start && newEnd > start && current[oldEnd - 1] === value[newEnd - 1]) {
    oldEnd--
    newEnd--
  }
  view.dispatch({
    changes: { from: start, to: oldEnd, insert: value.slice(start, newEnd) },
    annotations: [external.of(true), isolateHistory.of('full')],
  })
})
watch(() => props.disabled, () => view?.dispatch({ effects: locked.reconfigure(editability()) }))
onBeforeUnmount(() => view?.destroy())
</script>

<template><div ref="host" class="code-editor"></div></template>
