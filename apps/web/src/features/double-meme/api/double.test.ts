import { expect, it } from 'vite-plus/test';
import type { Meme } from '../../../entities/meme';
import { doubleMemeURL, getDoubleMeme } from './double';
import { AxiosError } from 'axios';
import { createApi } from '../../../shared/api/client';

it('keeps selection order, output extension, and public API origin in shared URLs', () => {
  const left = { id: 'left', mime_type: 'image/png' } as Meme;
  const right = { id: 'right', mime_type: 'image/gif' } as Meme;
  expect(doubleMemeURL(left, right, 'https://memes.example/?q=cat', '/api/')).toBe(
    'https://memes.example/api/double/left/right/file.gif',
  );
  expect(doubleMemeURL(right, left, 'https://memes.example', 'https://api.example/api')).toBe(
    'https://api.example/api/double/right/left/file.gif',
  );
  expect(doubleMemeURL(left, left, 'https://memes.example', '/api')).toBe(
    'https://memes.example/api/double/left/left/file.png',
  );
});

it('reads binary results and surfaces duration errors returned as blobs', async () => {
  const api = createApi('/api');
  const gif = new Blob(['gif'], { type: 'image/gif' });
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: gif,
  });
  expect(await getDoubleMeme(api, '/api/double/left/right/file.gif')).toBe(gif);
  api.defaults.adapter = async (config) => {
    throw new AxiosError('Rejected', 'ERR_BAD_REQUEST', config, undefined, {
      config,
      status: 422,
      statusText: 'Unprocessable Entity',
      headers: {},
      data: new Blob([JSON.stringify({ error: 'double_animation_duration_unsupported' })], {
        type: 'application/json',
      }),
    });
  };
  await expect(getDoubleMeme(api, '/api/double/left/right/file.gif')).rejects.toThrow(
    'Each GIF must be no longer than 30 seconds.',
  );
});
