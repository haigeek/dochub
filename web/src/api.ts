let csrfToken = ''

export class ApiError extends Error {
  status: number
  code: string
  details?: string

  constructor(status: number, code: string, message: string, details?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}

export function updateCSRF(value: string) {
  csrfToken = value
}

export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const headers = new Headers(options.headers)
  const method = (options.method ?? 'GET').toUpperCase()
  if (options.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const response = await fetch(path, {
    ...options,
    method,
    headers,
    credentials: 'same-origin',
  })
  if (response.status === 204) {
    return undefined as T
  }
  const payload = (await response.json().catch(() => null)) as
    | { data: T; error?: never }
    | { data?: never; error: { code: string; message: string; details?: string } }
    | null
  if (!response.ok) {
    const error = payload?.error
    throw new ApiError(
      response.status,
      error?.code ?? 'REQUEST_FAILED',
      error?.message ?? `请求失败 (${response.status})`,
      error?.details,
    )
  }
  return (payload as { data: T }).data
}

export async function downloadFile(path: string, fallbackName: string) {
  const response = await fetch(path, {
    credentials: 'same-origin',
  })
  if (!response.ok) {
    const payload = (await response.json().catch(() => null)) as
      | { error?: { code: string; message: string; details?: string } }
      | null
    throw new ApiError(
      response.status,
      payload?.error?.code ?? 'DOWNLOAD_FAILED',
      payload?.error?.message ?? `下载失败 (${response.status})`,
      payload?.error?.details,
    )
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  const utf8Name = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1]
  const filename = utf8Name ? decodeURIComponent(utf8Name) : fallbackName
  const blob = await response.blob()
  const objectURL = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectURL
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(objectURL), 1000)
}
