import type {Overview, Profile, Instance} from './types'

// Fictional, in-memory fixtures. Never written to the API or production database.
export function makeDemo(): Overview {
  const now = Date.now()
  const ago = (minutes: number) => new Date(now - minutes * 60000).toISOString()
  const policy = {medicine_budget: 0, stone_budget: 0, expiring_hours: 48, source: '演示配置', verified: true}
  const inventory = [
    ['30013','固源岩组', 42, 80, '1-7'], ['30063','全新装置', 18, 50, '7-15'],
    ['30043','异铁组', 67, 80, 'S4-1'], ['30053','酮凝集组', 96, 80, '3-7'],
    ['30023','糖组', 34, 60, '4-2'], ['30033','聚酸酯组', 82, 60, '2-6'],
  ].map((r, i) => ({id: String(r[0]), name: String(r[1]), current: Number(r[2]), minimum: 20, target: Number(r[3]), upper: 120, stage: String(r[4]), priority: i + 1, source: 'demo', observed_at: ago(3)}))
  const profile: Profile = {id: 'demo-main', name: '日常长草', account_hint: '演示账号', policy, tasks: ['StartUp', 'DepotMaintain', 'Infrast', 'Recruit', 'Mall', 'Award'], schedule_hash: 'demo', inventory, schedule: {title: '两班制 / 演示排班', plans: [
    {name: '早班', period: [['06:00','18:00']], rooms: {control: [{operators: ['干员 A', '干员 B', '干员 C']}], manufacture: [{operators: ['干员 D', '干员 E'], product: '纯金'}, {operators: ['干员 F', '干员 G'], product: '作战记录'}], trading: [{operators: ['干员 H', '干员 I'], product: '龙门币'}]}},
    {name: '晚班', period: [['18:00','23:59'], ['00:00','06:00']], rooms: {control: [{operators: ['干员 J', '干员 K']}], manufacture: [{operators: ['干员 L', '干员 M'], product: '纯金'}], trading: [{operators: ['干员 N', '干员 O'], product: '龙门币'}]}},
  ]}}
  const inst: Instance = {id: 'demo-maa', name: 'MAA / 演示实例', version: '演示', process_running: true, state: 'running', device_connected: true, fingerprint: 'demo', profiles: [profile], logs: ['[演示] 开始执行库存保持', '[演示] 关卡 1-7 代理中', '[演示] 账号策略校验通过：普通药剂 0 / 源石 0']}
  return {
    accounts: [{id: 'demo-primary', name: '主账号', role: 'primary', status: 'running', bound: true, agent_online: true, last_seen: ago(0), instance: inst, profile, policy: {...policy, expiring_hours: 0}}, {id: 'demo-secondary', name: '副账号', role: 'secondary', status: 'idle', bound: true, agent_online: true, last_seen: ago(0), instance: {...inst, id: 'demo-maa-2', state: 'idle'}, profile: {...profile, id: 'demo-secondary-profile'}, policy}],
    agents: [{agent_id: 'demo-agent', hostname: 'WINDOWS / DEMO', os: 'windows', version: '演示', last_seen: ago(0), online: true, instance: inst}],
    jobs: [{id: 'demo-job-001', account_id: 'demo-primary', type: '库存保持', state: 'running', progress: 64, created_at: ago(18)}, {id: 'demo-job-002', account_id: 'demo-secondary', type: '基建换班', state: 'pending', progress: 0, created_at: ago(4)}],
    events: [{id: 4, time: ago(1), level: 'info', account_id: 'demo-primary', message: '[演示] 理智作战完成，继续补充目标库存'}, {id: 3, time: ago(3), level: 'info', account_id: 'demo-primary', message: '[演示] 库存识别完成，发现 4 项材料缺口'}, {id: 2, time: ago(4), level: 'info', account_id: 'demo-secondary', message: '[演示] 基建换班任务已加入队列'}, {id: 1, time: ago(18), level: 'info', account_id: '', message: '[演示] Windows Agent 已连接'}],
    server_time: new Date(now).toISOString(), mode: 'demo', version: '0.4.3',
  }
}
