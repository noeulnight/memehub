import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getSession } from '../../../entities/session';
import { ManageMeme } from '../../../features/manage-meme';
import { ManageAnalysis } from '../../../features/manage-analysis';
import { CloseIcon } from '../../../shared/ui/icons';
import { Button } from '../../../shared/ui/button';
import { Masonry } from '../../../shared/ui/masonry';
import { apiError, useApi } from '../../../shared/api/client';
import {
  formatTagToken,
  MemeCard,
  useMemeList,
  type Meme,
  type ContentFilter,
} from '../../../entities/meme';
import { BrowseTags } from '../../../features/browse-tags';
import { SearchDock } from '../../../features/search-memes';
import { UserMenu } from '../../../features/user-menu';
import { AdminSettings } from '../../admin-settings';
import { UploadMeme } from '../../../features/upload-meme';
export function MemeLibrary() {
  const api = useApi();
  const session = useQuery({ queryKey: ['session'], queryFn: () => getSession(api) });
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [sharedID, setSharedID] = useState('');
  const sharedMeme = useQuery({
    queryKey: ['memes', 'detail', sharedID],
    queryFn: async ({ signal }) =>
      (await api.get<Meme>(`memes/${encodeURIComponent(sharedID)}`, { signal })).data,
    enabled: !!sharedID,
  });
  const [mine, setMine] = useState(false);
  const [tag, setTag] = useState('');
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<ContentFilter>('');
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    setSharedID(params.get('meme') || '');
    setQuery(params.get('q') || '');
    setTag(params.get('tag') || '');
    setMine(params.get('mine') === 'true');
    const kind = params.get('content_type');
    if (kind === 'image' || kind === 'text') setFilter(kind);
  }, []);
  const collection = useMemeList(query, filter, tag, mine, session.data?.id || '');
  const items = collection.data?.pages.flatMap((page) => page.items) || [];
  const loading = collection.isPending;
  const loadingMore = collection.isFetchingNextPage;
  const cursor = collection.hasNextPage;
  const error = collection.error ? apiError(collection.error) : '';
  function updateSearch(value: string, kind: ContentFilter, selectedTag = '', onlyMine = mine) {
    const normalized = value.trim();
    const own = onlyMine && !/^@\S+/.test(normalized);
    setMine(own);
    setQuery(normalized);
    setTag(selectedTag);
    setFilter(kind);
    const url = new URL(window.location.href);
    if (own) url.searchParams.set('mine', 'true');
    else url.searchParams.delete('mine');
    if (selectedTag) url.searchParams.set('tag', selectedTag);
    else url.searchParams.delete('tag');
    if (normalized) url.searchParams.set('q', normalized);
    else url.searchParams.delete('q');
    if (kind) url.searchParams.set('content_type', kind);
    else url.searchParams.delete('content_type');
    window.history.replaceState(null, '', url);
  }

  return (
    <div className="min-h-dvh">
      {sharedID && sharedMeme.isPending && (
        <p role="status" className="px-6 pt-6 text-sm text-muted-foreground">
          Loading shared meme…
        </p>
      )}
      {sharedID && sharedMeme.isError && (
        <div role="alert" className="space-y-2 px-6 pt-6 text-sm">
          <p>{apiError(sharedMeme.error)}</p>
          <Button variant="secondary" onClick={() => void sharedMeme.refetch()}>
            Try again
          </Button>
        </div>
      )}
      {sharedID && sharedMeme.data && (
        <MemeCard
          key={sharedID}
          meme={sharedMeme.data}
          index={0}
          detailOnly
          onClose={() => {
            setSharedID('');
            const url = new URL(window.location.href);
            url.searchParams.delete('meme');
            window.history.replaceState(null, '', url);
          }}
          onTagSearch={(selected) => updateSearch('', filter, selected)}
          onUserSearch={(username) => updateSearch(`@${username}`, filter)}
          analysis={
            session.data?.is_admin ? (
              <ManageAnalysis meme={sharedMeme.data} hideCompleted />
            ) : undefined
          }
          manage={
            session.data &&
            (session.data.id === sharedMeme.data.created_by || session.data.is_admin)
              ? (props) => <ManageMeme meme={sharedMeme.data!} {...props} />
              : undefined
          }
        />
      )}
      <main id="main" className="relative z-0 px-4 pb-32 pt-4 sm:px-6 sm:pt-6">
        <div aria-live="polite" aria-busy={loading}>
          {loading ? (
            <div className="meme-grid" role="status" aria-label="Loading memes">
              {Array.from({ length: 8 }, (_, i) => (
                <div
                  key={i}
                  className="h-24 animate-pulse bg-muted/70 motion-reduce:animate-none"
                />
              ))}
            </div>
          ) : items.length ? (
            <Masonry
              onNearEnd={
                cursor && !collection.isFetching && !error
                  ? () => {
                      void collection.fetchNextPage({ cancelRefetch: false });
                    }
                  : undefined
              }
            >
              {items.map((meme, i) => (
                <MemeCard
                  key={meme.id}
                  meme={meme}
                  index={i}
                  analysis={
                    session.data?.is_admin ? (
                      <ManageAnalysis meme={meme} hideCompleted />
                    ) : undefined
                  }
                  onUserSearch={(username) => updateSearch(`@${username}`, filter)}
                  onTagSearch={(tag) => updateSearch('', filter, tag)}
                  manage={
                    session.data && (session.data.id === meme.created_by || session.data.is_admin)
                      ? (props) => <ManageMeme meme={meme} {...props} />
                      : undefined
                  }
                />
              ))}
            </Masonry>
          ) : (
            !error && (
              <p className="py-12 text-center text-sm text-muted-foreground">No memes found.</p>
            )
          )}
          {error && (
            <div
              role="alert"
              className="my-6 flex flex-wrap items-center justify-center gap-3 text-xs text-muted-foreground"
            >
              <p>{error}</p>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  items.length ? void collection.fetchNextPage() : void collection.refetch()
                }
              >
                Try again
              </Button>
            </div>
          )}
          {loadingMore && (
            <p
              role="status"
              className="fixed bottom-24 left-1/2 -translate-x-1/2 text-xs text-muted-foreground"
            >
              Loading…
            </p>
          )}
        </div>
      </main>

      {session.data?.is_admin && (
        <AdminSettings open={settingsOpen} onOpenChange={setSettingsOpen} />
      )}
      <div className="search-dock fixed left-1/2 z-20 flex w-[calc(100%_-_32px)] max-w-[468px] -translate-x-1/2 items-center gap-2">
        {mine && (
          <Button
            variant="secondary"
            size="xs"
            className="absolute bottom-full left-0 mb-2 rounded-full"
            onClick={() => updateSearch(query, filter, tag, false)}
          >
            My memes <CloseIcon aria-hidden="true" />
          </Button>
        )}
        <SearchDock
          query={tag ? formatTagToken(tag) : query}
          onSearch={(value) =>
            tag && value === formatTagToken(tag)
              ? updateSearch('', filter, tag)
              : updateSearch(value, filter)
          }
        />
        <BrowseTags onSelect={(selectedTag) => updateSearch('', filter, selectedTag)} />
        <UploadMeme />
        <UserMenu
          onSettings={() => setSettingsOpen(true)}
          mine={mine}
          onMineChange={(own) => updateSearch(query.startsWith('@') ? '' : query, filter, tag, own)}
        />
      </div>
    </div>
  );
}
