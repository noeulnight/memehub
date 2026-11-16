// @vitest-environment happy-dom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expect, it, vi } from 'vite-plus/test';
import { ApiContext, createApi } from '../../../shared/api/client';
import { MemeLibrary } from '../../../widgets/meme-library';
import type { Meme } from '../../../entities/meme';
import { DoubleMeme } from './DoubleMeme';

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

async function until(condition: () => boolean) {
  for (let i = 0; i < 50 && !condition(); i++)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5));
    });
  expect(condition()).toBe(true);
}

it('starts from details, selects a second image, swaps and shares without a dock button', async () => {
  const api = createApi('/api');
  const requested: string[] = [];
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.spyOn(navigator.clipboard, 'writeText').mockImplementation(writeText);
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:double-test');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const left = {
    id: 'left',
    title: 'Left cat',
    content_type: 'image',
    mime_type: 'image/png',
    safety_rating: 'sfw',
    tags: [],
    content_revision: 0,
  } as unknown as Meme;
  const right = { ...left, id: 'right', title: 'Right cat', mime_type: 'image/gif' };
  api.defaults.adapter = async (config) => {
    const path = config.url || '';
    let data: unknown;
    if (path.includes('/double/')) {
      requested.push(path);
      data = new Blob(['gif'], { type: 'image/gif' });
    } else if (path === 'memes/left') data = left;
    else if (path === 'auth/me') data = { id: 'owner', is_admin: false };
    else if (path.endsWith('/media'))
      data = {
        original: { url: 'https://media.example/cat.png' },
        thumbnail: { url: 'https://media.example/cat.png' },
      };
    else data = { items: [left, right], next_cursor: null };
    return { config, status: 200, statusText: 'OK', headers: {}, data };
  };
  window.history.replaceState(null, '', '/?meme=left&tag=cats');
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  const click = async (element: Element | null | undefined) => {
    expect(element).toBeTruthy();
    await act(async () => (element as HTMLElement).click());
  };
  try {
    await act(async () =>
      root.render(
        <ApiContext.Provider value={api}>
          <QueryClientProvider client={client}>
            <MemeLibrary />
          </QueryClientProvider>
        </ApiContext.Provider>,
      ),
    );
    await until(() =>
      [...document.querySelectorAll('button')].some(
        (x) => x.textContent === 'Combine with another',
      ),
    );
    expect(host.querySelector('[aria-label="Double meme"]')).toBeNull();
    await click(
      [...document.querySelectorAll('button')].find(
        (x) => x.textContent === 'Combine with another',
      ),
    );
    await until(() => !!document.querySelector('[aria-label="Combine with: Right cat"]'));
    expect(document.querySelector('[aria-label="Combine with: Left cat"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(new URLSearchParams(window.location.search).get('tag')).toBe('cats');
    await click(document.querySelector('[aria-label="Combine with: Right cat"]'));
    await until(() => !!document.querySelector('img[alt="Combined double meme"]'));
    const button = (text: string) =>
      [...document.querySelectorAll('[role="dialog"] button')].find(
        (x) => x.textContent?.trim() === text,
      );
    await click(button('Copy media URL'));
    expect(writeText).toHaveBeenLastCalledWith(
      expect.stringContaining('/api/double/left/right/file.gif'),
    );
    await click(document.querySelector('[aria-label="Swap images"]'));
    await until(() => requested.some((url) => url.includes('/double/right/left/file.gif')));
    await click(button('Close'));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(host.querySelector('[aria-label="Double meme"]')).toBeNull();
  } finally {
    await act(async () => root.unmount());
    client.clear();
    host.remove();
    window.history.replaceState(null, '', '/');
    vi.restoreAllMocks();
  }
});

it('keeps combined NSFW content and copy actions hidden until explicitly revealed', async () => {
  const api = createApi('/api');
  const client = new QueryClient();
  const left = {
    id: 'left',
    title: 'Left',
    content_type: 'image',
    mime_type: 'image/png',
    safety_rating: 'nsfw',
  } as Meme;
  const right = { ...left, id: 'right', safety_rating: 'sfw' } as Meme;
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: new Blob(['png'], { type: 'image/png' }),
  });
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:double-nsfw');
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  try {
    await act(async () =>
      root.render(
        <ApiContext.Provider value={api}>
          <QueryClientProvider client={client}>
            <DoubleMeme left={left} right={right} onSwap={() => {}} onClose={() => {}} />
          </QueryClientProvider>
        </ApiContext.Provider>,
      ),
    );
    await until(() => !!document.querySelector('[inert] img[alt="Combined double meme"]'));
    const buttons = () => [
      ...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'),
    ];
    expect(buttons().find((x) => x.textContent?.includes('Copy media URL'))?.disabled).toBe(true);
    await act(async () =>
      buttons()
        .find((x) => x.textContent === 'Show NSFW')!
        .click(),
    );
    expect(document.querySelector('[inert]')).toBeNull();
    expect(buttons().find((x) => x.textContent?.includes('Copy media URL'))?.disabled).toBe(false);
  } finally {
    await act(async () => root.unmount());
    client.clear();
    host.remove();
    vi.restoreAllMocks();
  }
});
