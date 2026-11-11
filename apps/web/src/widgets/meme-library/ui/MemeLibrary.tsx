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
import { getCollection, type Collection } from '../../../entities/collection';
import { MyCollections, SaveToCollection } from '../../../features/manage-collections';
export function MemeLibrary() {
  const api = useApi();
  const session = useQuery({ queryKey: ['session'], queryFn: () => getSession(api) });
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [collectionsOpen, setCollectionsOpen] = useState(false);
  const [saveMemeId, setSaveMemeId] = useState('');
  const [collectionId, setCollectionId] = useState('');
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
    setCollectionId(params.get('collection') || '');
    const kind = params.get('content_type');
    if (kind === 'image' || kind === 'text' || kind === 'audio') setFilter(kind);
  }, []);
  useEffect(() => {
    if (session.isSuccess && !session.data) {
      setMine(false);
      setCollectionId('');
      setCollectionsOpen(false);
      setSaveMemeId('');
      const url = new URL(window.location.href);
      url.searchParams.delete('mine');
      url.searchParams.delete('collection');
      window.history.replaceState(null, '', url);
    } else if (session.data) {
      const pendingMeme = sessionStorage.getItem('memehub.pendingCollectionMeme');
      if (pendingMeme) {
        sessionStorage.removeItem('memehub.pendingCollectionMeme');
        setSaveMemeId(pendingMeme);
      }
    }
  }, [session.isSuccess, session.data?.id]);
  const selectedCollection = useQuery({
    queryKey: ['collections', session.data?.id || '', 'detail', collectionId],
    queryFn: ({ signal }) => getCollection(api, collectionId, signal),
    enabled: !!collectionId && !!session.data,
  });
  const collection = useMemeList(query, filter, tag, mine, session.data?.id || '', collectionId);
  const items = collection.data?.pages.flatMap((page) => page.items) || [];
  const loadError =
    collection.error ||
    (collectionId && selectedCollection.error) ||
    ((mine || collectionId) && session.error);
  const loading = collection.isPending && !loadError;
  const loadingMore = collection.isFetchingNextPage;
  const cursor = collection.hasNextPage;
  const error = loadError ? apiError(loadError) : '';
  function updateSearch(
    value: string,
    kind: ContentFilter,
    selectedTag = '',
    onlyMine = mine,
    savedCollection = collectionId,
  ) {
    const normalized = value.trim();
    const own = onlyMine && !/^@\S+/.test(normalized);
    setMine(own);
    setQuery(normalized);
    setTag(selectedTag);
    setFilter(kind);
    setCollectionId(savedCollection);
    const url = new URL(window.location.href);
    if (own) url.searchParams.set('mine', 'true');
    else url.searchParams.delete('mine');
    if (savedCollection) url.searchParams.set('collection', savedCollection);
    else url.searchParams.delete('collection');
    if (selectedTag) url.searchParams.set('tag', selectedTag);
    else url.searchParams.delete('tag');
    if (normalized) url.searchParams.set('q', normalized);
    else url.searchParams.delete('q');
    if (kind) url.searchParams.set('content_type', kind);
    else url.searchParams.delete('content_type');
    window.history.replaceState(null, '', url);
  }
  function openCollection(selected: Collection) {
    updateSearch('', '', '', false, selected.id);
    setCollectionsOpen(false);
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
          onSave={() => setSaveMemeId(sharedMeme.data!.id)}
          onClose={() => {
            setSharedID('');
            const url = new URL(window.location.href);
            url.searchParams.delete('meme');
            window.history.replaceState(null, '', url);
          }}
          onTagSearch={(selected) => updateSearch('', filter, selected)}
          onUserSearch={(username) => updateSearch(`@${username}`, filter)}
          analysis={
            session.data?.is_admin && sharedMeme.data.content_type !== 'audio' ? (
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
        {collectionId && (
          <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0">
              <p className="mb-1 text-xs text-muted-foreground">My collections</p>
              <h1 className="break-words text-lg font-semibold">
                {selectedCollection.data?.name || 'Collection'}
              </h1>
            </div>
            <Button variant="outline" size="sm" onClick={() => setCollectionsOpen(true)}>
              My collections
            </Button>
          </div>
        )}
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
                  onSave={() => setSaveMemeId(meme.id)}
                  analysis={
                    session.data?.is_admin && meme.content_type !== 'audio' ? (
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
              <p className="py-12 text-center text-sm text-muted-foreground">
                {collectionId
                  ? 'No memes found in this collection. Save memes using the bookmark button.'
                  : 'No memes found.'}
              </p>
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
                onClick={() => {
                  if ((mine || collectionId) && !session.data) void session.refetch();
                  else {
                    if (collectionId) void selectedCollection.refetch();
                    if (collection.isFetchNextPageError) void collection.fetchNextPage();
                    else void collection.refetch();
                  }
                }}
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
      {saveMemeId && (
        <SaveToCollection
          key={`${session.data?.id || ''}:${saveMemeId}`}
          memeId={saveMemeId}
          onClose={() => setSaveMemeId('')}
        />
      )}
      {collectionsOpen && session.data && (
        <MyCollections
          key={session.data.id}
          userId={session.data.id}
          onClose={() => setCollectionsOpen(false)}
          onSelect={openCollection}
          onDeleted={(id) => {
            if (collectionId === id) updateSearch('', '', '', false, '');
          }}
        />
      )}
      <div className="search-dock fixed left-1/2 z-20 flex w-[calc(100%_-_32px)] max-w-[468px] -translate-x-1/2 items-center gap-2">
        <div className="absolute bottom-full left-0 right-0 mb-2 flex flex-wrap items-center gap-2">
          {(mine || collectionId) && (
            <Button
              variant="secondary"
              size="xs"
              className="max-w-full rounded-full"
              onClick={() => updateSearch(query, filter, tag, false, '')}
            >
              <span className="truncate">
                {collectionId ? selectedCollection.data?.name || 'Collection' : 'My memes'}
              </span>{' '}
              <CloseIcon aria-hidden="true" />
            </Button>
          )}
        </div>
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
          onCollections={() => setCollectionsOpen(true)}
          inCollection={!!collectionId}
          mine={mine}
          onMineChange={(own) =>
            updateSearch(query.startsWith('@') ? '' : query, filter, tag, own, '')
          }
        />
      </div>
    </div>
  );
}
