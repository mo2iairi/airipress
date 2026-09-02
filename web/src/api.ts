const BASE = (import.meta.env.VITE_API_BASE as string | undefined) || '/api/v1';

export type Workspace = { id: string; name: string; root_path?: string; created_at?: string; updated_at?: string };
export type Source = { id: string; name: string; kind?: string; mime?: string; size?: number; relative_path?: string; source_type?: string };
export type FileObject = { id: string; name: string; kind: string; mime: string; size: number; sha256: string; source_count: number; created_at: string };
export type Model = { id: string; provider: 'openai' | 'deepseek' | 'gemini'; name: string; model: string; api_key?: string; base_url?: string; has_api_key?: boolean };
export type DiscoveredModel = { id: string; name: string };
export type ModelDiscovery = DiscoveredModel[] | { models: DiscoveredModel[] };
export type MessageVersion = { id: string; content: string; selected: boolean };
export type Message = { id: string; role: 'user' | 'assistant'; content: string; created_at?: string; versions?: MessageVersion[] };
export type Chat = { id: string; workspace_id: string; title: string; created_at: string };
export type Job = { id: string; status: 'queued' | 'running' | 'succeeded' | 'failed'; url?: string; error?: string };
export type GitHubStatus = { connected: boolean; login?: string; scopes?: string };
export type GitHubRepository = { name: string; full_name?: string; private: boolean; default_branch: string; description?: string };
export type GitHubBranch = { name: string };
export type MindmapNode = { text: string; children?: MindmapNode[] };
export type SiteTheme = { id: string; name: string; engine: 'astro' | 'hugo'; repository?: string; ref?: string; preview_url?: string; description?: string; installed: boolean; commit?: string };

export type Session = { authenticated?: boolean; username?: string; is_admin?: boolean };
export const auth = {
  session: () => req<Session>('/auth/session'),
  login: (username: string, password: string) => req<void>('/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => req<void>('/auth/session', { method: 'DELETE' }),
};

async function req<T>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method || 'GET').toUpperCase();
  const response = await fetch(BASE + path, { ...options, credentials: 'same-origin', headers: { ...(options.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }), ...(method !== 'GET' && method !== 'HEAD' ? { 'X-Airipress-Request': '1' } : {}), ...(options.headers || {}) } });
  if (!response.ok) {
    let message = `请求失败 (${response.status})`;
    if (response.status === 401) window.dispatchEvent(new CustomEvent('airipress:unauthorized'));
    try { const body = await response.json() as { error?: string }; if (body.error) message = body.error; } catch { /* response had no JSON body */ }
    throw new Error(message);
  }
  return response.status === 204 ? undefined as T : response.json() as Promise<T>;
}

async function stream(path: string, value: object, handlers: { start?: (value: { chat_id: string }) => void; delta: (content: string) => void; done: (message: Message) => void; error: (error: string) => void }, signal?: AbortSignal, method = 'POST'): Promise<void> {
  const response = await fetch(BASE + path, { method, credentials: 'same-origin', signal, headers: { 'Content-Type': 'application/json', 'X-Airipress-Request': '1' }, body: JSON.stringify(value) });
  if (!response.ok || !response.body) {
    let detail = `请求失败 (${response.status})`;
    try { const body = await response.json() as { error?: string }; detail = body.error || detail; } catch { /* no JSON error body */ }
    throw new Error(detail);
  }
  const reader = response.body.getReader(); const decoder = new TextDecoder(); let buffer = ''; let event = 'message';
  const dispatch = (chunk: string) => {
    const line = chunk.split('\n').find(item => item.startsWith('data:'));
    if (!line) return;
    try {
      const data = JSON.parse(line.slice(5).trim()) as { content?: string; error?: string; chat_id?: string; id?: string; role?: Message['role']; created_at?: string };
      if (event === 'start' && data.chat_id) handlers.start?.({ chat_id: data.chat_id });
      if (event === 'delta' && data.content) handlers.delta(data.content);
      if (event === 'done' && data.id && data.role && typeof data.content === 'string') handlers.done({ id: data.id, role: data.role, content: data.content, created_at: data.created_at });
      if (event === 'error') handlers.error(data.error || '生成失败');
    } catch { handlers.error('无法解析模型返回内容'); }
  };
  while (true) {
    const { done, value: chunk } = await reader.read();
    if (done) break;
    buffer += decoder.decode(chunk, { stream: true });
    let separator: number;
    while ((separator = buffer.indexOf('\n\n')) >= 0) {
      const packet = buffer.slice(0, separator); buffer = buffer.slice(separator + 2);
      const eventLine = packet.split('\n').find(line => line.startsWith('event:'));
      event = eventLine ? eventLine.slice(6).trim() : 'message'; dispatch(packet);
    }
  }
}

export function normalizeMindmap(value: unknown): MindmapNode | null {
  if (!value || typeof value !== 'object') return null;
  const candidate = value as { root?: unknown; content?: { root?: unknown } };
  const root = candidate.root || candidate.content?.root || value;
  if (!root || typeof root !== 'object') return null;
  const node = root as { text?: unknown; title?: unknown; name?: unknown; children?: unknown };
  const text = node.text || node.title || node.name;
  if (typeof text !== 'string') return null;
  return { text, children: Array.isArray(node.children) ? node.children.map(normalizeMindmap).filter((child): child is MindmapNode => child !== null) : [] };
}

export const api = {
  workspaces: () => req<Workspace[]>('/workspaces'),
  createWorkspace: (value: Partial<Workspace>) => req<Workspace>('/workspaces', { method: 'POST', body: JSON.stringify(value) }),
  updateWorkspace: (id: string, value: Partial<Workspace>) => req<Workspace>(`/workspaces/${id}`, { method: 'PATCH', body: JSON.stringify(value) }),
  deleteWorkspace: (id: string) => req<void>(`/workspaces/${id}`, { method: 'DELETE' }),
  sources: (workspace: string) => req<Source[]>(`/workspaces/${workspace}/sources`),
  uploadSource: (workspace: string, file: File) => { const data = new FormData(); data.append('file', file); return req<Source>(`/workspaces/${workspace}/sources`, { method: 'POST', body: data }); },
  attachSource: (workspace: string, fileID: string, relativePath: string) => req<Source>(`/workspaces/${workspace}/sources`, { method: 'POST', body: JSON.stringify({ file_id: fileID, relative_path: relativePath }) }),
  deleteSource: (workspace: string, id: string) => req<void>(`/workspaces/${workspace}/sources/${id}`, { method: 'DELETE' }),
  files: () => req<FileObject[]>('/files'),
  uploadFile: (file: File) => { const data = new FormData(); data.append('file', file); return req<FileObject>('/files', { method: 'POST', body: data }); },
  models: () => req<Model[]>('/models'),
  discoverModels: (value: { provider: Model['provider']; api_key?: string; base_url?: string; model_id?: string }) => req<ModelDiscovery>('/models/discover', { method: 'POST', body: JSON.stringify(value) }),
  createModel: (value: Partial<Model>) => req<Model>('/models', { method: 'POST', body: JSON.stringify(value) }),
  updateModel: (id: string, value: Partial<Model>) => req<Model>(`/models/${id}`, { method: 'PATCH', body: JSON.stringify(value) }),
  deleteModel: (id: string) => req<void>(`/models/${id}`, { method: 'DELETE' }),
  chats: (workspace: string) => req<Chat[]>(`/workspaces/${workspace}/chats`),
  createChat: (workspace: string, title?: string) => req<Chat>(`/workspaces/${workspace}/chats`, { method: 'POST', body: JSON.stringify({ title }) }),
  updateChat: (workspace: string, chat: string, title: string) => req<Chat>(`/workspaces/${workspace}/chats/${chat}`, { method: 'PATCH', body: JSON.stringify({ title }) }),
  deleteChat: (workspace: string, chat: string) => req<void>(`/workspaces/${workspace}/chats/${chat}`, { method: 'DELETE' }),
  chatMessages: (workspace: string, chat: string) => req<Message[]>(`/workspaces/${workspace}/chats/${chat}/messages`),
  streamMessage: (workspace: string, chat: string, model_id: string, content: string, handlers: Parameters<typeof stream>[2], signal?: AbortSignal) => stream(`/workspaces/${workspace}/chats/${chat}/messages`, { model_id, content }, handlers, signal),
  editMessage: (workspace: string, chat: string, message: string, model_id: string, content: string, handlers: Parameters<typeof stream>[2], signal?: AbortSignal) => stream(`/workspaces/${workspace}/chats/${chat}/messages/${message}`, { model_id, content }, handlers, signal, 'PATCH'),
  retryMessage: (workspace: string, chat: string, message: string, model_id: string, handlers: Parameters<typeof stream>[2], signal?: AbortSignal) => stream(`/workspaces/${workspace}/chats/${chat}/messages/${message}/retry`, { model_id }, handlers, signal),
  selectMessageVersion: (workspace: string, chat: string, message: string, version_id: string) => req<Message>(`/workspaces/${workspace}/chats/${chat}/messages/${message}/version`, { method: 'PATCH', body: JSON.stringify({ version_id }) }),
  branchChat: (workspace: string, chat: string, message_id: string) => req<Chat>(`/workspaces/${workspace}/chats/${chat}/branch`, { method: 'POST', body: JSON.stringify({ message_id }) }),
  mindmap: (workspace: string) => req<unknown>(`/workspaces/${workspace}/studio/mindmap`, { method: 'POST' }),
  publish: (workspace: string, value: object) => req<Job>(`/workspaces/${workspace}/publish`, { method: 'POST', body: JSON.stringify(value) }),
  themes: () => req<SiteTheme[]>('/themes'),
  installTheme: (id: string) => req<SiteTheme>(`/themes/${encodeURIComponent(id)}/install`, { method: 'POST' }),
  importTheme: (value: { git_url: string; ref?: string; name?: string; preview_url?: string; description?: string }) => req<SiteTheme>('/themes/import', { method: 'POST', body: JSON.stringify(value) }),
  githubStatus: () => req<GitHubStatus>('/github/status'),
  githubRepositories: () => req<GitHubRepository[]>('/github/repos'),
  githubBranches: (repository: string) => req<GitHubBranch[]>(`/github/repos/${encodeURIComponent(repository)}/branches`),
  githubCreateRepository: (value: { name: string; private: boolean; description?: string }) => req<GitHubRepository>('/github/repos', { method: 'POST', body: JSON.stringify(value) }),
  githubConnect: () => { window.location.assign(BASE + '/github/start'); },
  githubDisconnect: () => req<void>('/github/disconnect', { method: 'DELETE' }),
  job: (id: string) => req<Job>(`/jobs/${id}`),
  exportData: async () => {
    const response = await fetch(BASE + '/data/export', { credentials: 'same-origin' });
    if (!response.ok) throw new Error(`导出失败 (${response.status})`);
    return response.blob();
  },
  importData: (file: File) => { const data = new FormData(); data.append('file', file); return req<void>('/data/import', { method: 'POST', body: data }); },
};
