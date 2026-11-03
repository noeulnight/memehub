import { useEffect, useState, type SubmitEvent } from 'react';
import { toast } from 'sonner';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  deleteCollection,
  renameCollection,
  useCollections,
  type Collection,
} from '../../../entities/collection';
import { apiError, useApi } from '../../../shared/api/client';
import { Button } from '../../../shared/ui/button';
import { Input } from '../../../shared/ui/input';
import { CollectionIcon, MoreIcon, LoadingIcon } from '../../../shared/ui/icons';
import { Dialog, DialogDescription, DialogTitle } from '../../../shared/ui/dialog';
import {
  DialogBody,
  DialogPanel,
  DialogPanelFooter,
  DialogPanelHeader,
} from '../../../shared/ui/dialog-layout';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '../../../shared/ui/dropdown-menu';
import { CollectionForm } from './CollectionForm';

export function MyCollections({
  userId,
  onClose,
  onSelect,
  onDeleted,
}: {
  userId: string;
  onClose: () => void;
  onSelect: (collection: Collection) => void;
  onDeleted: (id: string) => void;
}) {
  const api = useApi();
  const cache = useQueryClient();
  const collections = useCollections(userId, true);
  useEffect(() => {
    if (collections.error)
      toast.error(apiError(collections.error), { id: `collections-list-${userId}` });
  }, [collections.error, collections.errorUpdatedAt, userId]);
  const [action, setAction] = useState<{
    collection: Collection;
    kind: 'rename' | 'delete';
  } | null>(null);
  const [name, setName] = useState('');
  const mutation = useMutation({
    mutationFn: async () => {
      if (!action) return;
      if (action.kind === 'delete') await deleteCollection(api, action.collection.id);
      else await renameCollection(api, action.collection.id, name);
    },
    onError: (error) => toast.error(apiError(error)),
    onSuccess: async () => {
      toast.success(
        action?.kind === 'delete'
          ? `Deleted “${action.collection.name}”.`
          : `Renamed to “${name.trim()}”.`,
      );
      if (action?.kind === 'delete') onDeleted(action.collection.id);
      setAction(null);
      await cache.invalidateQueries({ queryKey: ['collections', userId] });
    },
  });
  function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!mutation.isPending && (action?.kind === 'delete' || name.trim())) mutation.mutate();
  }
  const items = collections.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogPanel className="sm:max-w-sm">
        <DialogPanelHeader>
          <DialogTitle>
            {action
              ? action.kind === 'delete'
                ? 'Delete collection?'
                : 'Rename collection'
              : 'My collections'}
          </DialogTitle>
          <DialogDescription>
            {action?.kind === 'delete'
              ? 'This removes the collection. The original memes stay in the library.'
              : 'Your collections are only visible to you.'}
          </DialogDescription>
        </DialogPanelHeader>
        {action ? (
          <form onSubmit={submit} className="flex min-h-0 flex-col">
            <DialogBody>
              {action.kind === 'delete' ? (
                <p className="break-words text-sm">Delete “{action.collection.name}”?</p>
              ) : (
                <label className="block space-y-2 text-xs font-medium">
                  Collection name
                  <Input
                    value={name}
                    maxLength={100}
                    required
                    disabled={mutation.isPending}
                    onChange={(event) => setName(event.target.value)}
                  />
                </label>
              )}
            </DialogBody>
            <DialogPanelFooter>
              <Button
                type="button"
                variant="outline"
                disabled={mutation.isPending}
                onClick={() => setAction(null)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                variant={action.kind === 'delete' ? 'destructive' : 'default'}
                disabled={mutation.isPending || (action.kind === 'rename' && !name.trim())}
              >
                {mutation.isPending && <LoadingIcon className="animate-spin" aria-hidden="true" />}
                {action.kind === 'delete' ? 'Delete' : 'Save'}
              </Button>
            </DialogPanelFooter>
          </form>
        ) : (
          <>
            <DialogBody className="space-y-5 pb-6">
              <div>
                {collections.isPending && (
                  <LoadingIcon
                    role="status"
                    aria-label="Loading collections"
                    className="mx-auto my-4 size-5 animate-spin text-muted-foreground"
                  />
                )}
                {collections.isSuccess && !items.length && (
                  <p className="py-4 text-sm text-muted-foreground">
                    No collections yet. Create your first one below.
                  </p>
                )}
                <ul className="space-y-1">
                  {items.map((collection) => (
                    <li key={collection.id} className="flex items-center gap-1">
                      <Button
                        variant="ghost"
                        className="h-auto min-h-12 min-w-0 flex-1 justify-start whitespace-normal px-2 py-3 text-left"
                        onClick={() => onSelect(collection)}
                      >
                        <CollectionIcon className="text-muted-foreground" aria-hidden="true" />
                        <span className="min-w-0 flex-1 break-words">{collection.name}</span>
                        <span className="text-xs tabular-nums text-muted-foreground">
                          {collection.meme_count}
                        </span>
                      </Button>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Manage ${collection.name}`}
                          >
                            <MoreIcon aria-hidden="true" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          {(['rename', 'delete'] as const).map((kind) => (
                            <DropdownMenuItem
                              key={kind}
                              onSelect={() => {
                                mutation.reset();
                                setName(collection.name);
                                setAction({ collection, kind });
                              }}
                            >
                              {kind === 'rename' ? 'Rename' : 'Delete'}
                            </DropdownMenuItem>
                          ))}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </li>
                  ))}
                </ul>
                {collections.hasNextPage && (
                  <Button
                    variant="ghost"
                    className="mt-2 w-full"
                    disabled={collections.isFetchingNextPage}
                    onClick={() => void collections.fetchNextPage()}
                  >
                    {collections.isFetchingNextPage ? 'Loading…' : 'Load more'}
                  </Button>
                )}
                {collections.isError && (
                  <div className="py-3">
                    <Button
                      variant="secondary"
                      onClick={() =>
                        void (collections.isFetchNextPageError
                          ? collections.fetchNextPage()
                          : collections.refetch())
                      }
                    >
                      Try again
                    </Button>
                  </div>
                )}
              </div>
              <CollectionForm userId={userId} onCreated={onSelect} />
            </DialogBody>
          </>
        )}
      </DialogPanel>
    </Dialog>
  );
}
