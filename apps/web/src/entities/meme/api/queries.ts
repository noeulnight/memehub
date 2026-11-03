import { queryOptions, useInfiniteQuery, useQuery } from '@tanstack/react-query';
import type { AxiosInstance } from 'axios';
import { useApi } from '../../../shared/api/client';
import { parseMemeSearch } from '../model/search';
import type { ContentFilter, MemePage } from '../model/types';

export async function listMemes(
  api: AxiosInstance,
  query: string,
  filter: ContentFilter,
  cursor: string | null,
  signal?: AbortSignal,
  tag = '',
  mine = false,
  collectionId = '',
): Promise<MemePage> {
  const search = parseMemeSearch(query);
  const { data } = await api.get<MemePage>('memes', {
    signal,
    paramsSerializer: { indexes: null },
    params: {
      limit: 24,
      tag: [...new Set([tag, ...search.tags].filter(Boolean))],
      q: search.text || undefined,
      uploader: search.uploader || undefined,
      mine: mine || undefined,
      collection: collectionId || undefined,
      content_type: filter || undefined,
      cursor: cursor || undefined,
    },
  });
  if (
    !Array.isArray(data?.items) ||
    !(data.next_cursor === null || typeof data.next_cursor === 'string')
  )
    throw new Error('The library returned an unexpected response.');
  return data;
}
export function useMemeList(
  query: string,
  filter: ContentFilter,
  tag = '',
  mine = false,
  userId = '',
  collectionId = '',
) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ['memes', query, filter, tag, mine, mine || collectionId ? userId : '', collectionId],
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      listMemes(api, query, filter, pageParam, signal, tag, mine, collectionId),
    enabled: !(mine || collectionId) || !!userId,
    getNextPageParam: (page) => page.next_cursor ?? undefined,
    refetchInterval: (query) =>
      query.state.data?.pages.some((page) =>
        page.items.some((meme) => ['queued', 'processing'].includes(meme.analysis_status)),
      )
        ? 5_000
        : false,
  });
}
export type Media = {
  original: { url: string; expires_at: string };
  thumbnail: { url: string; expires_at: string };
};
export async function getMedia(
  api: AxiosInstance,
  id: string,
  signal?: AbortSignal,
): Promise<Media> {
  const { data } = await api.get<Media>(`memes/${encodeURIComponent(id)}/media`, { signal });
  for (const item of [data?.original, data?.thumbnail]) {
    if (
      !item ||
      !['http:', 'https:'].includes(new URL(item.url).protocol) ||
      !Number.isFinite(Date.parse(item.expires_at))
    )
      throw new Error('Invalid image URL');
  }
  return data;
}
export function memeMediaOptions(api: AxiosInstance, id: string) {
  return queryOptions({
    queryKey: ['meme-media', id],
    queryFn: ({ signal }) => getMedia(api, id, signal),
    staleTime: (query) => Math.max(0, mediaRefreshAt(query.state.data) - query.state.dataUpdatedAt),
    refetchInterval: (query) => {
      return query.state.data
        ? Math.max(1_000, mediaRefreshAt(query.state.data) - Date.now())
        : false;
    },
  });
}

function mediaRefreshAt(media: Media | undefined) {
  return media
    ? Math.min(Date.parse(media.original.expires_at), Date.parse(media.thumbnail.expires_at)) -
        30_000
    : 0;
}

export function useMemeMedia(id: string) {
  const api = useApi();
  return useQuery(memeMediaOptions(api, id));
}
