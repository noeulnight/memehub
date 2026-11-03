import { useInfiniteQuery } from '@tanstack/react-query';
import type { AxiosInstance } from 'axios';
import { useApi } from '../../../shared/api/client';

export type Collection = {
  id: string;
  name: string;
  created_at: string;
  updated_at: string;
  meme_count: number;
  contains_meme: boolean;
};
export type CollectionPage = { items: Collection[]; next_cursor: string | null };

export async function listCollections(
  api: AxiosInstance,
  cursor: string | null,
  memeId = '',
  signal?: AbortSignal,
): Promise<CollectionPage> {
  const { data } = await api.get<CollectionPage>('collections', {
    params: { limit: 50, cursor: cursor || undefined, meme_id: memeId || undefined },
    signal,
  });
  return data;
}

export function useCollections(userId: string, enabled: boolean, memeId = '') {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ['collections', userId, 'list', memeId],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) => listCollections(api, pageParam, memeId, signal),
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    enabled: enabled && !!userId,
    staleTime: 0,
  });
}

export async function createCollection(api: AxiosInstance, name: string, memeId?: string) {
  const { data } = await api.post<Collection>('collections', {
    name: name.trim(),
    ...(memeId ? { meme_id: memeId } : {}),
  });
  return data;
}

export async function getCollection(api: AxiosInstance, id: string, signal?: AbortSignal) {
  return (await api.get<Collection>(`collections/${encodeURIComponent(id)}`, { signal })).data;
}

export async function setCollectionMeme(
  api: AxiosInstance,
  collectionId: string,
  memeId: string,
  saved: boolean,
) {
  const path = `collections/${encodeURIComponent(collectionId)}/memes/${encodeURIComponent(memeId)}`;
  if (saved) await api.put(path);
  else await api.delete(path);
}

export async function renameCollection(api: AxiosInstance, id: string, name: string) {
  await api.put(`collections/${encodeURIComponent(id)}`, { name: name.trim() });
}

export async function deleteCollection(api: AxiosInstance, id: string) {
  await api.delete(`collections/${encodeURIComponent(id)}`);
}
