import { ref } from 'vue'

export type DelayDisplay = { label: string; value?: number; tone: 'fast' | 'medium' | 'slow' | 'failed' }

// Completed measurements outlive the group view; only in-flight work belongs to it.
export const nodeDelays = ref(new Map<string, DelayDisplay>())
