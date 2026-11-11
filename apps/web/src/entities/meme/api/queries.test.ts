import { describe, expect, it, vi } from 'vite-plus/test';
import { focusManager, QueryClient, QueryObserver } from '@tanstack/react-query';
import { AxiosError } from 'axios';
import { createApi } from '../../../shared/api/client';
import { listMemes, getMedia, memeMediaOptions, type Media } from './queries';

describe('meme queries', () => {
  it('sends filters, cursor and cancellation through Axios', async () => {
    const api = createApi('/api/');
    const signal = new AbortController().signal;
    api.defaults.adapter = async (config) => {
      expect(api.getUri(config)).toContain('q=100%25+%26+monday');
      expect(config.params.content_type).toBe('text');
      expect(config.params.cursor).toBe('cursor+/=');
      expect(config.signal).toBe(signal);
      expect(config.xsrfCookieName).toBe('memehub_csrf');
      expect(config.withCredentials).toBe(true);
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { items: [], next_cursor: null },
      };
    };
    expect(await listMemes(api, '  100% & monday  ', 'text', 'cursor+/=', signal)).toEqual({
      items: [],
      next_cursor: null,
    });
  });
  it('passes an exact tag filter separately from free text', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => {
      expect(config.params.tag).toEqual(['Work & life']);
      expect(config.params.q).toBeUndefined();
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { items: [], next_cursor: null },
      };
    };
    await listMemes(api, '', '', null, undefined, 'Work & life');
  });
  it('sends uploader and own-library filters independently from text', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => {
      expect(config.params.uploader).toBe('NoeulNight');
      expect(config.params.q).toBe('cat');
      expect(config.params.mine).toBe(true);
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { items: [], next_cursor: null },
      };
    };
    await listMemes(api, '@NoeulNight cat', '', null, undefined, '', true);
  });
  it('serializes typed and selected tags as repeated exact filters', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => {
      expect(config.params.tag).toEqual(['selected', 'work', 'Exacttag']);
      expect(api.getUri(config)).toContain('tag=selected&tag=work&tag=Exacttag');
      expect(config.params.q).toBe('cat');
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { items: [], next_cursor: null },
      };
    };
    await listMemes(api, '#work #"Exact tag" cat', '', null, undefined, 'selected');
  });
  it('propagates failures and rejects malformed pages', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async () => {
      throw new AxiosError('unavailable');
    };
    await expect(listMemes(api, '', '', null)).rejects.toThrow('unavailable');
    api.defaults.adapter = async (config) => ({
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { items: [] },
    });
    await expect(listMemes(api, '', '', null)).rejects.toThrow('unexpected response');
  });
  it('rejects unsafe or missing media URLs', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => ({
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { original: { url: 'javascript:alert(1)', expires_at: new Date().toISOString() } },
    });
    await expect(getMedia(api, 'image')).rejects.toThrow('Invalid image URL');
  });
});

describe('media URL lifetime', () => {
  it('keeps public URLs without expiry across focus and elapsed time', async () => {
    vi.useFakeTimers();
    const api = createApi('/api');
    let calls = 0;
    api.defaults.adapter = async (config) => {
      calls++;
      const image = { url: 'https://s3.example.test/memes/originals/cat.gif' };
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { original: image, thumbnail: image },
      };
    };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.mount();
    const observer = new QueryObserver(client, memeMediaOptions(api, 'public-image'));
    const unsubscribe = observer.subscribe(() => {});
    try {
      await vi.advanceTimersByTimeAsync(0);
      expect(observer.getCurrentResult().isSuccess).toBe(true);
      await vi.advanceTimersByTimeAsync(3_600_000);
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      await vi.advanceTimersByTimeAsync(0);
      expect(calls).toBe(1);
    } finally {
      unsubscribe();
      client.unmount();
      client.clear();
      focusManager.setFocused(undefined);
      vi.useRealTimers();
    }
  });

  it('reuses valid URLs on focus and remount, then refreshes before expiry', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-02T03:00:00Z'));
    const api = createApi('/api');
    let calls = 0;
    api.defaults.adapter = async (config) => {
      calls++;
      const image = {
        url: `https://images.example.test/preview.webp?signature=${calls}`,
        expires_at: new Date(Date.now() + 90_000).toISOString(),
      };
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { original: image, thumbnail: image },
      };
    };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.mount();
    let unsubscribe = () => {};
    try {
      const options = memeMediaOptions(api, 'image');
      let observer = new QueryObserver(client, options);
      unsubscribe = observer.subscribe(() => {});
      await vi.advanceTimersByTimeAsync(0);
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      await vi.advanceTimersByTimeAsync(0);
      expect(calls).toBe(1);
      unsubscribe();
      observer = new QueryObserver(client, options);
      unsubscribe = observer.subscribe(() => {});
      await vi.advanceTimersByTimeAsync(0);
      expect(calls).toBe(1);
      await vi.advanceTimersByTimeAsync(61_000);
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      await vi.advanceTimersByTimeAsync(0);
      expect(calls).toBe(2);
      expect(client.getQueryData<Media>(options.queryKey)?.thumbnail.url).toContain('signature=2');
    } finally {
      unsubscribe();
      client.unmount();
      client.clear();
      focusManager.setFocused(undefined);
      vi.useRealTimers();
    }
  });
});

it('sends reserved safety filters independently from ordinary tags and text', async () => {
  const api = createApi('/api');
  api.defaults.adapter = async (config) => {
    expect(config.params.safety_rating).toBe('nsfw');
    expect(config.params.tag).toEqual(['work']);
    expect(config.params.q).toBe('cat');
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: { items: [], next_cursor: null },
    };
  };
  await listMemes(api, '#NSFW #work cat', '', null);
  await listMemes(api, '#work cat', '', null, undefined, 'nsfw');
});
