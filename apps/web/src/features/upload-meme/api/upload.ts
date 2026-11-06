import axios, { type AxiosInstance } from 'axios';
import type { Meme } from '../../../entities/meme';

export type FileInput = {
  file: File;
  title: string;
  source_url: string;
  source_name: string;
  source_author: string;
};
type Reservation = Meme & {
  upload: { url: string; method: 'PUT'; headers: Record<string, string>; expires_at: string };
};
export function mediaMIME(file: File) {
  const aliases: Record<string, string> = {
    'audio/x-wav': 'audio/wav',
    'audio/wave': 'audio/wav',
    'audio/x-m4a': 'audio/mp4',
    'audio/mp3': 'audio/mpeg',
  };
  if (file.type) return aliases[file.type] || file.type;
  return (
    (
      { mp3: 'audio/mpeg', wav: 'audio/wav', ogg: 'audio/ogg', m4a: 'audio/mp4' } as Record<
        string,
        string
      >
    )[file.name.split('.').pop()?.toLowerCase() || ''] || ''
  );
}
export function validateMediaFile(file: File) {
  if (
    ![
      'image/png',
      'image/jpeg',
      'image/webp',
      'image/gif',
      'audio/mpeg',
      'audio/wav',
      'audio/ogg',
      'audio/mp4',
    ].includes(mediaMIME(file)) ||
    file.size === 0 ||
    file.size > 10 * 1024 * 1024
  )
    throw new Error('Choose an image or MP3, WAV, OGG, or M4A audio file up to 10 MB.');
}

export type UploadCheckpoint = { id?: string; uploaded?: boolean };
export class DuplicateMediaError extends Error {
  constructor(public duplicate: { id: string; title: string }) {
    super('This file is already in the library.');
  }
}

export async function uploadFile(
  api: AxiosInstance,
  input: FileInput,
  onProgress: (percent: number) => void,
  signal?: AbortSignal,
  options: { checkpoint?: UploadCheckpoint; allowDuplicate?: boolean } = {},
) {
  const checkpoint = options.checkpoint ?? {};
  const { file, ...metadata } = input;
  validateMediaFile(file);
  try {
    if (!checkpoint.uploaded) {
      let upload: Reservation['upload'];
      if (checkpoint.id) {
        const { data } = await api.post<Reservation['upload']>(
          `memes/${encodeURIComponent(checkpoint.id)}/upload-url`,
          undefined,
          { signal },
        );
        upload = data;
      } else {
        const hash = await crypto.subtle.digest('SHA-256', await file.arrayBuffer());
        const sha256 = Array.from(new Uint8Array(hash), (byte) =>
          byte.toString(16).padStart(2, '0'),
        ).join('');
        const { data } = await api.post<Reservation>(
          'memes',
          {
            content_type: mediaMIME(file).startsWith('audio/') ? 'audio' : 'image',
            original_filename: file.name,
            mime_type: mediaMIME(file),
            size_bytes: file.size,
            sha256,
            ...metadata,
            allow_duplicate: options.allowDuplicate ?? false,
          },
          { signal },
        );
        checkpoint.id = data.id;
        upload = data.upload;
      }
      const target = new URL(upload.url);
      if (!['https:', 'http:'].includes(target.protocol) || upload.method !== 'PUT')
        throw new Error('Invalid upload URL');
      // This client must never send service cookies or CSRF headers to storage.
      await axios.put(target.href, file, {
        headers: upload.headers,
        signal,
        withCredentials: false,
        onUploadProgress: (event) => {
          if (event.total) onProgress(Math.round((event.loaded * 100) / event.total));
        },
      });
      checkpoint.uploaded = true;
    }
    onProgress(100);
    const completed = await api.post<Meme>(
      `memes/${encodeURIComponent(checkpoint.id!)}/upload-completion`,
      options.allowDuplicate ? { allow_duplicate: true } : undefined,
      { signal },
    );
    return completed.data;
  } catch (error) {
    if (axios.isAxiosError(error)) {
      const body = error.response?.data;
      if (
        ['duplicate_image', 'duplicate_audio'].includes(body?.error) &&
        typeof body.duplicate?.id === 'string' &&
        typeof body.duplicate?.title === 'string'
      )
        throw new DuplicateMediaError(body.duplicate);
      if (error.response?.status === 404) {
        delete checkpoint.id;
        delete checkpoint.uploaded;
      }
    }
    throw error;
  }
}

export type TextInput = Omit<FileInput, 'file'> & { body_text: string };
export async function createTextMeme(api: AxiosInstance, input: TextInput, signal?: AbortSignal) {
  if (!input.body_text.trim() || Array.from(input.body_text).length > 10_000) {
    throw new Error('Enter text up to 10,000 characters.');
  }
  const { data } = await api.post<Meme>('memes', { ...input, content_type: 'text' }, { signal });
  return data;
}
