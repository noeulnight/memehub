import { describe, expect, it } from 'vite-plus/test';
import { createApi } from '../../../shared/api/client';
import { createCollection, listCollections, setCollectionMeme } from './collections';
import { listMemes } from '../../meme/api/queries';

describe('collections API', () => {
  it('passes membership context, pagination and cancellation to the private list', async () => {
    const api = createApi('/api');
    const signal = new AbortController().signal;
    api.defaults.adapter = async (config) => {
      expect(config.params).toEqual({ limit: 50, cursor: 'next+/=', meme_id: 'meme-id' });
      expect(config.signal).toBe(signal);
      expect(config.withCredentials).toBe(true);
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { items: [], next_cursor: null },
      };
    };
    expect(await listCollections(api, 'next+/=', 'meme-id', signal)).toEqual({
      items: [],
      next_cursor: null,
    });
  });

  it('creates and saves in one request and uses idempotent membership operations', async () => {
    const api = createApi('/api');
    const calls: unknown[] = [];
    api.defaults.adapter = async (config) => {
      calls.push([config.method, config.url, config.data ? JSON.parse(config.data) : undefined]);
      expect(config.xsrfHeaderName).toBe('X-CSRF-Token');
      return { config, status: 200, statusText: 'OK', headers: {}, data: { id: 'new' } };
    };
    await createCollection(api, '  고양이 모음  ', 'meme-id');
    await setCollectionMeme(api, 'folder/one', 'meme/two', true);
    await setCollectionMeme(api, 'folder/one', 'meme/two', false);
    expect(calls).toEqual([
      ['post', 'collections', { name: '고양이 모음', meme_id: 'meme-id' }],
      ['put', 'collections/folder%2Fone/memes/meme%2Ftwo', undefined],
      ['delete', 'collections/folder%2Fone/memes/meme%2Ftwo', undefined],
    ]);
  });

  it('combines a collection with text, tag, type and cursor filters', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => {
      expect(config.params).toMatchObject({
        collection: 'my-folder',
        q: 'cat',
        tag: ['cute'],
        content_type: 'image',
        cursor: 'next',
      });
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { items: [], next_cursor: null },
      };
    };
    await listMemes(api, 'cat #cute', 'image', 'next', undefined, '', false, 'my-folder');
  });
});
