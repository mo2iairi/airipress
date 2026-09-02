import { describe, expect, it, vi } from 'vitest';
import { api, auth, normalizeMindmap } from './api';

describe('API client', () => {
  it('normalizes supported mindmap envelopes', () => {
    expect(normalizeMindmap({ content: { root: { text: 'root', children: [{ title: 'child' }] } } })).toEqual({ text: 'root', children: [{ text: 'child', children: [] }] });
  });

  it('uses cookie credentials and write request marker', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => [] });
    vi.stubGlobal('fetch', fetchMock);
    await api.createWorkspace({ name: 'test' });
    expect(fetchMock.mock.calls[0][1].credentials).toBe('same-origin');
    expect(fetchMock.mock.calls[0][1].headers['X-Airipress-Request']).toBe('1');
  });

  it('logs in and logs out through the session endpoints', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal('fetch', fetchMock);
    await auth.login('admin', 'secret');
    await auth.logout();
    expect(fetchMock.mock.calls[0][0]).toContain('/auth/login');
    expect(fetchMock.mock.calls[1][1].method).toBe('DELETE');
  });

  it('exports and imports the complete data archive', async () => {
    const archive = new Blob(['zip']);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, blob: async () => archive })
      .mockResolvedValueOnce({ ok: true, status: 204 });
    vi.stubGlobal('fetch', fetchMock);
    expect(await api.exportData()).toBe(archive);
    await api.importData(new File(['zip'], 'backup.zip', { type: 'application/zip' }));
    expect(fetchMock.mock.calls[0][0]).toContain('/data/export');
    expect(fetchMock.mock.calls[1][0]).toContain('/data/import');
    expect(fetchMock.mock.calls[1][1].body).toBeInstanceOf(FormData);
  });

  it('lists global files and attaches an existing object as a workspace source', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => [] })
      .mockResolvedValueOnce({ ok: true, status: 201, json: async () => ({ id: 'source-1' }) });
    vi.stubGlobal('fetch', fetchMock);
    await api.files();
    await api.attachSource('workspace-1', 'file-1', 'notes/guide.md');
    expect(fetchMock.mock.calls[0][0]).toContain('/files');
    expect(fetchMock.mock.calls[1][0]).toContain('/workspaces/workspace-1/sources');
    expect(fetchMock.mock.calls[1][1].body).toContain('"file_id":"file-1"');
  });
});
