import { useState, type SubmitEvent } from 'react';
import { toast } from 'sonner';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createCollection, type Collection } from '../../../entities/collection';
import { apiError, useApi } from '../../../shared/api/client';
import { Button } from '../../../shared/ui/button';
import { Input } from '../../../shared/ui/input';
import { LoadingIcon, PlusIcon } from '../../../shared/ui/icons';

export function CollectionForm({
  userId,
  memeId,
  onCreated,
  disabled = false,
}: {
  userId: string;
  memeId?: string;
  onCreated?: (collection: Collection) => void;
  disabled?: boolean;
}) {
  const api = useApi();
  const cache = useQueryClient();
  const [name, setName] = useState('');
  const create = useMutation({
    mutationFn: () => createCollection(api, name, memeId),
    onError: (error) => toast.error(apiError(error)),
    onSuccess: async (collection) => {
      setName('');
      toast.success(memeId ? `Saved to “${collection.name}”.` : `Created “${collection.name}”.`);
      await cache.invalidateQueries({ queryKey: ['collections', userId] });
      onCreated?.(collection);
    },
  });
  function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (name.trim() && !create.isPending && !disabled) create.mutate();
  }
  return (
    <form onSubmit={submit} className="space-y-2">
      <div className="flex min-w-0 items-center gap-2">
        <Input
          name="collection-name"
          aria-label="New collection"
          placeholder="New collection"
          className="flex-1"
          value={name}
          maxLength={100}
          required
          disabled={create.isPending || disabled}
          onChange={(event) => {
            setName(event.target.value);
            create.reset();
          }}
        />
        <Button
          type="submit"
          variant="ghost"
          size="icon"
          aria-label={memeId ? 'Create & save' : 'Create collection'}
          title={memeId ? 'Create & save' : 'Create collection'}
          disabled={!name.trim() || create.isPending || disabled}
        >
          {create.isPending ? (
            <LoadingIcon className="animate-spin" aria-hidden="true" />
          ) : (
            <PlusIcon aria-hidden="true" />
          )}
        </Button>
      </div>
    </form>
  );
}
