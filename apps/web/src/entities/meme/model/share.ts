import { endpoint } from '../../../shared/api/client';
export function memeShareURLs(
  id: string,
  appBase: string,
  apiBase: string,
  mimeType?: string | null,
) {
  const detail = new URL(appBase);
  detail.search = '';
  detail.hash = '';
  detail.searchParams.set('meme', id);
  const extension = (
    {
      'image/jpeg': 'jpg',
      'image/png': 'png',
      'image/webp': 'webp',
      'image/gif': 'gif',
      'audio/mpeg': 'mp3',
      'audio/wav': 'wav',
      'audio/ogg': 'ogg',
      'audio/mp4': 'm4a',
    } as Record<string, string>
  )[mimeType || ''];
  const filename = extension ? `file.${extension}` : 'file';
  const media = new URL(endpoint(apiBase, `memes/${encodeURIComponent(id)}/${filename}`), appBase);
  return { detail: detail.href, media: media.href };
}
