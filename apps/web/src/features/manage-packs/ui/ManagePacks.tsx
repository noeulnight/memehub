import { useState, type SubmitEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { packOptions, type Pack } from '../../../entities/pack';
import { apiError, useApi } from '../../../shared/api/client';
import { Button } from '../../../shared/ui/button';
import { Input } from '../../../shared/ui/input';
import { Dialog, DialogTitle, DialogDescription } from '../../../shared/ui/dialog';
import {
  DialogPanel,
  DialogPanelHeader,
  DialogBody,
  DialogPanelFooter,
} from '../../../shared/ui/dialog-layout';
export function ManagePacks({
  userId,
  isAdmin,
  onClose,
}: {
  userId: string;
  isAdmin: boolean;
  onClose: () => void;
}) {
  const api = useApi();
  const cache = useQueryClient();
  const packs = useQuery(packOptions(api));
  const [formVersion, setFormVersion] = useState(0);
  const [editing, setEditing] = useState<Pack | null>(null);
  const [deleting, setDeleting] = useState<Pack | null>(null);
  const mutation = useMutation({
    mutationFn: async (input: { name: string; source_url: string } | null) => {
      if (!input && deleting) await api.delete(`packs/${deleting.id}`);
      else if (editing) await api.put(`packs/${editing.id}`, input);
      else await api.post('packs', input);
    },
    onSuccess: async () => {
      setFormVersion((value) => value + 1);
      setEditing(null);
      setDeleting(null);
      await Promise.all([
        cache.invalidateQueries({ queryKey: ['packs'] }),
        cache.invalidateQueries({ queryKey: ['memes'] }),
      ]);
    },
  });
  function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    mutation.mutate({
      name: String(data.get('name') || ''),
      source_url: String(data.get('source_url') || ''),
    });
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) onClose();
      }}
    >
      <DialogPanel>
        <DialogPanelHeader>
          <DialogTitle>Packs</DialogTitle>
          <DialogDescription>
            Group memes by their original pack, independently of tags.
          </DialogDescription>
        </DialogPanelHeader>
        <DialogBody>
          <form key={editing?.id || `new-${formVersion}`} onSubmit={submit} className="space-y-3">
            <fieldset disabled={mutation.isPending} className="space-y-3">
              <label className="block space-y-2 text-xs font-medium">
                Name
                <Input name="name" required maxLength={100} defaultValue={editing?.name || ''} />
              </label>
              <label className="block space-y-2 text-xs font-medium">
                Source URL (optional)
                <Input
                  name="source_url"
                  type="url"
                  maxLength={2048}
                  defaultValue={editing?.source_url || ''}
                />
              </label>
              <div className="flex gap-2">
                <Button type="submit" size="sm">
                  {editing ? 'Save pack' : 'Create pack'}
                </Button>
                {editing && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      setEditing(null);
                      mutation.reset();
                    }}
                  >
                    Cancel
                  </Button>
                )}
              </div>
            </fieldset>
          </form>
          {mutation.isError && (
            <p role="alert" className="mt-3 text-sm text-destructive">
              {apiError(mutation.error)}
            </p>
          )}
          {packs.isError && (
            <Button variant="ghost" onClick={() => void packs.refetch()}>
              Retry packs
            </Button>
          )}
          {packs.isPending && <p role="status">Loading packs…</p>}
          <ul className="mt-6 space-y-3">
            {packs.data?.map((pack) => (
              <li key={pack.id} className="flex items-center justify-between gap-3 text-sm">
                <div className="min-w-0">
                  <p className="truncate">
                    {pack.name}{' '}
                    <span className="text-xs text-muted-foreground">{pack.meme_count}</span>
                  </p>
                  {pack.source_url && (
                    <a
                      href={pack.source_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-xs text-muted-foreground hover:underline"
                    >
                      Original source
                    </a>
                  )}
                </div>
                {(pack.created_by === userId || isAdmin) && (
                  <div className="flex shrink-0 gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={mutation.isPending}
                      onClick={() => {
                        setEditing(pack);
                        setDeleting(null);
                        mutation.reset();
                      }}
                    >
                      Edit
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={mutation.isPending}
                      onClick={() => {
                        setDeleting(pack);
                        mutation.reset();
                      }}
                    >
                      Delete
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
          {deleting && (
            <div className="mt-4 space-y-2 text-sm">
              <p>Delete {deleting.name}? Its memes will remain without a pack.</p>
              <div className="flex gap-2">
                <Button
                  variant="destructive"
                  size="sm"
                  disabled={mutation.isPending}
                  onClick={() => mutation.mutate(null)}
                >
                  Delete pack
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={mutation.isPending}
                  onClick={() => setDeleting(null)}
                >
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </DialogBody>
        <DialogPanelFooter>
          <Button variant="ghost" disabled={mutation.isPending} onClick={onClose}>
            Close
          </Button>
        </DialogPanelFooter>
      </DialogPanel>
    </Dialog>
  );
}
