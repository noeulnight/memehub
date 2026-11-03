// @vitest-environment happy-dom
import React, { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AxiosError } from 'axios';
import { toast } from 'sonner';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test';
import { ApiContext, createApi } from '../../../shared/api/client';
import { MemeCard, type Meme } from '../../../entities/meme';
import type { Collection } from '../../../entities/collection';
import { SaveToCollection } from './SaveToCollection';
import { MemeLibrary } from '../../../widgets/meme-library';
import { desktopHover } from '../../../shared/lib/desktop-hover';
import { Toaster } from '../../../shared/ui/sonner';

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
let root: Root | undefined;
let client: QueryClient;
const nativeMatchMedia = window.matchMedia.bind(window);

function mockDesktop(desktop: boolean) {
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => {
    const media = nativeMatchMedia(query);
    Object.defineProperty(media, 'matches', {
      value:
        query === desktopHover
          ? desktop
          : query === '(prefers-reduced-motion: reduce)' || media.matches,
    });
    return media;
  });
}

beforeEach(() => mockDesktop(false));

afterEach(async () => {
  await act(async () => {
    toast.dismiss();
    root?.unmount();
  });
  client?.clear();
  document.body.innerHTML = '';
  window.history.replaceState(null, '', '/');
  sessionStorage.clear();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function render(element: React.ReactNode, api: ReturnType<typeof createApi>) {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  await act(async () =>
    root!.render(
      <ApiContext.Provider value={api}>
        <QueryClientProvider client={client}>
          {element}
          <Toaster />
        </QueryClientProvider>
      </ApiContext.Provider>,
    ),
  );
}

async function until(condition: () => boolean) {
  for (let i = 0; i < 100 && !condition(); i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5));
    });
  }
  expect(condition()).toBe(true);
}

async function click(element: Element | null) {
  expect(element).not.toBeNull();
  await act(async () => (element as HTMLElement).click());
}

describe('collection interactions', () => {
  it('opens the picker from the library and browses saved memes from the account menu', async () => {
    const api = createApi('/api');
    let saved = false;
    const filtered: string[] = [];
    const meme = {
      id: 'image',
      content_type: 'image',
      title: 'Cat',
      tags: [],
      created_at: '2026-11-02T00:00:00Z',
      created_by: 'other',
      analysis_status: 'completed',
    };
    api.defaults.adapter = async (config) => {
      let data: unknown;
      if (config.url === 'auth/me')
        data = { id: 'owner', display_name: 'Collector', is_admin: false };
      else if (config.url === 'memes') {
        if (config.params.collection) filtered.push(config.params.collection);
        data = { items: [meme], next_cursor: null };
      } else if (config.url === 'memes/image/media') {
        const media = {
          url: 'https://example.test/cat.png',
          expires_at: new Date(Date.now() + 90_000).toISOString(),
        };
        data = { original: media, thumbnail: media };
      } else if (config.url === 'collections')
        data = {
          items: [
            { id: 'favorites', name: 'Favorites', contains_meme: saved, meme_count: saved ? 1 : 0 },
          ],
          next_cursor: null,
        };
      else if (config.url === 'collections/favorites')
        data = { id: 'favorites', name: 'Favorites', meme_count: 1 };
      else if (config.url === 'collections/favorites/memes/image' && config.method === 'put')
        saved = true;
      else throw new Error(`Unexpected request: ${config.method} ${config.url}`);
      return { config, status: 200, statusText: 'OK', headers: {}, data };
    };
    await render(<MemeLibrary />, api);
    await until(() => !!document.querySelector('article [aria-label="Add to collection"]'));
    await click(document.querySelector('article [aria-label="Add to collection"]'));
    await until(() => !!document.querySelector('[aria-label="Add to Favorites"]'));
    await click(document.querySelector('[aria-label="Add to Favorites"]'));
    await until(() => !!document.querySelector('[aria-label="Remove from Favorites"]'));
    await until(
      () =>
        document
          .querySelector('[data-sonner-toast][data-type="success"]')
          ?.textContent?.includes('Saved to “Favorites”.') === true,
    );
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain(
      'Saved to “Favorites”.',
    );
    await click(document.querySelector('[role="dialog"] [data-slot="dialog-close"]'));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.querySelector('[data-sonner-toast]')?.textContent).toContain(
      'Saved to “Favorites”.',
    );
    await act(async () => {
      document
        .querySelector('[aria-label="Account menu"]')!
        .dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, button: 0 }));
    });
    await until(() =>
      Array.from(document.querySelectorAll('[role="menuitem"]')).some((item) =>
        item.textContent?.includes('My collections'),
      ),
    );
    await click(
      Array.from(document.querySelectorAll('[role="menuitem"]')).find((item) =>
        item.textContent?.includes('My collections'),
      )!,
    );
    await until(() =>
      Array.from(document.querySelectorAll('[role="dialog"] button')).some((button) =>
        button.textContent?.includes('Favorites'),
      ),
    );
    await click(
      Array.from(document.querySelectorAll('[role="dialog"] button')).find((button) =>
        button.textContent?.includes('Favorites'),
      )!,
    );
    await until(
      () =>
        document.querySelector('h1')?.textContent === 'Favorites' && filtered.includes('favorites'),
    );
    expect(window.location.search).toContain('collection=favorites');
  });

  it('adds a fourth image action and keeps it separate from opening meme details', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => ({
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: {
        original: {
          url: 'https://example.test/original.png',
          expires_at: new Date(Date.now() + 90_000).toISOString(),
        },
        thumbnail: {
          url: 'https://example.test/thumbnail.png',
          expires_at: new Date(Date.now() + 90_000).toISOString(),
        },
      },
    });
    const save = vi.fn();
    const meme = {
      id: 'image',
      content_type: 'image',
      title: 'Cat',
      tags: [],
      created_at: '2026-11-02T00:00:00Z',
    } as unknown as Meme;
    await render(
      <MemeCard
        meme={meme}
        index={0}
        onTagSearch={() => {}}
        onUserSearch={() => {}}
        onSave={save}
      />,
      api,
    );
    const actions = document.querySelector('article')!;
    expect(actions.querySelectorAll('button')).toHaveLength(4);
    await click(actions.querySelector('[aria-label="Add to collection"]'));
    expect(save).toHaveBeenCalledOnce();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it('preserves desktop click-to-copy and adds collections to the hold-for-options menu', async () => {
    mockDesktop(true);
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    const save = vi.fn();
    const meme = {
      id: 'text',
      content_type: 'text',
      title: 'Reaction',
      body_text: 'A saved reaction',
      tags: [],
      created_at: '2026-11-02T00:00:00Z',
    } as unknown as Meme;
    await render(
      <MemeCard
        meme={meme}
        index={0}
        onTagSearch={() => {}}
        onUserSearch={() => {}}
        onSave={save}
      />,
      createApi('/api'),
    );
    vi.useFakeTimers();
    const hover = document.querySelector('.meme-hover')!;
    await act(async () => {
      hover.dispatchEvent(new PointerEvent('pointerover', { bubbles: true, pointerType: 'mouse' }));
    });
    expect(hover.getAttribute('data-hovered')).toBe('true');
    const card = document.querySelector('article button')!;
    expect(card.getAttribute('aria-label')).toContain('Hold for options');
    await act(async () => {
      card.dispatchEvent(
        new PointerEvent('pointerdown', {
          bubbles: true,
          button: 0,
          isPrimary: true,
          pointerType: 'mouse',
        }),
      );
      await vi.advanceTimersByTimeAsync(500);
      card.dispatchEvent(
        new PointerEvent('pointerup', {
          bubbles: true,
          button: 0,
          isPrimary: true,
          pointerType: 'mouse',
        }),
      );
    });
    await click(card);
    expect(copy).not.toHaveBeenCalled();
    const options = document.querySelector('[role="group"][aria-label="Meme options"]')!;
    expect(options.querySelectorAll('button')).toHaveLength(4);
    await click(options.querySelector('[aria-label="Add to collection"]'));
    expect(save).toHaveBeenCalledOnce();
    expect(document.querySelector('[aria-label="Meme options"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    await click(card);
    expect(copy).toHaveBeenCalledWith('A saved reaction');
  });

  it('creates and saves inside the popup, shows membership, then removes the meme', async () => {
    const api = createApi('/api');
    const collections: Collection[] = [];
    const writes: unknown[] = [];
    api.defaults.adapter = async (config) => {
      let data: unknown;
      if (config.url === 'auth/me') data = { id: 'user-one', is_admin: false };
      else if (config.url === 'collections' && config.method === 'get')
        data = { items: structuredClone(collections), next_cursor: null };
      else if (config.url === 'collections' && config.method === 'post') {
        const input = JSON.parse(config.data);
        writes.push(input);
        const collection = {
          id: 'cats',
          name: input.name,
          meme_count: 1,
          contains_meme: true,
          created_at: '',
          updated_at: '',
        };
        collections.push(collection);
        data = collection;
      } else if (config.url === 'collections/cats/memes/image' && config.method === 'delete') {
        collections[0].contains_meme = false;
        collections[0].meme_count = 0;
      } else throw new Error(`Unexpected request: ${config.method} ${config.url}`);
      return { config, status: 200, statusText: 'OK', headers: {}, data };
    };
    const close = vi.fn();
    await render(<SaveToCollection memeId="image" onClose={close} />, api);
    await until(() => !!document.querySelector('input[name="collection-name"]'));
    const input = document.querySelector<HTMLInputElement>('input[name="collection-name"]')!;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(
        input,
        '  고양이 모음  ',
      );
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await click(document.querySelector('button[aria-label="Create & save"]'));
    await until(() => !!document.querySelector('[aria-label="Remove from 고양이 모음"]'));
    expect(writes).toEqual([{ name: '고양이 모음', meme_id: 'image' }]);
    expect(
      document
        .querySelector('[aria-label="Remove from 고양이 모음"]')
        ?.getAttribute('aria-pressed'),
    ).toBe('true');
    await click(document.querySelector('[aria-label="Remove from 고양이 모음"]'));
    await until(() => !!document.querySelector('[aria-label="Add to 고양이 모음"]'));
    expect(
      document.querySelector('[aria-label="Add to 고양이 모음"]')?.getAttribute('aria-pressed'),
    ).toBe('false');
    await until(() =>
      Array.from(document.querySelectorAll('[data-sonner-toast][data-type="success"]')).some(
        (toast) => toast.textContent?.includes('Removed from “고양이 모음”.'),
      ),
    );
    expect(document.querySelector('[role="dialog"]')?.textContent).not.toContain(
      'Removed from “고양이 모음”.',
    );
    expect(
      Array.from(document.querySelectorAll('button')).some(
        (button) => button.textContent === 'Done',
      ),
    ).toBe(false);
    await click(document.querySelector('[role="dialog"] [data-slot="dialog-close"]'));
    expect(close).toHaveBeenCalledOnce();
  });

  it('prompts guests to sign in without requesting private collections', async () => {
    const api = createApi('/api');
    const requests: string[] = [];
    api.defaults.adapter = async (config) => {
      requests.push(config.url!);
      throw new AxiosError('Login required', 'ERR_BAD_REQUEST', config, undefined, {
        config,
        status: 401,
        statusText: 'Unauthorized',
        headers: {},
        data: { error: 'login_required' },
      });
    };
    await render(<SaveToCollection memeId="image" onClose={() => {}} />, api);
    await until(() => !!document.querySelector('a'));
    expect(document.querySelector('a')?.getAttribute('href')).toContain('/api/auth/login');
    expect(document.querySelector('input')).toBeNull();
    expect(requests).toEqual(['auth/me']);
  });

  it('can retry a failed save to an existing collection without falsely marking it saved', async () => {
    const api = createApi('/api');
    let attempts = 0;
    let saved = false;
    api.defaults.adapter = async (config) => {
      let data: unknown;
      if (config.url === 'auth/me') data = { id: 'user-one', is_admin: false };
      else if (config.method === 'get')
        data = {
          items: [
            { id: 'favorites', name: 'Favorites', meme_count: saved ? 1 : 0, contains_meme: saved },
          ],
          next_cursor: null,
        };
      else {
        expect(config.method).toBe('put');
        expect(config.url).toBe('collections/favorites/memes/image');
        if (++attempts === 1) throw new AxiosError('Offline');
        saved = true;
      }
      return { config, status: 200, statusText: 'OK', headers: {}, data };
    };
    await render(<SaveToCollection memeId="image" onClose={() => {}} />, api);
    await until(() => !!document.querySelector('[aria-label="Add to Favorites"]'));
    await click(document.querySelector('[aria-label="Add to Favorites"]'));
    await until(() => !!document.querySelector('[data-sonner-toast][data-type="error"]'));
    expect(document.querySelector('[role="dialog"] [role="alert"]')).toBeNull();
    expect(
      document.querySelector('[aria-label="Add to Favorites"]')?.getAttribute('aria-pressed'),
    ).toBe('false');
    await click(document.querySelector('[aria-label="Add to Favorites"]'));
    await until(() => !!document.querySelector('[aria-label="Remove from Favorites"]'));
    expect(attempts).toBe(2);
    await until(() => !!document.querySelector('[data-sonner-toast][data-type="success"]'));
    expect(document.querySelector('[role="dialog"] [role="alert"]')).toBeNull();
  });

  it('does not display another account’s cached collections after the session changes', async () => {
    const api = createApi('/api');
    let currentUser = 'one';
    let release: () => void = () => {};
    api.defaults.adapter = async (config) => {
      if (config.url === 'auth/me')
        return { config, status: 200, statusText: 'OK', headers: {}, data: { id: currentUser } };
      if (currentUser === 'two')
        await new Promise<void>((resolve) => {
          release = resolve;
        });
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: {
          items: [
            {
              id: currentUser,
              name: `${currentUser}'s folder`,
              contains_meme: false,
              meme_count: 0,
            },
          ],
          next_cursor: null,
        },
      };
    };
    await render(<SaveToCollection memeId="image" onClose={() => {}} />, api);
    await until(() => document.body.textContent!.includes("one's folder"));
    currentUser = 'two';
    await act(async () => {
      client.setQueryData(['session'], { id: 'two' });
    });
    await until(() => !document.body.textContent!.includes("one's folder"));
    await act(async () => release());
    await until(() => document.body.textContent!.includes("two's folder"));
  });
});
