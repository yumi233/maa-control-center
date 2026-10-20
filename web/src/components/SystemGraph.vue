<script setup lang="ts">
import {Server, Monitor, Cpu, Smartphone, ChevronRight} from 'lucide-vue-next'
defineProps<{agentOnline: boolean; maaRunning: boolean; connected: boolean; demo: boolean}>()
</script>
<template>
  <div class="system-graph" aria-label="NAS 到 Windows Agent、MAA 和模拟器的连接状态">
    <div class="graph-heading"><span class="eyebrow">// CONNECTION</span><span>{{ demo ? '模拟连接' : '连接状态' }}<i class="graph-led"></i></span></div>
    <div class="graph-nodes">
      <div class="graph-node active"><Server :size="24"/><strong>NAS</strong><small>Web 服务</small></div><div class="graph-wire"><span></span><ChevronRight :size="13"/></div>
      <div class="graph-node" :class="{active: agentOnline}"><Monitor :size="24"/><strong>AGENT</strong><small>{{ agentOnline ? '已连接' : '等待连接' }}</small></div><div class="graph-wire"><span></span><ChevronRight :size="13"/></div>
      <div class="graph-node" :class="{active: maaRunning}"><Cpu :size="24"/><strong>MAA</strong><small>{{ maaRunning ? '进程存在' : '未运行 / 未知' }}</small></div><div class="graph-wire"><span></span><ChevronRight :size="13"/></div>
      <div class="graph-node" :class="{active: connected}"><Smartphone :size="24"/><strong>DEVICE</strong><small>{{ connected ? '已验证连接' : '连接待确认' }}</small></div>
    </div>
    <div class="graph-note"><span class="tiny-square"></span>{{ demo ? '演示数据 / 不连接实际设备' : 'MAA 负责执行，控制中心负责观察。' }}</div>
  </div>
</template>
