import { endpoint } from '../../../shared/api/client';
import type { Meme } from '../../../entities/meme';

export function doubleMemeURL(left: Meme, right: Meme, origin: string, apiBase: string) {
  const extension = [left, right].some((meme) => meme.mime_type === 'image/gif') ? 'gif' : 'png';
  return new URL(
    endpoint(
      apiBase,
      `double/${encodeURIComponent(left.id)}/${encodeURIComponent(right.id)}/file.${extension}`,
    ),
    origin,
  ).href;
}
import { isAxiosError, type AxiosInstance } from 'axios';

export async function getDoubleMeme(api: AxiosInstance, url: string, signal?: AbortSignal) {
  try {
    const response = await api.get<Blob>(url, {
      signal,
      responseType: 'blob',
      withCredentials: false,
    });
    return response.data;
  } catch (error) {
    const data = isAxiosError(error) ? error.response?.data : undefined;
    const body =
      data instanceof Blob
        ? await data
            .text()
            .then((text) => JSON.parse(text))
            .catch(() => ({}))
        : data;
    throw new Error(
      body?.error === 'double_animation_duration_unsupported'
        ? 'Each GIF must be no longer than 30 seconds.'
        : 'Could not combine these images. Try again.',
    );
  }
}
