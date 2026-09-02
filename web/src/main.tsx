import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { BookOpen, Database, FolderOpen, Globe2, GitBranch, Link2, LogOut, MoreHorizontal, Network, Pencil, Plus, RotateCcw, Send, Settings, ShieldCheck, Trash2, Upload, Workflow, X } from 'lucide-react';
import { api, auth, Chat as ChatRecord, DiscoveredModel, FileObject, GitHubBranch, GitHubRepository, GitHubStatus, Job, Message, MindmapNode, Model, normalizeMindmap, Session, SiteTheme, Source, Workspace } from './api';
import './styles.css';

type Tab = 'chat' | 'sources' | 'studio' | 'models';
const messageOf = (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback;
const ErrorBox = ({ error }: { error: string }) => error ? <div className="error">{error}</div> : null;

function Login({ onLogin }: { onLogin: (session: Session) => void }) {
  const [username, setUsername] = useState(''); const [password, setPassword] = useState(''); const [error, setError] = useState(''); const [busy, setBusy] = useState(false);
  const submit = async (event: React.FormEvent) => { event.preventDefault(); if (!username || !password) return; setBusy(true); try { await auth.login(username, password); const session = await auth.session(); setPassword(''); onLogin({ ...session, authenticated: true, username: session.username || username }); } catch (cause) { setError(messageOf(cause, '登录失败，请检查账号和密码')); } finally { setBusy(false); } };
  return <div className="login-page"><form className="login-card" onSubmit={submit}><div className="brand"><b>✦</b> airipress</div><h1>登录工作台</h1><p>使用管理员账号访问你的私有知识空间。</p><ErrorBox error={error}/><label className="field">用户名<input autoComplete="username" value={username} onChange={event => setUsername(event.target.value)} required/></label><label className="field">密码<input type="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.target.value)} required/></label><button className="primary" disabled={busy}>{busy ? '登录中…' : '登录'}</button></form></div>;
}

function AuthGate() {
  const [session, setSession] = useState<Session | null>(null); const [checking, setChecking] = useState(true);
  useEffect(() => { const unauthorized = () => setSession(null); window.addEventListener('airipress:unauthorized', unauthorized); auth.session().then(value => setSession(value.authenticated === false ? null : { ...value, authenticated: true })).catch(() => setSession(null)).finally(() => setChecking(false)); return () => window.removeEventListener('airipress:unauthorized', unauthorized); }, []);
  if (checking) return <div className="login-page"><div className="login-card"><p>正在检查登录状态…</p></div></div>;
  return session ? <Workbench session={session} onLogout={() => setSession(null)}/> : <Login onLogin={setSession}/>;
}

function Workbench({ session, onLogout }: { session: Session; onLogout: () => void }) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [active, setActive] = useState('');
  const [models, setModels] = useState<Model[]>([]);
  const [sources, setSources] = useState<Source[]>([]);
  const [tab, setTab] = useState<Tab>('chat');
  const [error, setError] = useState('');
  const [showModels, setShowModels] = useState(false);
  const [showFiles, setShowFiles] = useState(false);
  const [showData, setShowData] = useState(false);
  const load = async () => { try { const [ws, ms] = await Promise.all([api.workspaces(), api.models()]); setWorkspaces(ws); setModels(ms); setActive(current => ws.some(item => item.id === current) ? current : (ws[0]?.id || '')); setError(''); } catch (cause) { setError(messageOf(cause, '无法加载服务数据')); } };
  useEffect(() => { void load(); }, []);
  useEffect(() => { if (!active) { setSources([]); return; } api.sources(active).then(setSources).catch(cause => setError(messageOf(cause, '无法加载来源'))); }, [active]);
  const addWorkspace = async () => { const name = window.prompt('工作区名称'); if (!name?.trim()) return; try { const created = await api.createWorkspace({ name: name.trim() }); setWorkspaces(items => [...items, created]); setActive(created.id); } catch (cause) { setError(messageOf(cause, '创建失败')); } };
  const renameWorkspace = async () => {
    if (!current) return;
    const name = window.prompt('新的工作区名称', current.name);
    if (!name?.trim() || name.trim() === current.name) return;
    try {
      const updated = await api.updateWorkspace(current.id, { name: name.trim() });
      setWorkspaces(items => items.map(item => item.id === updated.id ? updated : item));
      setError('');
    } catch (cause) { setError(messageOf(cause, '重命名失败')); }
  };
  const deleteWorkspace = async () => {
    if (!current || !window.confirm(`删除工作区“${current.name}”？共享文件对象不会被误删。`)) return;
    try {
      await api.deleteWorkspace(current.id);
      const remaining = workspaces.filter(item => item.id !== current.id);
      setWorkspaces(remaining);
      setActive(remaining[0]?.id || '');
      setError('');
    } catch (cause) { setError(messageOf(cause, '删除失败')); }
  };
  const current = workspaces.find(item => item.id === active);
  if (!current) return <EmptyWorkbench session={session} onLogout={onLogout} onError={setError} models={models} setModels={setModels} onCreate={addWorkspace} onImported={load} showModels={showModels} setShowModels={setShowModels} showFiles={showFiles} setShowFiles={setShowFiles} showData={showData} setShowData={setShowData} error={error}/>;
  const tabs: [Tab, string, typeof BookOpen][] = [['chat', '对话', BookOpen], ['sources', '来源', Network], ['studio', 'Studio', Settings], ['models', '模型与安全', ShieldCheck]];
  const mobile = tab === 'chat' ? <Chat workspace={active} models={models}/> : tab === 'sources' ? <Sources workspace={active} sources={sources} setSources={setSources}/> : tab === 'studio' ? <Studio workspace={active}/> : <Models models={models} setModels={setModels}/>;
  return <div className="app"><header className="topbar"><div className="brand"><b>✦</b> airipress</div><div className="topbar-workspace"><span>WORKSPACE</span><select aria-label="选择工作区" value={active} onChange={event => setActive(event.target.value)}>{workspaces.map(workspace => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}</select><button className="top-icon" aria-label="创建工作区" onClick={addWorkspace}><Plus size={16}/></button><button className="top-icon" aria-label="重命名工作区" onClick={renameWorkspace}><Pencil size={15}/></button><button className="top-icon danger-icon" aria-label="删除工作区" onClick={deleteWorkspace}><Trash2 size={15}/></button></div><div className="topbar-actions"><span className="model-pill"><span className="live-dot"/> {models[0]?.name || '未配置模型'}</span><span className="admin-name">{session.username || '管理员'}</span><button className="top-action" onClick={() => setShowFiles(true)}><FolderOpen size={16}/> 文件系统</button><button className="top-action" onClick={() => setShowModels(true)}><ShieldCheck size={16}/> 模型与安全</button><button className="top-action" onClick={() => setShowData(true)}><Database size={16}/> 全库同步</button><button className="top-action" onClick={async () => { try { await auth.logout(); onLogout(); } catch (cause) { setError(messageOf(cause, '退出失败')); } }}><LogOut size={16}/> 退出</button></div></header>
    <nav className="mobile-tabs">{tabs.map(([id, label, Icon]) => <button className={tab === id ? 'nav-active' : ''} onClick={() => setTab(id)} key={id}><Icon size={16}/>{label}</button>)}</nav>
    <div className="workspace-grid"><aside className="source-column"><div className="column-heading"><div><span className="eyebrow">SOURCE</span><h2>来源</h2></div></div><div className="desktop-source"><Sources workspace={active} sources={sources} setSources={setSources}/></div><div className="mobile-content">{tab === 'sources' && mobile}</div><div className="source-workspaces"><div className="side-label">工作区</div>{workspaces.map(workspace => <button className={`workspace ${workspace.id === active ? 'selected' : ''}`} key={workspace.id} onClick={() => setActive(workspace.id)}><i/>{workspace.name}</button>)}</div></aside>
      <main className="chat-column"><div className="column-heading"><div><span className="eyebrow">CHAT</span><h2>{current?.name || '选择一个工作区'}</h2></div><span className="secure-label"><ShieldCheck size={14}/> 私密工作区</span></div><div className="desktop-chat"><Chat workspace={active} models={models}/></div><div className="mobile-content">{tab === 'chat' && mobile}</div></main>
      <aside className="studio-column"><div className="column-heading"><div><span className="eyebrow">STUDIO</span><h2>工作室</h2></div></div><div className="desktop-studio"><Studio workspace={active}/></div><div className="mobile-content">{(tab === 'studio' || tab === 'models') && mobile}</div></aside></div>
    {showModels && <div className="modal"><div className="modal-inner model-drawer"><button className="close" aria-label="关闭" onClick={() => setShowModels(false)}><X size={18}/></button><Models models={models} setModels={setModels}/></div></div>}{showFiles && <FileSystemModal onClose={() => setShowFiles(false)}/>} {showData && <DataModal onClose={() => setShowData(false)} onImported={async () => { setShowData(false); await load(); }}/>} {error && <div className="shell-error"><ErrorBox error={error}/></div>}</div>;
}

function EmptyWorkbench({ session, onLogout, onError, models, setModels, onCreate, onImported, showModels, setShowModels, showFiles, setShowFiles, showData, setShowData, error }: { session: Session; onLogout: () => void; onError: (value: string) => void; models: Model[]; setModels: React.Dispatch<React.SetStateAction<Model[]>>; onCreate: () => void; onImported: () => void | Promise<void>; showModels: boolean; setShowModels: (value: boolean) => void; showFiles: boolean; setShowFiles: (value: boolean) => void; showData: boolean; setShowData: (value: boolean) => void; error: string }) {
  return <div className="app"><header className="topbar"><div className="brand"><b>✦</b> airipress</div><div className="topbar-actions"><span className="model-pill"><span className="live-dot"/> {models[0]?.name || '未配置模型'}</span><span className="admin-name">{session.username || '管理员'}</span><button className="top-action" onClick={() => setShowFiles(true)}><FolderOpen size={16}/> 文件系统</button><button className="top-action" onClick={() => setShowModels(true)}><ShieldCheck size={16}/> 模型与安全</button><button className="top-action" onClick={() => setShowData(true)}><Database size={16}/> 全库同步</button><button className="top-action" onClick={async () => { try { await auth.logout(); onLogout(); } catch (cause) { onError(messageOf(cause, '退出失败')); } }}><LogOut size={16}/> 退出</button></div></header><main className="workspace-empty"><div className="empty-card"><div className="empty-symbol">✦</div><h1>创建你的第一个工作区</h1><p>工作区用于管理来源、对话和 Studio 成果。</p><button className="primary" onClick={onCreate}><Plus size={16}/> 创建工作区</button></div></main>{showModels && <div className="modal"><div className="modal-inner model-drawer"><button className="close" aria-label="关闭" onClick={() => setShowModels(false)}><X size={18}/></button><Models models={models} setModels={setModels}/></div></div>}{showFiles && <FileSystemModal onClose={() => setShowFiles(false)}/>} {showData && <DataModal onClose={() => setShowData(false)} onImported={async () => { setShowData(false); await onImported(); }}/>} {error && <div className="shell-error"><ErrorBox error={error}/></div>}</div>;
}

function DataModal({ onClose, onImported }: { onClose: () => void; onImported: () => void | Promise<void> }) {
  const [file, setFile] = useState<File | null>(null); const [busy, setBusy] = useState(false); const [error, setError] = useState('');
  const exportData = async () => { try { const blob = await api.exportData(); const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = 'airipress-data.airipress'; link.click(); URL.revokeObjectURL(url); } catch (cause) { setError(messageOf(cause, '导出失败')); } };
  const importData = async () => { if (!file || !window.confirm('导入将覆盖当前全部工作区、来源、对话和配置数据，且无法撤销。确定继续吗？')) return; setBusy(true); try { await api.importData(file); await onImported(); } catch (cause) { setError(messageOf(cause, '导入失败')); } finally { setBusy(false); } };
  return <div className="modal"><div className="modal-inner"><button className="close" aria-label="关闭" onClick={onClose}><X size={18}/></button><h2>全库同步</h2><p className="muted">在本机导出或覆盖导入全部工作区、来源、对话和模型配置。</p><button className="primary" onClick={exportData}><Database size={16}/> 导出全部数据</button><div className="import-warning"><strong>危险操作</strong><p>导入会覆盖当前全部数据，请先确认你已备份现有内容。</p></div><label className="drop compact-drop"><Upload size={20}/><strong>选择 airipress 备份包</strong><small>{file?.name || '支持 .airipress 或 .zip'}</small><input type="file" accept=".airipress,.zip,application/zip" onChange={event => setFile(event.target.files?.[0] || null)}/></label><ErrorBox error={error}/><button className="primary" disabled={!file || busy} onClick={importData}>{busy ? '导入中…' : '确认覆盖并导入'}</button></div></div>;
}

function FileSystemModal({ onClose }: { onClose: () => void }) {
  const [files, setFiles] = useState<FileObject[]>([]); const [error, setError] = useState(''); const [busy, setBusy] = useState(false);
  const load = async () => { try { setFiles(await api.files()); setError(''); } catch (cause) { setError(messageOf(cause, '无法读取文件系统')); } };
  useEffect(() => { void load(); }, []);
  const upload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]; if (!file) return; setBusy(true);
    try { const stored = await api.uploadFile(file); setFiles(items => [stored, ...items.filter(item => item.id !== stored.id)]); setError(''); }
    catch (cause) { setError(messageOf(cause, '添加文件失败')); }
    finally { setBusy(false); event.target.value = ''; }
  };
  return <div className="modal"><div className="modal-inner model-drawer file-system-modal"><button className="close" aria-label="关闭" onClick={onClose}><X size={18}/></button><h2>文件系统</h2><p className="muted">文件对象只存一份，可被多个工作区引用。</p><label className="drop compact-drop"><Upload size={20}/><strong>{busy ? '正在添加…' : '全局添加文件'}</strong><small>图片、Markdown、文本和常见代码文件</small><input disabled={busy} type="file" accept="image/*,.md,.txt,.go,.js,.jsx,.ts,.tsx,.py,.json,.yaml,.yml,.css,.html,.rs,.java,.c,.h,.cpp,.sh" onChange={upload}/></label><ErrorBox error={error}/><div className="file-list">{files.length ? files.map(file => <div className="file-row" key={file.id}><b>{file.kind === 'image' ? 'IMG' : 'FILE'}</b><span>{file.name}<small>{(file.size / 1024).toFixed(1)} KB · 已被 {file.source_count} 个来源引用</small></span></div>) : <div className="empty-card">文件系统为空。</div>}</div></div></div>;
}

function Chat({ workspace, models }: { workspace: string; models: Model[] }) {
  const [chats, setChats] = useState<ChatRecord[]>([]); const [activeChat, setActiveChat] = useState(''); const [messages, setMessages] = useState<Message[]>([]);
  const [text, setText] = useState(''); const [modelID, setModelID] = useState(''); const [error, setError] = useState(''); const [busy, setBusy] = useState(false); const [editing, setEditing] = useState<Message | null>(null); const [menu, setMenu] = useState('');
  const controller = React.useRef<AbortController | null>(null);
  useEffect(() => { setModelID(models[0]?.id || ''); }, [models]);
  useEffect(() => { controller.current?.abort(); setChats([]); setMessages([]); setActiveChat(''); if (!workspace) return; api.chats(workspace).then(items => { setChats(items); setActiveChat(items[0]?.id || ''); }).catch(cause => setError(messageOf(cause, '无法加载聊天'))); return () => controller.current?.abort(); }, [workspace]);
  useEffect(() => { if (!workspace || !activeChat) { setMessages([]); return; } api.chatMessages(workspace, activeChat).then(setMessages).catch(cause => setError(messageOf(cause, '无法加载对话'))); }, [workspace, activeChat]);
  const beginStream = async (run: (handlers: { delta: (value: string) => void; done: (value: Message) => void; error: (value: string) => void }, signal: AbortSignal) => Promise<void>, user?: Message) => {
    const temporary: Message = { id: `stream-${Date.now()}`, role: 'assistant', content: '' }; if (user) setMessages(items => [...items, user, temporary]); else setMessages(items => [...items, temporary]);
    setBusy(true); setError(''); const aborter = new AbortController(); controller.current = aborter;
    try { await run({ delta: value => setMessages(items => items.map(item => item.id === temporary.id ? { ...item, content: item.content + value } : item)), done: message => setMessages(items => items.map(item => item.id === temporary.id ? message : item)), error: value => { setMessages(items => items.filter(item => item.id !== temporary.id)); setError(value); } }, aborter.signal); }
    catch (cause) { if (!aborter.signal.aborted) { setMessages(items => items.filter(item => item.id !== temporary.id)); setError(messageOf(cause, '生成失败')); } }
    finally { if (controller.current === aborter) controller.current = null; setBusy(false); }
  };
  const send = async () => { if (!text.trim() || !workspace || !activeChat || !modelID || busy) return; const content = text.trim(); setText(''); await beginStream((handlers, signal) => api.streamMessage(workspace, activeChat, modelID, content, handlers, signal), { id: `user-${Date.now()}`, role: 'user', content }); };
  const create = async () => { try { const created = await api.createChat(workspace); setChats(items => [created, ...items]); setActiveChat(created.id); setMessages([]); } catch (cause) { setError(messageOf(cause, '新建聊天失败')); } };
  const remove = async () => { if (!activeChat || !window.confirm('删除当前聊天及其消息？')) return; try { await api.deleteChat(workspace, activeChat); const rest = chats.filter(item => item.id !== activeChat); setChats(rest); setActiveChat(rest[0]?.id || ''); } catch (cause) { setError(messageOf(cause, '删除聊天失败')); } };
  const retry = async (message: Message) => { if (!modelID || busy) return; setMenu(''); const index = messages.findIndex(item => item.id === message.id); setMessages(items => items.slice(0, index + (message.role === 'assistant' ? 0 : 1))); await beginStream((handlers, signal) => api.retryMessage(workspace, activeChat, message.id, modelID, handlers, signal)); try { setMessages(await api.chatMessages(workspace, activeChat)); } catch { /* the streamed result remains visible */ } };
  const saveEdit = async () => { if (!editing || !modelID || !editing.content.trim() || busy) return; const content = editing.content.trim(); const index = messages.findIndex(item => item.id === editing.id); setEditing(null); setMessages(items => items.slice(0, index + 1).map(item => item.id === editing.id ? { ...item, content } : item)); await beginStream((handlers, signal) => api.editMessage(workspace, activeChat, editing.id, modelID, content, handlers, signal)); };
  const branch = async (message: Message) => { try { const created = await api.branchChat(workspace, activeChat, message.id); setChats(items => [created, ...items]); setActiveChat(created.id); setMenu(''); } catch (cause) { setError(messageOf(cause, '创建分支失败')); } };
  return <section className="chat"><ErrorBox error={error}/><div className="chat-toolbar"><select aria-label="选择聊天" value={activeChat} onChange={event => setActiveChat(event.target.value)}><option value="">选择聊天</option>{chats.map(chat => <option key={chat.id} value={chat.id}>{chat.title}</option>)}</select><button className="top-icon" aria-label="新建聊天" onClick={create}><Plus size={16}/></button><button className="top-icon danger-icon" aria-label="删除当前聊天" disabled={!activeChat} onClick={remove}><Trash2 size={15}/></button></div><div className="messages">
    {activeChat && !messages.length && <div className="empty"><div>✦</div><h2>开始对话</h2></div>}
    {!activeChat && <div className="empty"><h2>新建聊天</h2><button className="primary" onClick={create}><Plus size={16}/> 新建聊天</button></div>}
    {messages.map(message => <div className={`msg ${message.role}`} key={message.id}><div className="avatar">{message.role === 'user' ? '你' : '✦'}</div><div className="message-main"><small>{message.role === 'user' ? '你' : 'airipress'}</small>{editing?.id === message.id ? <div className="message-edit"><textarea value={editing.content} onChange={event => setEditing({ ...editing, content: event.target.value })}/><button className="secondary" onClick={() => setEditing(null)}>取消</button><button className="primary" onClick={() => void saveEdit()}>保存并生成</button></div> : <div className="bubble">{message.content || (busy ? '正在生成…' : '')}</div>} {message.role === 'assistant' && (message.versions?.length || 0) > 1 && <div className="version-nav"><button aria-label="上一版回答" disabled={message.versions?.[0]?.id === message.versions?.find(version => version.selected)?.id} onClick={async () => { const versions = message.versions || []; const index = versions.findIndex(version => version.selected); if (index <= 0) return; try { const updated = await api.selectMessageVersion(workspace, activeChat, message.id, versions[index - 1].id); setMessages(items => items.map(item => item.id === message.id ? updated : item)); } catch (cause) { setError(messageOf(cause, '切换回答版本失败')); } }}>‹</button><span>{(message.versions?.findIndex(version => version.selected) || 0) + 1} / {message.versions?.length}</span><button aria-label="下一版回答" disabled={(message.versions || [])[Math.max(0, (message.versions?.length || 1) - 1)]?.id === message.versions?.find(version => version.selected)?.id} onClick={async () => { const versions = message.versions || []; const index = versions.findIndex(version => version.selected); if (index < 0 || index >= versions.length - 1) return; try { const updated = await api.selectMessageVersion(workspace, activeChat, message.id, versions[index + 1].id); setMessages(items => items.map(item => item.id === message.id ? updated : item)); } catch (cause) { setError(messageOf(cause, '切换回答版本失败')); } }}>›</button></div>} {!message.id.startsWith('stream-') && !message.id.startsWith('user-') && editing?.id !== message.id && <div className="message-actions"><button aria-label="打开消息操作" className="message-menu" onClick={() => setMenu(menu === message.id ? '' : message.id)}><MoreHorizontal size={17}/></button>{menu === message.id && <div className="message-popover">{message.role === 'user' && <button onClick={() => { setEditing(message); setMenu(''); }}><Pencil size={14}/> 修改并重新生成</button>}<button onClick={() => void retry(message)}><RotateCcw size={14}/> 重试</button><button onClick={() => void branch(message)}><GitBranch size={14}/> 分支到新聊天</button></div>}</div>}</div></div>)}
  </div><div className="composer"><textarea disabled={!activeChat || busy} value={text} onChange={event => setText(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void send(); } }} placeholder={activeChat ? '输入问题，按 Enter 发送…' : '请先新建聊天'}/><div>
    <select value={modelID} onChange={event => setModelID(event.target.value)}><option value="">选择模型</option>{models.map(model => <option key={model.id} value={model.id}>{model.provider} · {model.name}</option>)}</select>
    <button className="send" disabled={busy || !text.trim() || !workspace || !activeChat || !modelID} onClick={() => void send()}><Send size={15}/>{busy ? '生成中…' : '发送'}</button>
  </div></div></section>;
}

function Sources({ workspace, sources, setSources }: { workspace: string; sources: Source[]; setSources: React.Dispatch<React.SetStateAction<Source[]>> }) {
  const [error, setError] = useState(''); const [showPicker, setShowPicker] = useState(false);
  const upload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file || !workspace) return;
    try { const created = await api.uploadSource(workspace, file); setSources(items => [...items, created]); setError(''); }
    catch (cause) { setError(messageOf(cause, '上传失败')); }
    finally { event.target.value = ''; }
  };
  return <section className="panel"><ErrorBox error={error}/>
    <label className="drop"><Upload size={25}/><strong>上传图片、Markdown、文本或代码</strong><small>支持图片、MD、TXT 与常见代码格式</small><input type="file" accept="image/*,.md,.txt,.go,.js,.jsx,.ts,.tsx,.py,.json,.yaml,.yml,.css,.html,.rs,.java,.c,.h,.cpp,.sh" onChange={upload}/></label>
    <button className="secondary source-library-button" onClick={() => setShowPicker(true)}><Link2 size={16}/> 引用已有文件</button>
    <h3>已添加的来源 <em>{sources.length}</em></h3>{sources.length ? <div>{sources.map(source => <div className="source" key={source.id}><b>{(source.kind || source.mime || '').startsWith('image') ? 'IMG' : 'TXT'}</b><span>{source.relative_path || source.name}<small>{source.size ? `${(source.size / 1024).toFixed(1)} KB` : source.source_type}</small></span><button aria-label="删除来源" onClick={async () => {
      if (!window.confirm(`解除来源“${source.relative_path || source.name}”的引用？`)) return;
      try { await api.deleteSource(workspace, source.id); setSources(items => items.filter(item => item.id !== source.id)); }
      catch (cause) { setError(messageOf(cause, '删除失败')); }
    }}><Trash2 size={16}/></button></div>)}</div> : <div className="empty-card">还没有来源。</div>}{showPicker && <FilePicker workspace={workspace} onClose={() => setShowPicker(false)} onAttached={source => { setSources(items => [...items, source]); setShowPicker(false); }}/>} 
  </section>;
}

function FilePicker({ workspace, onClose, onAttached }: { workspace: string; onClose: () => void; onAttached: (source: Source) => void }) {
  const [files, setFiles] = useState<FileObject[]>([]); const [error, setError] = useState(''); const [busy, setBusy] = useState('');
  useEffect(() => { api.files().then(setFiles).catch(cause => setError(messageOf(cause, '无法读取文件系统'))); }, []);
  const attach = async (file: FileObject) => {
    const relativePath = window.prompt('在此工作区中的来源路径', file.name);
    if (!relativePath?.trim()) return;
    setBusy(file.id);
    try { onAttached(await api.attachSource(workspace, file.id, relativePath.trim())); }
    catch (cause) { setError(messageOf(cause, '引用失败')); }
    finally { setBusy(''); }
  };
  return <div className="modal"><div className="modal-inner model-drawer file-picker"><button className="close" aria-label="关闭" onClick={onClose}><X size={18}/></button><h2>引用已有文件</h2><p className="muted">选择后仅创建当前工作区的来源引用，不会复制文件内容。</p><ErrorBox error={error}/><div className="file-list">{files.length ? files.map(file => <div className="file-row" key={file.id}><b>{file.kind === 'image' ? 'IMG' : 'FILE'}</b><span>{file.name}<small>{(file.size / 1024).toFixed(1)} KB · 已被 {file.source_count} 个来源引用</small></span><button className="secondary" disabled={busy === file.id} onClick={() => void attach(file)}>{busy === file.id ? '引用中…' : '引用'}</button></div>) : <div className="empty-card">文件系统为空，请先在顶部添加文件。</div>}</div></div></div>;
}

function Studio({ workspace }: { workspace: string }) {
  const [feature, setFeature] = useState<'mindmap' | 'platforms' | 'site' | null>(null);
  const [root, setRoot] = useState<ReturnType<typeof normalizeMindmap>>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!feature) return;
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') setFeature(null); };
    window.addEventListener('keydown', closeOnEscape);
    return () => window.removeEventListener('keydown', closeOnEscape);
  }, [feature]);
  const close = () => { setFeature(null); setError(''); };
  return <section className="panel"><div className="studio-grid"><button className="studio-card mindmap-card" onClick={() => setFeature('mindmap')}><Workflow size={20}/><strong>思维导图</strong><small>整理来源的知识结构</small></button><button className="studio-card site-card" onClick={() => setFeature('platforms')}><Globe2 size={20}/><strong>静态网站</strong><small>构建并发布工作区内容</small></button></div>
    {feature && <div className="modal studio-modal" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) close(); }}><div className="modal-inner studio-modal-inner" role="dialog" aria-modal="true" aria-labelledby="studio-dialog-title"><button className="close" aria-label="关闭 Studio" onClick={close}><X size={18}/></button>
      {feature === 'site' ? <><button className="back-button" onClick={() => setFeature('platforms')}>← 返回平台</button><h2 id="studio-dialog-title">静态网站</h2><Publish workspace={workspace}/></> : feature === 'platforms' ? <><h2 id="studio-dialog-title">选择部署平台</h2><p className="muted">选择用于构建和发布网站的部署目标。</p><div className="platform-list"><button className="platform-option" onClick={() => setFeature('site')}><Globe2 size={22}/><span><strong>GitHub Pages</strong><small>使用已连接 GitHub 账号发布静态网站</small></span><span>›</span></button><div className="platform-option disabled"><Globe2 size={22}/><span><strong>Vercel</strong><small>平台适配尚未启用</small></span><span>—</span></div></div></> : <><button className="back-button" onClick={close}>← 返回 Studio</button><ErrorBox error={error}/><div className="studio-head"><div><h2 id="studio-dialog-title">思维导图</h2><p>从来源路径和 Markdown 标题生成知识结构。</p></div><button className="primary" disabled={busy || !workspace} onClick={async () => {
        setBusy(true); try { setRoot(normalizeMindmap(await api.mindmap(workspace))); setError(''); } catch (cause) { setError(messageOf(cause, '生成失败')); } finally { setBusy(false); }
      }}>{busy ? '生成中…' : '生成思维导图'}</button></div>{root ? <div className="mindmap"><Tree node={root}/></div> : <div className="empty-card">生成后将在此显示真实来源结构。</div>}</>}
    </div></div>}
  </section>;
}
function Tree({ node }: { node: MindmapNode }): React.ReactNode { return <ul><li><span>{node.text}</span>{node.children?.map((child, index) => <Tree node={child} key={`${child.text}-${index}`}/>)}</li></ul>; }

function Publish({ workspace }: { workspace: string }) {
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState('');
  const [github, setGithub] = useState<GitHubStatus | null>(null);
  const [repositories, setRepositories] = useState<GitHubRepository[]>([]);
  const [branches, setBranches] = useState<GitHubBranch[]>([]);
  const [selectedRepo, setSelectedRepo] = useState('');
  const [branch, setBranch] = useState('');
  const [newRepo, setNewRepo] = useState({ name: '', private: true, description: '' });
  const [creating, setCreating] = useState(false);
  const [themes, setThemes] = useState<SiteTheme[]>([]);
  const [themeID, setThemeID] = useState('astro-default');
  const [installing, setInstalling] = useState('');
  const [showThemeImport, setShowThemeImport] = useState(false);
  const [themeImport, setThemeImport] = useState({ git_url: '', ref: '', name: '', preview_url: '', description: '' });
  const selected = repositories.find(repository => repository.name === selectedRepo);
  useEffect(() => {
    api.githubStatus().then(value => {
      setGithub(value);
      if (value.connected) api.githubRepositories().then(setRepositories).catch(cause => setError(messageOf(cause, '无法加载 GitHub 仓库')));
    }).catch(cause => setError(messageOf(cause, '无法读取 GitHub 授权状态')));
  }, []);
  useEffect(() => { api.themes().then(setThemes).catch(cause => setError(messageOf(cause, '无法加载主题库'))); }, []);
  useEffect(() => {
    if (!selectedRepo || selectedRepo === '__new__') { setBranches([]); setBranch(''); return; }
    setBranch('');
    api.githubBranches(selectedRepo).then(setBranches).catch(cause => setError(messageOf(cause, '无法加载仓库分支')));
  }, [selectedRepo]);
  useEffect(() => {
    if (!job || job.status === 'succeeded' || job.status === 'failed') return;
    const timer = window.setInterval(() => api.job(job.id).then(setJob).catch(cause => setError(messageOf(cause, '无法查询发布状态'))), 3000);
    return () => window.clearInterval(timer);
  }, [job]);
  const canPublish = Boolean(workspace && github?.connected && themes.find(theme => theme.id === themeID)?.installed && (selected ? selected.name : newRepo.name.trim()) && (selected || newRepo.name.trim()));
  const createRepository = async () => {
    if (!newRepo.name.trim()) return;
    setCreating(true);
    try {
      const repository = await api.githubCreateRepository({ ...newRepo, name: newRepo.name.trim(), description: newRepo.description.trim() || undefined });
      setRepositories(items => [...items, repository]); setSelectedRepo(repository.name); setNewRepo({ name: '', private: true, description: '' }); setError('');
    } catch (cause) { setError(messageOf(cause, '创建 GitHub 仓库失败')); } finally { setCreating(false); }
  };
  const publish = async () => {
    const repo = selected || (newRepo.name.trim() ? await (async () => { setCreating(true); try { return await api.githubCreateRepository({ ...newRepo, name: newRepo.name.trim(), description: newRepo.description.trim() || undefined }); } finally { setCreating(false); } })() : null);
    if (!repo) return;
    try {
      if (!selected) { setRepositories(items => [...items, repo]); setSelectedRepo(repo.name); }
      setJob(await api.publish(workspace, { theme: themeID, target: 'github-pages', repo: repo.name, branch: branch || 'gh-pages' })); setError('');
    } catch (cause) { setError(messageOf(cause, '发布失败')); }
  };
  const selectedTheme = themes.find(theme => theme.id === themeID);
  return <section className="panel publish-panel"><ErrorBox error={error}/><h3 className="section-title">发布到 GitHub Pages</h3>
    <div className="theme-library"><div className="theme-library-head"><strong>主题</strong><span>{selectedTheme?.engine === 'hugo' ? 'Hugo' : 'Astro'}</span><button className="secondary" onClick={() => setShowThemeImport(true)}>从 Git 链接导入</button></div><div className="theme-list">{themes.map(theme => <div className={`theme-item ${theme.id === themeID ? 'selected' : ''}`} key={theme.id}><button className="theme-choice" onClick={() => theme.installed && setThemeID(theme.id)} disabled={!theme.installed}><span><strong>{theme.name}</strong><small>{theme.description || theme.engine}</small>{theme.repository && <small>{theme.repository}@{theme.commit || theme.ref}</small>}</span>{theme.installed ? <i>已安装</i> : <i>需安装</i>}</button><div className="theme-item-actions">{theme.preview_url && <a href={theme.preview_url} target="_blank" rel="noreferrer">预览</a>}{!theme.installed && <button className="secondary" disabled={installing === theme.id} onClick={async () => { setInstalling(theme.id); try { const installed = await api.installTheme(theme.id); setThemes(items => items.map(item => item.id === installed.id ? installed : item)); setThemeID(installed.id); } catch (cause) { setError(messageOf(cause, '主题安装失败')); } finally { setInstalling(''); } }}>{installing === theme.id ? '安装中…' : '安装'}</button>}</div></div>)}</div></div>
    <div className="github-connection">{github?.connected ? <><span className="live-dot"/> 已连接 GitHub：{github.login}<button className="secondary" onClick={async () => { try { await api.githubDisconnect(); setGithub({ connected: false }); setRepositories([]); setSelectedRepo(''); } catch (cause) { setError(messageOf(cause, '断开 GitHub 失败')); } }}>断开</button></> : <><span>未连接 GitHub</span><button className="secondary" onClick={api.githubConnect}>连接 GitHub</button></>}</div>
    {github?.connected && <>
      <label className="field">仓库<select value={selectedRepo} onChange={event => setSelectedRepo(event.target.value)}><option value="">选择已有仓库</option>{repositories.map(repository => <option key={repository.name} value={repository.name}>{repository.name}{repository.private ? ' · 私有' : ''}</option>)}<option value="__new__">创建新仓库</option></select></label>
      {selectedRepo === '__new__' && <div className="github-new-repo"><label className="field">仓库名称<input value={newRepo.name} onChange={event => setNewRepo({ ...newRepo, name: event.target.value })}/></label><label className="field github-checkbox"><input type="checkbox" checked={newRepo.private} onChange={event => setNewRepo({ ...newRepo, private: event.target.checked })}/> 私有仓库</label><label className="field">描述（可选）<input value={newRepo.description} onChange={event => setNewRepo({ ...newRepo, description: event.target.value })}/></label><button className="secondary" disabled={!newRepo.name.trim() || creating} onClick={() => void createRepository()}>{creating ? '创建中…' : '创建仓库'}</button></div>}
      {selected && <label className="field">分支<select value={branch} onChange={event => setBranch(event.target.value)}><option value="">使用 default branch（{selected.default_branch}）</option>{branches.filter(item => item.name !== selected.default_branch).map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</select></label>}
      <button className="primary" disabled={!canPublish || creating} onClick={() => void publish()}>发布到 GitHub Pages</button>
    </>}
    {job && <div className="job"><span className={`status ${job.status}`}/><div><strong>作业状态：{job.status}</strong>{job.error && <small>{job.error}</small>}{job.url && <a href={job.url} target="_blank" rel="noreferrer">打开站点 →</a>}</div></div>}
    {showThemeImport && <ThemeImportModal value={themeImport} busy={installing === 'import'} onChange={setThemeImport} onClose={() => setShowThemeImport(false)} onSubmit={async () => { if (!themeImport.git_url.trim() || installing) return; setInstalling('import'); try { const installed = await api.importTheme({ ...themeImport, git_url: themeImport.git_url.trim(), ref: themeImport.ref.trim() || undefined, name: themeImport.name.trim() || undefined, preview_url: themeImport.preview_url.trim() || undefined, description: themeImport.description.trim() || undefined }); setThemes(items => [installed, ...items]); setThemeID(installed.id); setThemeImport({ git_url: '', ref: '', name: '', preview_url: '', description: '' }); setShowThemeImport(false); } catch (cause) { setError(messageOf(cause, '主题导入失败')); } finally { setInstalling(''); } }}/>} 
  </section>;
}

type ThemeImport = { git_url: string; ref: string; name: string; preview_url: string; description: string };

function ThemeImportModal({ value, busy, onChange, onClose, onSubmit }: { value: ThemeImport; busy: boolean; onChange: (value: ThemeImport) => void; onClose: () => void; onSubmit: () => void | Promise<void> }) {
  return <div className="modal nested-modal" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget && !busy) onClose(); }}><form className="modal-inner theme-import-modal" role="dialog" aria-modal="true" aria-labelledby="theme-import-title" onSubmit={event => { event.preventDefault(); void onSubmit(); }}><button className="close" type="button" aria-label="关闭导入主题窗口" disabled={busy} onClick={onClose}><X size={18}/></button><h2 id="theme-import-title">从 Git 链接导入主题</h2><p className="muted">仅导入你信任的 GitHub HTTPS 仓库；私有仓库需要先连接 GitHub。</p><label className="field">Git 仓库链接<input required type="url" autoFocus placeholder="https://github.com/owner/repository.git" value={value.git_url} onChange={event => onChange({ ...value, git_url: event.target.value })}/></label><label className="field">分支或标签（可选）<input placeholder="默认分支" value={value.ref} onChange={event => onChange({ ...value, ref: event.target.value })}/></label><details className="theme-import-details"><summary>添加主题信息（可选）</summary><label className="field">主题名称<input value={value.name} onChange={event => onChange({ ...value, name: event.target.value })}/></label><label className="field">预览地址<input type="url" placeholder="https://…" value={value.preview_url} onChange={event => onChange({ ...value, preview_url: event.target.value })}/></label></details><div className="actions"><button type="button" className="secondary" disabled={busy} onClick={onClose}>取消</button><button className="primary" disabled={busy || !value.git_url.trim()}>{busy ? '导入中…' : '导入并安装'}</button></div></form></div>;
}

function Models({ models, setModels }: { models: Model[]; setModels: React.Dispatch<React.SetStateAction<Model[]>> }) {
  const [editing, setEditing] = useState<Model | null>(null);
  const [error, setError] = useState('');
  const [status, setStatus] = useState<'idle' | 'loading' | 'success'>('idle');
  const [discovered, setDiscovered] = useState<DiscoveredModel[]>([]);
  const [form, setForm] = useState<Partial<Model>>({ provider: 'openai', name: '', model: '', api_key: '', base_url: '' });
  const providerInfo: Record<Model['provider'], { label: string; keyLabel: string; endpointLabel: string; endpoint: string }> = {
    openai: { label: 'OpenAI', keyLabel: 'OpenAI API Key', endpointLabel: 'API Base URL（兼容服务时填写）', endpoint: 'https://api.openai.com/v1' },
    deepseek: { label: 'DeepSeek', keyLabel: 'DeepSeek API Key', endpointLabel: 'API Base URL（可选）', endpoint: 'https://api.deepseek.com/v1' },
    gemini: { label: 'Gemini', keyLabel: 'Google AI API Key', endpointLabel: 'Google AI API Host（可选）', endpoint: 'https://generativelanguage.googleapis.com' },
  };
  const provider = (form.provider || 'openai') as Model['provider'];
  const info = providerInfo[provider];
  const update = (value: Partial<Model>, invalidate = false) => { setForm(current => ({ ...current, ...value, ...(invalidate ? { model: '' } : {}) })); if (invalidate) { setStatus('idle'); setDiscovered([]); } setError(''); };
  const connect = async () => {
    if (!form.api_key?.trim() && !editing?.id) { setError(`请输入${info.keyLabel}`); return; }
    setStatus('loading'); setError('');
    try {
      const result = await api.discoverModels({ provider, api_key: form.api_key?.trim() || undefined, base_url: form.base_url?.trim() || undefined, model_id: editing?.id || undefined });
      const values = Array.isArray(result) ? result : result.models;
      setDiscovered(values.filter(value => value && typeof value.id === 'string' && typeof value.name === 'string'));
      setStatus('success');
    } catch (cause) { setStatus('idle'); setError(messageOf(cause, '连接失败，请检查 API Key 和端点')); }
  };
  const save = async () => {
    if (!form.name?.trim() || !form.model?.trim()) { setError('请填写配置名称并选择模型'); return; }
    try { const saved = editing?.id ? await api.updateModel(editing.id, form) : await api.createModel(form); setModels(items => editing?.id ? items.map(item => item.id === saved.id ? saved : item) : [...items, saved]); setEditing(null); setError(''); }
    catch (cause) { setError(messageOf(cause, '模型保存失败')); }
  };
  const openNew = () => { setForm({ provider: 'openai', name: '', model: '', api_key: '', base_url: '' }); setEditing({ id: '', provider: 'openai', name: '', model: '' }); setStatus('idle'); setDiscovered([]); setError(''); };
  const openEdit = (model: Model) => { setEditing(model); setForm({ ...model, api_key: '', model: model.model }); setStatus('idle'); setDiscovered([{ id: model.model, name: model.model }]); setError(''); };
  return <section className="panel"><ErrorBox error={error}/>
    <div className="studio-head"><div><h2>模型配置</h2><p>API Key 加密后由服务端保存，读取接口不会回显。</p></div><button className="primary" onClick={openNew}>添加模型</button></div>
    {models.map(model => <div className="model" key={model.id}><div><strong>{model.name}</strong><small>{providerInfo[model.provider].label} · {model.model}</small></div><button onClick={() => openEdit(model)}>编辑</button><button aria-label="删除模型" onClick={async () => { if (!window.confirm(`删除模型“${model.name}”？`)) return; try { await api.deleteModel(model.id); setModels(items => items.filter(item => item.id !== model.id)); } catch (cause) { setError(messageOf(cause, '删除失败')); } }}><Trash2 size={16}/></button></div>)}
    {editing && <div className="modal"><div className="modal-inner model-config-modal"><button className="close" aria-label="关闭" onClick={() => setEditing(null)}><X size={18}/></button><h3>{editing.id ? '编辑模型' : '添加模型'}</h3><p className="muted">先连接提供商获取可用模型，再保存所选配置。</p><label className="field">提供商<select value={provider} onChange={event => update({ provider: event.target.value as Model['provider'], model: '' }, true)}><option value="openai">OpenAI</option><option value="deepseek">DeepSeek</option><option value="gemini">Gemini</option></select></label><label className="field">配置名称<input value={form.name || ''} placeholder={`${info.label} 主配置`} onChange={event => update({ name: event.target.value })}/></label><label className="field">{info.keyLabel}<input type="password" autoComplete="new-password" value={form.api_key || ''} placeholder={editing.id ? '留空以使用已保存的 Key' : '输入 API Key'} onChange={event => update({ api_key: event.target.value }, true)}/></label><label className="field">{info.endpointLabel}<input value={form.base_url || ''} placeholder={info.endpoint} onChange={event => update({ base_url: event.target.value }, true)}/></label><button className="secondary connect-button" disabled={status === 'loading'} onClick={() => void connect()}>{status === 'loading' ? '连接中…' : '连接并获取模型'}</button>{status === 'success' && <div className="success" role="status">已连接，找到 {discovered.length} 个模型</div>}{(discovered.length > 0 || status === 'success') && <label className="field">模型<select value={form.model || ''} onChange={event => setForm(current => ({ ...current, model: event.target.value }))}><option value="">选择模型</option>{discovered.map(model => <option key={model.id} value={model.id}>{model.name}</option>)}</select><small>选择后点击“保存配置”</small></label>}<div className="actions"><button className="secondary" onClick={() => setEditing(null)}>取消</button><button className="primary" disabled={!form.name?.trim() || !form.model?.trim()} onClick={() => void save()}>保存配置</button></div></div></div>}
  </section>;
}

createRoot(document.getElementById('root')!).render(<AuthGate/>);
