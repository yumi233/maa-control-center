<script setup lang="ts">
import {ArrowUpRight, Play, ShieldCheck, SlidersHorizontal, Cpu, Clock3} from 'lucide-vue-next'
import StatusPill from './StatusPill.vue'
import type {Account, Job} from '../types'
defineProps<{account: Account; index: number; job?: Job; demo: boolean}>()
defineEmits<{detail: [account: Account]; run: [account: Account]}>()
const taskNames: Record<string,string> = {StartUp:'开始唤醒', DepotMaintain:'库存保持', Infrast:'基建换班', Recruit:'公开招募', Mall:'信用收支', Award:'领取奖励', UserDataUpdate:'更新数据', Fight:'理智作战'}
</script>
<template>
 <article class="account-card">
  <div class="account-top"><span class="account-number">0{{ index + 1 }}<small>/ ACCOUNT</small></span><StatusPill :status="account.status"/></div>
  <div class="account-identity"><div class="account-symbol" aria-hidden="true">{{ account.role === 'primary' ? 'M' : 'S' }}<span></span></div><div><h3>{{ account.name }}</h3><span class="muted">{{ account.role === 'primary' ? '主账号' : '副账号' }}<span class="separator">/</span>{{ account.profile?.name || '尚未绑定配置' }}</span></div><button class="icon-button arrow-link" aria-label="查看账号详情" @click="$emit('detail', account)"><ArrowUpRight :size="22"/></button></div>
  <div class="account-current"><span>{{ job?.state === 'running' ? '当前任务' : '当前状态' }}</span><strong>{{ job?.state === 'running' ? job.type : account.bound ? (account.instance?.process_running ? '等待任务状态确认' : 'MAA 尚未运行') : '等待接入现有配置' }}</strong><span v-if="job?.state === 'running'" class="readout">{{ job.progress }}<small>%</small></span></div>
  <div class="segmented-progress" aria-hidden="true"><i v-for="n in 24" :key="n" :class="{filled: job && n <= job.progress / 100 * 24, signal: job?.state === 'running' && n > 13 && n < 17}"></i></div>
  <div class="account-meta"><span><Cpu :size="14"/>{{ account.instance ? `MAA ${account.instance.version || '版本待确认'}` : 'MAA 未发现' }}</span><span><Clock3 :size="14"/>{{ account.agent_online ? 'Agent 在线' : 'Agent 未连接' }}</span></div>
  <div class="policy-line"><ShieldCheck :size="15"/><span>{{ account.role === 'secondary' ? '普通药剂 0 / 源石 0 / 临期药 48h' : '保留原有资源使用策略' }}</span><b v-if="!account.policy.verified">待核实</b><b v-else>已读取</b></div>
  <div class="task-chips"><span v-for="t in (account.profile?.tasks || ['等待读取任务']).slice(0, 4)" :key="t">{{ taskNames[t] || t }}</span><span v-if="(account.profile?.tasks.length || 0) > 4">+{{ account.profile!.tasks.length - 4 }}</span></div>
  <div class="account-actions"><button class="button small" @click="$emit('detail', account)"><SlidersHorizontal :size="15"/>查看配置</button><button class="button small primary" :disabled="!demo" :title="demo ? '模拟添加任务' : '真实控制尚未通过兼容性验证'" @click="$emit('run', account)"><Play :size="14"/>执行任务<ArrowUpRight :size="14"/></button></div>
 </article>
</template>
