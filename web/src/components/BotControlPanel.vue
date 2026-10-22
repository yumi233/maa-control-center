<script setup lang="ts">
import {onMounted, ref} from 'vue'
import {request} from '../api'
import type {Account} from '../types'

const props = defineProps<{accounts: Account[]; demo: boolean}>()
type Member = {uid: string; label: string; role: string}
type Binding = {id: string; account_id: string; owner_id: string; umo: string; members: Member[]; enabled: boolean; revision: number}
const bindings = ref<Binding[]>([]), busy = ref(false), error = ref(''), feedback = ref(''), connection = ref(''), revoking = ref('')
const form = ref({account_id: props.accounts[0]?.id || '', umo: '', members: [{uid: '', label: '', role: 'operator'}] as Member[]})
const name = (id: string) => props.accounts.find(a => a.id === id)?.name || id
async function load() {if (!props.demo) bindings.value = await request<Binding[]>('/bot/bindings')}
async function run(action: () => Promise<void>) {if (busy.value) return; busy.value = true; error.value = ''; feedback.value = ''; try {await action()} catch(e) {error.value = (e as Error).message} finally {busy.value = false}}
async function create() {await run(async () => {
 if (props.demo) {feedback.value = '演示模式不生成凭据或绑定真实群聊。'; return}
 const result = await request<{binding: Binding; token: string}>('/bot/bindings', {method: 'POST', body: JSON.stringify(form.value)})
 connection.value = JSON.stringify({base_url: location.origin, ca_file: '', routes: [{account_id: result.binding.account_id, label: name(result.binding.account_id), umo: result.binding.umo, token: result.token}]}, null, 2)
 await load(); feedback.value = '接入已创建。请保存下方仅展示一次的私有配置，再安装到 AstrBot。'
})}
async function save(b: Binding) {await run(async () => {if (props.demo) return; await request('/bot/bindings/' + b.id, {method:'PUT', body:JSON.stringify({members:b.members, revision:b.revision})}); await load(); feedback.value = '成员权限已保存，下次指令立即按新权限核对。'})}
async function revoke(b: Binding) {await run(async () => {if (!props.demo) await request('/bot/bindings/' + b.id, {method:'DELETE'}); revoking.value = ''; connection.value = ''; await load(); feedback.value = '接入已撤销，旧凭据无法继续调用。'})}
function add(members: Member[]) {if (members.length < 32) members.push({uid:'', label:'', role:'viewer'})}
function download() {const blob = new Blob([connection.value], {type:'application/json'}), url = URL.createObjectURL(blob), link = document.createElement('a'); link.href = url; link.download = 'maa-connection.private.json'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)}
onMounted(() => run(load))
</script>

<template>
 <div class="agent-grid bot-control">
  <p v-if="error" class="inline-alert" role="alert">{{ error }}</p><p v-if="feedback" class="agent-feedback" role="status">{{ feedback }}</p>
  <section class="panel agent-request">
   <div class="panel-heading"><div><span class="eyebrow">// GROUP ACCESS</span><h2>一个会话，一个账号的授权</h2></div></div>
   <p class="muted bot-lede">先在网页创建并绑定账号，再授权群控。每个接入只操作选定账号；通知接收范围在“我的接入与通知”单独设置。</p>
   <form @submit.prevent="create">
    <label>绑定账号<select v-model="form.account_id" required><option v-for="a in accounts" :key="a.id" :value="a.id">{{ a.name }}</option></select></label>
    <label>群聊或私聊 UMO<input v-model.trim="form.umo" maxlength="1024" required placeholder="在目标会话发送 /sid，粘贴完整 UMO" autocomplete="off"/></label>
    <h3>授权成员</h3><p class="muted">成员在机器人会话里发送 /sid，提供 UID。仅查看可查状态与汇报；可启停允许额外发起或停止该账号任务。</p>
    <div v-for="(m,i) in form.members" :key="i" class="agent-channel">
     <label>成员 UID<input v-model.trim="m.uid" maxlength="128" required autocomplete="off" placeholder="使用 /sid 的 UID，非 QQ 昵称或群号"/></label>
     <div class="agent-field-row"><label>成员备注<input v-model.trim="m.label" maxlength="120" placeholder="方便识别的名字"/></label><label>权限<select v-model="m.role"><option value="viewer">仅查看</option><option value="operator">可启停任务</option></select></label></div>
     <button type="button" class="text-button" :disabled="form.members.length === 1" @click="form.members.splice(i,1)">移除此成员</button>
    </div>
    <div class="agent-button-row"><button type="button" class="button" :disabled="form.members.length >= 32 || busy" @click="add(form.members)">添加成员</button><button class="button primary" :disabled="busy || !form.account_id">创建接入</button></div>
   </form>
  </section>
  <section class="panel agent-request">
   <div class="panel-heading"><div><span class="eyebrow">// SETUP GUIDE</span><h2>添加成员与新账号</h2></div></div>
   <div class="bot-copy">
   <p>已有接入：在下方添加 UID、选择权限并保存，无需重装机器人。移除成员后立即失去后续指令权限。</p>
   <p>新账号：管理员添加账号并确定归属；需要独立网页登录时再创建普通用户。设备管理员把新 MAA 配置档加入 Agent 允许范围，再在账号页绑定、核对预算并添加定时。</p>
   <p>新群控：为新账号创建独立接入，将私有配置合并到 AstrBot 插件配置的 routes 列表。一个群绑定多个账号时，指令必须带账号备注。</p>
   <p>新通知：在账号主人的“我的接入与通知”填写接收会话及账号范围。新建群控不会自动让原有群收到新账号记录。</p>
   <p class="muted">开始沿用当前已启用任务和资源预算；停止先发请求，等待 Windows 确认。群控不能改预算、账号归属、成员权限或定时安排。撤销接入不自动停止已提交任务；需要时由账号主人在网页停止。</p>
   <p><code>/maa 状态</code> · <code>/maa 开始 账号备注</code> · <code>/maa 停止 账号备注</code> · <code>/maa 汇报</code> · <code>/maa 今日</code> · <code>/maa 计划</code></p>
   <a class="text-button" href="https://github.com/yumi233/maa-control-center/blob/main/docs/group-control.md" target="_blank" rel="noopener noreferrer">查看完整接入与移交说明</a>
   <div v-if="connection" class="agent-channel"><h3>一次性私有接入配置</h3><p class="muted">包含群控密钥，请只交给自己的 AstrBot，勿发群、截图分享或提交 Git。内网 HTTPS 需把 NAS CA 复制到 AstrBot，并填写 ca_file；不要关闭证书验证。</p><textarea :value="connection" readonly rows="8" aria-label="一次性群控私有配置"></textarea><div class="agent-button-row"><button class="button primary" @click="download">下载私有配置</button><button class="button" @click="connection = ''">已保存，隐藏</button></div></div>
   </div>
  </section>
  <section class="panel agent-history agent-request">
   <div class="panel-heading"><div><span class="eyebrow">// MEMBERS AND PERMISSIONS</span><h2>已授权接入</h2></div><button class="text-button" :disabled="busy" @click="run(load)">刷新</button></div>
   <p v-if="!bindings.length" class="agent-history-empty">尚无群控接入。创建后可在这里添加成员、调整权限或撤销。</p>
   <div v-for="b in bindings" :key="b.id" class="agent-channel bot-binding">
    <h3>{{ name(b.account_id) }} / {{ b.enabled ? '启用' : '已撤销' }}</h3><p class="muted" style="overflow-wrap:anywhere">{{ b.umo }}</p>
    <template v-if="b.enabled">
     <div v-for="(m,i) in b.members" :key="i" class="agent-channel">
      <label>成员 UID<input v-model.trim="m.uid" maxlength="128" autocomplete="off"/></label><div class="agent-field-row"><label>成员备注<input v-model.trim="m.label" maxlength="120"/></label><label>权限<select v-model="m.role"><option value="viewer">仅查看</option><option value="operator">可启停任务</option></select></label></div><button type="button" class="text-button" :disabled="b.members.length === 1" @click="b.members.splice(i,1)">移除此成员</button>
     </div>
     <div class="agent-button-row"><button class="button" :disabled="busy || b.members.length >= 32" @click="add(b.members)">添加成员</button><button class="button primary" :disabled="busy" @click="save(b)">保存成员权限</button><button class="button" :disabled="busy" @click="revoking = b.id">撤销接入</button></div>
     <div v-if="revoking === b.id" class="agent-retry"><p>撤销后旧配置无法再调用；已提交的任务不会自动停止。</p><button class="button" @click="revoking = ''">返回</button><button class="button primary" :disabled="busy" @click="revoke(b)">确认撤销</button></div>
    </template>
   </div>
  </section>
 </div>
</template>

<style scoped>
.bot-control > .inline-alert,.bot-control > .agent-feedback{grid-column:1/-1;margin:0}
.bot-lede{padding:18px 24px 0;margin:0;font-size:12px;line-height:1.9}
.bot-copy{padding:24px;font-size:13px;line-height:1.9}
.bot-copy p{margin:0 0 16px}
.bot-copy code{overflow-wrap:anywhere}
.bot-binding{padding:24px;margin:0}
.bot-binding h3{margin:0 0 8px;font-size:16px}
.bot-binding > .muted{font-size:11px;margin-bottom:24px}
@media(max-width:600px){.bot-copy,.bot-binding{padding:20px}.bot-lede{padding:16px 20px 0}}
</style>
