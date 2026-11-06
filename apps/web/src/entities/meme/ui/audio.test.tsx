// @vitest-environment happy-dom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, it } from 'vite-plus/test';
import { ApiContext, createApi } from '../../../shared/api/client';
import { MemeCard } from './MemeCard';
import type { Meme } from '../model/types';

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
it('renders audio controls outside buttons without autoplay or eager media downloads', async () => {
  const api = createApi('/api');
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: {
      original: { url: 'https://media.example/sound.mp3' },
      thumbnail: { url: 'https://media.example/sound.mp3' },
    },
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['meme-media', 'audio'], {
    original: { url: 'https://media.example/sound.mp3' },
    thumbnail: { url: 'https://media.example/sound.mp3' },
  });
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  const meme: Meme = {
    id: 'audio',
    created_by: 'owner',
    created_by_username: null,
    created_by_name: null,
    content_type: 'audio',
    title: 'Sound',
    body_text: null,
    description: '',
    extracted_text: '',
    source_url: null,
    source_name: null,
    source_author: null,
    source_note: null,
    analysis_status: 'not_requested',
    content_revision: 0,
    created_at: '2026-11-06T00:00:00Z',
    tags: [],
    mime_type: 'audio/mpeg',
  };
  try {
    await act(async () => {
      root.render(
        <ApiContext.Provider value={api}>
          <QueryClientProvider client={client}>
            <MemeCard
              meme={meme}
              index={0}
              onTagSearch={() => {}}
              onUserSearch={() => {}}
              onSave={() => {}}
            />
          </QueryClientProvider>
        </ApiContext.Provider>,
      );
    });
    for (let i = 0; i < 20 && !host.querySelector('audio'); i++)
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 5));
      });
    const audio = host.querySelector('audio')!;
    expect(audio).not.toBeNull();
    expect(audio.hasAttribute('controls')).toBe(true);
    expect(audio.getAttribute('preload')).toBe('none');
    expect(audio.hasAttribute('autoplay')).toBe(false);
    expect(audio.closest('button')).toBeNull();
    expect(host.querySelector('button[aria-label="View audio details"]')).not.toBeNull();
  } finally {
    await act(async () => root.unmount());
    client.clear();
    host.remove();
  }
});
