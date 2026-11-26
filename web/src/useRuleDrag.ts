import { nextTick, onUnmounted, ref, watch, type Ref } from 'vue'

/** Rows expose their source index through data-rule-index, handles use data-rule-handle. */
export function useRuleDrag(
  list: Ref<HTMLElement | undefined>,
  move: (from: number, to: number) => Promise<boolean>,
  disabled: () => boolean,
) {
  const draggedIndex = ref<number | null>(null)
  const dropIndex = ref<number | null>(null)
  const saving = ref(false)
  let pointer: number | undefined
  let pointerY = 0
  let pointerX = 0
  let frame = 0

  function updateTarget() {
    const container = list.value
    if (!container) return
    const bounds = container.getBoundingClientRect()
    const x = Math.max(bounds.left + 1, Math.min(bounds.right - 1, pointerX))
    const y = Math.max(bounds.top + 1, Math.min(bounds.bottom - 1, pointerY))
    const row = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-rule-index]')
    if (row && container.contains(row)) {
      const index = Number(row.dataset.ruleIndex)
      if (Number.isInteger(index)) dropIndex.value = index
    }
  }

  function scroll() {
    const container = list.value
    if (pointer === undefined || !container) return
    const bounds = container.getBoundingClientRect()
    const edge = Math.min(64, bounds.height / 4)
    const above = Math.max(0, edge - (pointerY - bounds.top))
    const below = Math.max(0, edge - (bounds.bottom - pointerY))
    if (above || below) {
      container.scrollTop += Math.max(-18, Math.min(18, (below - above) / 4))
      updateTarget()
    }
    frame = requestAnimationFrame(scroll)
  }

  function cancel() {
    cancelAnimationFrame(frame)
    pointer = undefined
    draggedIndex.value = dropIndex.value = null
    window.removeEventListener('pointermove', track)
    window.removeEventListener('pointerup', finish)
    window.removeEventListener('pointercancel', cancel)
    window.removeEventListener('keydown', escape)
    window.removeEventListener('blur', cancel)
  }

  async function commit(from: number, to: number) {
    if (disabled() || saving.value || from === to) return
    saving.value = true
    try {
      const moved = await move(from, to)
      if (moved) {
        await nextTick()
        list.value?.querySelector<HTMLElement>(`[data-rule-index="${to}"] [data-rule-handle]`)?.focus({ preventScroll: true })
      }
    } finally {
      saving.value = false
    }
  }

  function track(event: PointerEvent) {
    if (event.pointerId !== pointer) return
    if (disabled()) { cancel(); return }
    event.preventDefault()
    pointerX = event.clientX
    pointerY = event.clientY
    updateTarget()
  }

  function finish(event: PointerEvent) {
    if (event.pointerId !== pointer) return
    const from = draggedIndex.value
    const to = dropIndex.value
    const bounds = list.value?.getBoundingClientRect()
    const inside = bounds && event.clientX >= bounds.left && event.clientX <= bounds.right &&
      event.clientY >= bounds.top && event.clientY <= bounds.bottom
    cancel()
    if (inside && from !== null && to !== null) void commit(from, to)
  }

  function escape(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); cancel() }
  }

  function start(event: PointerEvent, index: number) {
    if (disabled() || saving.value || pointer !== undefined || event.button !== 0 || !event.isPrimary) return
    event.preventDefault()
    const handle = event.currentTarget as HTMLElement
    handle.focus({ preventScroll: true })
    pointer = event.pointerId
    pointerX = event.clientX
    pointerY = event.clientY
    draggedIndex.value = dropIndex.value = index
    window.addEventListener('pointermove', track, { passive: false })
    window.addEventListener('pointerup', finish)
    window.addEventListener('pointercancel', cancel)
    window.addEventListener('keydown', escape)
    window.addEventListener('blur', cancel)
    frame = requestAnimationFrame(scroll)
  }

  function key(event: KeyboardEvent, index: number, previousIndex?: number, nextIndex?: number) {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    event.preventDefault()
    if (disabled() || saving.value) return
    const rows = Array.from(list.value?.querySelectorAll<HTMLElement>('[data-rule-index]') ?? [])
    const position = rows.findIndex(row => Number(row.dataset.ruleIndex) === index)
    const neighbor = rows[position + (event.key === 'ArrowUp' ? -1 : 1)]
    const target = event.key === 'ArrowUp' ? previousIndex : nextIndex
    const to = target ?? (neighbor ? Number(neighbor.dataset.ruleIndex) : undefined)
    if (to !== undefined && Number.isInteger(to)) void commit(index, to)
  }

  watch(disabled, value => { if (value) cancel() })
  onUnmounted(cancel)
  return { draggedIndex, dropIndex, saving, start, key, cancel }
}
