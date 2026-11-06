import { afterEach, describe, expect, it, vi } from 'vite-plus/test';
import axios from 'axios';
import { createApi } from '../../../shared/api/client';
import { createTextMeme, uploadFile, validateMediaFile } from './upload';

afterEach(() => vi.restoreAllMocks());
describe('image uploads', () => {
  it('hashes, reserves, uploads without API credentials, and completes in order', async () => {
    const api = createApi('/api');
    const steps: string[] = [];
    const input = {
      file: new File(['image bytes'], 'meme.png', { type: 'image/png' }),
      title: 'Meme',
      source_url: '',
      source_name: 'Source',
      source_author: 'Author',
    };
    api.defaults.adapter = async (config) => {
      steps.push(config.url!);
      if (config.url === 'memes') {
        const body = JSON.parse(config.data);
        expect(body.sha256).toMatch(/^[0-9a-f]{64}$/);
        expect(body.size_bytes).toBe(11);
        expect(body.source_author).toBe('Author');
        return {
          config,
          status: 201,
          statusText: 'Created',
          headers: {},
          data: {
            id: 'saved',
            upload: {
              url: 'https://storage.example/staging',
              method: 'PUT',
              headers: { 'Content-Type': 'image/png' },
            },
          },
        };
      }
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { id: 'saved', status: 'ready' },
      };
    };
    const put = vi.spyOn(axios, 'put').mockImplementation(async (_url, _file, options) => {
      steps.push('storage');
      expect(options?.withCredentials).toBe(false);
      expect(options?.headers).not.toHaveProperty('X-CSRF-Token');
      return { data: null };
    });
    expect(await uploadFile(api, input, () => {})).toEqual({ id: 'saved', status: 'ready' });
    expect(put).toHaveBeenCalledOnce();
    expect(steps).toEqual(['memes', 'storage', 'memes/saved/upload-completion']);
  });
  it('does not complete a failed storage upload', async () => {
    const api = createApi('/api');
    let calls = 0;
    api.defaults.adapter = async (config) => {
      calls++;
      return {
        config,
        status: 201,
        statusText: 'Created',
        headers: {},
        data: {
          id: 'saved',
          upload: { url: 'https://storage.example/staging', method: 'PUT', headers: {} },
        },
      };
    };
    vi.spyOn(axios, 'put').mockRejectedValue(new Error('storage failed'));
    await expect(
      uploadFile(
        api,
        {
          file: new File(['bytes'], 'm.png', { type: 'image/png' }),
          title: '',
          source_url: '',
          source_name: '',
          source_author: '',
        },
        () => {},
      ),
    ).rejects.toThrow('storage failed');
    expect(calls).toBe(1);
  });
});

describe('text memes', () => {
  it('saves text and source information without image storage requests', async () => {
    const api = createApi('/api');
    const put = vi.spyOn(axios, 'put');
    let requests = 0;
    api.defaults.adapter = async (config) => {
      requests++;
      expect(config.url).toBe('memes');
      expect(config.method).toBe('post');
      expect(JSON.parse(config.data)).toEqual({
        content_type: 'text',
        body_text: 'First line\nSecond line',
        title: 'A text meme',
        source_url: 'https://example.com/post',
        source_name: 'Example',
        source_author: 'Creator',
      });
      return {
        config,
        status: 201,
        statusText: 'Created',
        headers: {},
        data: { id: 'text-meme', content_type: 'text', body_text: 'First line\nSecond line' },
      };
    };
    const result = await createTextMeme(api, {
      body_text: 'First line\nSecond line',
      title: 'A text meme',
      source_url: 'https://example.com/post',
      source_name: 'Example',
      source_author: 'Creator',
    });
    expect(result.id).toBe('text-meme');
    expect(requests).toBe(1);
    expect(put).not.toHaveBeenCalled();
  });
  it('rejects blank and oversized text before sending a request', async () => {
    const api = createApi('/api');
    const adapter = vi.fn();
    api.defaults.adapter = adapter;
    for (const body_text of ['   \n', 'a'.repeat(10_001)]) {
      await expect(
        createTextMeme(api, {
          body_text,
          title: '',
          source_url: '',
          source_name: '',
          source_author: '',
        }),
      ).rejects.toThrow('Enter text');
    }
    expect(adapter).not.toHaveBeenCalled();
  });
});

describe('upload recovery', () => {
  const input = () => ({
    file: new File(['bytes'], 'm.png', { type: 'image/png' }),
    title: '',
    source_url: '',
    source_name: '',
    source_author: '',
  });
  it('renews the same reservation after a failed PUT and skips PUT after completion failure', async () => {
    const api = createApi('/api');
    const calls: string[] = [];
    let completeAttempts = 0;
    const upload = { url: 'https://storage.example/staging', method: 'PUT', headers: {} };
    api.defaults.adapter = async (config) => {
      calls.push(config.url!);
      if (config.url?.endsWith('/upload-completion') && completeAttempts++ === 0)
        throw new Error('completion failed');
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data:
          config.url === 'memes'
            ? { id: 'saved', upload }
            : config.url?.endsWith('/upload-url')
              ? upload
              : { id: 'saved' },
      };
    };
    const put = vi
      .spyOn(axios, 'put')
      .mockRejectedValueOnce(new Error('put failed'))
      .mockResolvedValue({ data: null });
    const checkpoint = {};
    const file = input();
    await expect(uploadFile(api, file, () => {}, undefined, { checkpoint })).rejects.toThrow(
      'put failed',
    );
    await expect(uploadFile(api, file, () => {}, undefined, { checkpoint })).rejects.toThrow(
      'completion failed',
    );
    await expect(uploadFile(api, file, () => {}, undefined, { checkpoint })).resolves.toEqual({
      id: 'saved',
    });
    expect(put).toHaveBeenCalledTimes(2);
    expect(calls).toEqual([
      'memes',
      'memes/saved/upload-url',
      'memes/saved/upload-completion',
      'memes/saved/upload-completion',
    ]);
  });
  it('sends explicit duplicate approval to reservation and completion', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => {
      expect(JSON.parse(config.data).allow_duplicate).toBe(true);
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: {
          id: 'saved',
          upload: { url: 'https://storage.example/staging', method: 'PUT', headers: {} },
        },
      };
    };
    vi.spyOn(axios, 'put').mockResolvedValue({ data: null });
    await uploadFile(api, input(), () => {}, undefined, { allowDuplicate: true });
  });
  it('returns duplicate details for a confirmation choice', async () => {
    const api = createApi('/api');
    api.defaults.adapter = async (config) => {
      throw new axios.AxiosError('duplicate', 'ERR_BAD_REQUEST', config, undefined, {
        config,
        status: 409,
        statusText: 'Conflict',
        headers: {},
        data: { error: 'duplicate_image', duplicate: { id: 'existing', title: 'Existing' } },
      });
    };
    await expect(uploadFile(api, input(), () => {})).rejects.toMatchObject({
      duplicate: { id: 'existing', title: 'Existing' },
    });
  });
});

describe('per-image metadata', () => {
  it('keeps each image title and source separate through a retry', async () => {
    const api = createApi('/api');
    const saved: Record<string, Record<string, string>> = {};
    let sequence = 0;
    let failCompletion = true;
    api.defaults.adapter = async (config) => {
      if (config.url === 'memes') {
        const id = `image-${++sequence}`;
        saved[id] = JSON.parse(config.data);
        return {
          config,
          status: 201,
          statusText: 'Created',
          headers: {},
          data: {
            id,
            upload: { url: `https://storage.example/${id}`, method: 'PUT', headers: {} },
          },
        };
      }
      if (config.url === 'memes/image-1/upload-completion' && failCompletion) {
        failCompletion = false;
        throw new Error('temporary failure');
      }
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data: { id: config.url?.split('/')[1] },
      };
    };
    vi.spyOn(axios, 'put').mockResolvedValue({ data: null });
    const first = {
      file: new File(['spring'], 'spring.png', { type: 'image/png' }),
      title: 'Spring',
      source_url: 'https://example.com/spring',
      source_name: 'Spring source',
      source_author: 'Alice',
    };
    const second = {
      file: new File(['may'], 'may.png', { type: 'image/png' }),
      title: 'May',
      source_url: 'https://example.com/may',
      source_name: 'May source',
      source_author: 'Bob',
    };
    const checkpoint = {};
    await expect(uploadFile(api, first, () => {}, undefined, { checkpoint })).rejects.toThrow(
      'temporary failure',
    );
    await uploadFile(api, second, () => {});
    await uploadFile(api, first, () => {}, undefined, { checkpoint });
    expect(sequence).toBe(2);
    expect(saved['image-1']).toMatchObject({
      title: 'Spring',
      source_author: 'Alice',
      source_url: first.source_url,
    });
    expect(saved['image-2']).toMatchObject({
      title: 'May',
      source_author: 'Bob',
      source_url: second.source_url,
    });
  });
});

describe('audio uploads', () => {
  it.each([
    ['sample.mp3', 'audio/mpeg', 'audio/mpeg'],
    ['sample.wav', 'audio/x-wav', 'audio/wav'],
    ['sample.ogg', 'audio/ogg', 'audio/ogg'],
    ['sample.m4a', '', 'audio/mp4'],
  ])('reserves and completes %s with a canonical MIME type', async (name, type, mime) => {
    const api = createApi('/api');
    const calls: string[] = [];
    api.defaults.adapter = async (config) => {
      calls.push(config.url!);
      if (config.url === 'memes') {
        expect(JSON.parse(config.data)).toMatchObject({
          content_type: 'audio',
          mime_type: mime,
          original_filename: name,
        });
      }
      return {
        config,
        status: 200,
        statusText: 'OK',
        headers: {},
        data:
          config.url === 'memes'
            ? {
                id: 'audio',
                upload: {
                  url: 'https://storage.example/audio',
                  method: 'PUT',
                  headers: { 'Content-Type': mime },
                },
              }
            : { id: 'audio', content_type: 'audio', status: 'ready' },
      };
    };
    const put = vi.spyOn(axios, 'put').mockResolvedValue({ data: null });
    const result = await uploadFile(
      api,
      {
        file: new File(['audio bytes'], name, { type }),
        title: 'Sound',
        source_url: '',
        source_name: '',
        source_author: '',
      },
      () => {},
    );
    expect(result.content_type).toBe('audio');
    expect(put).toHaveBeenCalledWith(
      'https://storage.example/audio',
      expect.any(File),
      expect.objectContaining({ withCredentials: false, headers: { 'Content-Type': mime } }),
    );
    expect(calls).toEqual(['memes', 'memes/audio/upload-completion']);
  });
  it('rejects empty, oversized, and unsupported audio', () => {
    for (const file of [
      new File([], 'empty.mp3', { type: 'audio/mpeg' }),
      new File([new Uint8Array(10 * 1024 * 1024 + 1)], 'big.wav', { type: 'audio/wav' }),
      new File(['bytes'], 'audio.flac', { type: 'audio/flac' }),
    ]) {
      expect(() => validateMediaFile(file)).toThrow('10 MB');
    }
  });
});
