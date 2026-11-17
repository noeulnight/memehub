import { queryOptions } from '@tanstack/react-query';
import type { AxiosInstance } from 'axios';
export type Pack = {
  id: string;
  name: string;
  source_url: string | null;
  created_by: string;
  meme_count: number;
};
export function packOptions(api: AxiosInstance) {
  return queryOptions({
    queryKey: ['packs'],
    queryFn: async ({ signal }) => {
      const items: Pack[] = [];
      let cursor: string | null = null;
      do {
        const { data }: { data: { items: Pack[]; next_cursor: string | null } } = await api.get(
          'packs',
          { signal, params: { cursor: cursor || undefined } },
        );
        items.push(...data.items);
        cursor = data.next_cursor;
      } while (cursor);
      return items;
    },
    staleTime: 60_000,
  });
}
