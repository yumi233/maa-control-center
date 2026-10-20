<script setup lang="ts">
import type {State} from '../types'
defineProps<{status: State}>()
const labels: Record<State, string> = {offline: '已离线', idle: '空闲待命', queued: '等待执行', running: '运行中', stopping: '停止确认中', unknown: '状态待确认', error: '需要检查'}
</script>
<template><span class="status-pill" :class="status"><i aria-hidden="true"></i>{{ labels[status] }}</span></template>
