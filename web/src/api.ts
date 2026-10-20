export let csrf = ''
export function setCSRF(value: string) {csrf = value}
export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {...options, credentials: 'same-origin', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, ...options.headers}})
  const data = await response.json()
  if (response.status===401 && path!=='/auth/login') window.dispatchEvent(new Event('maacc-session-expired'))
  if (!response.ok) throw new Error(data.message || '服务连接失败，请稍后重试')
  return data as T
}
