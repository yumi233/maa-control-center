export type State = 'offline' | 'idle' | 'queued' | 'running' | 'stopping' | 'unknown' | 'error'
export interface Policy {medicine_budget: number | null; stone_budget: number | null; expiring_hours: number | null; source: string; verified: boolean}
export interface InventoryItem {id: string; name: string; current: number | null; minimum: number | null; target: number | null; upper: number | null; priority: number; stage: string; source: string; observed_at: string}
export interface Schedule {title?: string; plans: {name?: string; period?: string[][]; rooms?: Record<string, {operators?: string[]; candidates?: string[]; product?: string; group?: string; skip?: boolean}[]>; drones?: Record<string, unknown>}[]}
export interface Profile {id: string; name: string; account_hint: string; policy: Policy; tasks: string[]; schedule?: Schedule; schedule_hash: string; inventory: InventoryItem[]}
export interface Instance {id: string; name: string; version: string; process_running: boolean; process_verified?: boolean; configuration_write?: boolean; state: State; device_connected: boolean | null; fingerprint: string; profiles: Profile[]; logs: string[]}
export interface Account {id: string; owner_id?:string; name: string; role: string; status: State; bound: boolean; agent_online: boolean; last_seen: string; instance: Instance | null; profile: Profile | null; policy: Policy}
export interface Agent {agent_id: string; hostname: string; os: string; version: string; last_seen: string; online: boolean; instance: Instance | null}
export interface Event {id: number; time: string; level: string; account_id: string; message: string}
export interface Job {id: string; account_id: string; type: string; state: string; progress: number; created_at: string}
export interface Overview {accounts: Account[]; agents: Agent[]; events: Event[]; jobs: Job[]; server_time: string; mode: string; version: string}
