<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { overviewInterval, trafficWindow } from './overview'
import { formatBytes } from '../../shared/format'
import type { TrafficSample } from './overview'

const props = defineProps<{ samples: TrafficSample[]; now: number; active: boolean; dark: boolean }>()
const canvas = ref<HTMLCanvasElement>()
let observer: ResizeObserver | undefined
let frame = 0
let displayedNow = props.now

function draw() {
  if (!props.active) return
  const chart = canvas.value
  const ctx = chart?.getContext('2d')
  if (!chart || !ctx) return
  const { width, height } = chart.getBoundingClientRect()
  if (width < 120 || height === 0) return
  const scale = window.devicePixelRatio || 1
  const pixelWidth = Math.round(width * scale)
  const pixelHeight = Math.round(height * scale)
  if (chart.width !== pixelWidth) chart.width = pixelWidth
  if (chart.height !== pixelHeight) chart.height = pixelHeight
  ctx.setTransform(scale, 0, 0, scale, 0, 0)
  ctx.clearRect(0, 0, chart.width / scale, chart.height / scale)

  const colors = getComputedStyle(chart)
  const muted = colors.getPropertyValue('--muted').trim()
  const grid = colors.getPropertyValue('--line').trim()
  const samples = props.samples
  const largest = Math.max(0, ...samples.map(point => Math.max(point.upload, point.download)))
  const target = Math.max(largest / 4, 250)
  const magnitude = 10 ** Math.floor(Math.log10(target))
  const step = ([1, 2, 5, 10].find(factor => factor * magnitude >= target) ?? 10) * magnitude
  const ceiling = step * 4
  const top = 12
  const bottom = height - 24
  const right = width - 6
  ctx.font = '11px system-ui, sans-serif'
  const ticks = Array.from({ length: 5 }, (_, index) => formatBytes(index * step, true))
  const left = Math.ceil(Math.max(...ticks.map(label => ctx.measureText(label).width))) + 12
  const x = (time: number) => right - (displayedNow - time) / trafficWindow * (right - left)
  const y = (rate: number) => bottom - rate / ceiling * (bottom - top)

  ctx.textAlign = 'right'
  ctx.textBaseline = 'middle'
  ctx.fillStyle = muted
  ctx.strokeStyle = grid
  ctx.lineWidth = 1
  ctx.setLineDash([])
  ctx.beginPath()
  for (const [tick, label] of ticks.entries()) {
    const value = tick * step
    const row = y(value)
    ctx.moveTo(left, row)
    ctx.lineTo(right, row)
    ctx.fillText(label, left - 7, row)
  }
  ctx.stroke()

  ctx.textAlign = 'left'
  ctx.fillText('2 分钟前', left, bottom + 15)
  ctx.textAlign = 'center'
  ctx.fillText('1 分钟前', (left + right) / 2, bottom + 15)
  ctx.textAlign = 'right'
  ctx.fillText('现在', right, bottom + 15)

  ctx.save()
  ctx.beginPath()
  ctx.rect(left, top - 3, right - left + 3, bottom - top + 6)
  ctx.clip()
  for (const series of ['download', 'upload'] as const) {
    const color = colors.getPropertyValue(series === 'download' ? '--accent' : '--success').trim()
    ctx.lineWidth = 2
    ctx.lineJoin = 'round'
    ctx.setLineDash(series === 'upload' ? [4, 3] : [])
    ctx.strokeStyle = ctx.fillStyle = color
    ctx.beginPath()
    for (const [index, point] of samples.entries()) {
      const before = samples[index - 1]
      const end = x(point.time)
      if (!before || point.startsSegment || point.time - before.time > 2 * overviewInterval) {
        // A short stroke keeps single samples visible at the edges of an outage.
        ctx.moveTo(end - 1, y(point[series]))
        ctx.lineTo(end, y(point[series]))
        continue
      }
      const middle = (x(before.time) + end) / 2
      ctx.bezierCurveTo(middle, y(before[series]), middle, y(point[series]), end, y(point[series]))
    }
    ctx.stroke()
    const last = samples.at(-1)
    if (last && displayedNow >= last.time && displayedNow - last.time <= 2 * overviewInterval) {
      ctx.beginPath()
      ctx.arc(x(last.time), y(last[series]), 2.5, 0, 2 * Math.PI)
      ctx.fill()
    }
  }
  ctx.restore()
}

watch(() => [props.active, props.now, props.dark] as const, ([active, now], [wasActive]) => {
  cancelAnimationFrame(frame)
  const from = displayedNow
  if (!active || !wasActive || !from || now <= from || now - from > 2 * overviewInterval ||
      window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    displayedNow = now
    draw()
    return
  }

  const start = performance.now()
  function advance(time: number) {
    const progress = Math.min(1, Math.max(0, time - start) / 420)
    displayedNow = from + (now - from) * (1 - (1 - progress) ** 3)
    draw()
    if (progress < 1) frame = requestAnimationFrame(advance)
  }
  frame = requestAnimationFrame(advance)
}, { flush: 'post' })
onMounted(() => {
  observer = new ResizeObserver(draw)
  if (canvas.value) observer.observe(canvas.value)
})
onUnmounted(() => {
  cancelAnimationFrame(frame)
  observer?.disconnect()
})
</script>

<template>
  <canvas ref="canvas" role="img" aria-label="最近两分钟的下载和上传速率趋势图"></canvas>
</template>

<style scoped>
canvas { display: block; width: 100%; height: clamp(230px, 29vh, 330px); }
@media (max-width: 600px) { canvas { height: 220px; } }
</style>
