import { useEffect, useState, type ReactNode, type SubmitEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { Meme } from '../../../entities/meme';
import { apiError, useApi } from '../../../shared/api/client';
import { Button } from '../../../shared/ui/button';
import { Input } from '../../../shared/ui/input';
import { Textarea } from '../../../shared/ui/textarea';
import { DialogTitle, DialogDescription } from '../../../shared/ui/dialog';
import { DialogPanelHeader, DialogPanelFooter, DialogBody } from '../../../shared/ui/dialog-layout';

export function ManageMeme({
  meme,
  content,
  actions,
  onDeleted,
  onPendingChange,
}: {
  meme: Meme;
  content: ReactNode;
  actions: ReactNode;
  onDeleted: () => void;
  onPendingChange: (pending: boolean) => void;
}) {
  const api = useApi();
  const cache = useQueryClient();
  const [action, setAction] = useState<'edit' | 'delete' | null>(null);
  const mutation = useMutation({
    mutationFn: async (input: Record<string, unknown> | null) => {
      const path = `memes/${encodeURIComponent(meme.id)}`;
      if (input) await api.put(path, input);
      else await api.delete(path);
    },
    onSuccess: async (_, input) => {
      setAction(null);
      if (!input) onDeleted();
      await cache.invalidateQueries({ queryKey: ['memes'] });
    },
  });
  useEffect(() => {
    onPendingChange(mutation.isPending);
    return () => onPendingChange(false);
  }, [mutation.isPending, onPendingChange]);
  function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const input: Record<string, unknown> = {
      content_type: meme.content_type,
      content_revision: meme.content_revision,
      tags: String(data.get('tags') || '')
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
    };
    for (const field of [
      'title',
      'description',
      'source_url',
      'source_name',
      'source_author',
      'source_note',
    ])
      input[field] = String(data.get(field) || '');
    if (meme.content_type === 'text') input.body_text = String(data.get('body_text') || '');
    else if (meme.content_type === 'image')
      input.extracted_text = String(data.get('extracted_text') || '');
    mutation.mutate(input);
  }
  function start(next: 'edit' | 'delete') {
    mutation.reset();
    setAction(next);
  }
  const error = mutation.isError ? (
    <p role="alert" className="text-sm text-destructive">
      {apiError(mutation.error)}
    </p>
  ) : null;
  return (
    <div className="flex min-h-0 min-w-0 flex-col">
      {action === null ? (
        <>
          {content}
          <DialogPanelFooter className="flex-row items-center justify-between sm:justify-between">
            {actions}
            <div className="ml-auto flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => start('edit')}>
                Edit
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="text-muted-foreground hover:text-destructive"
                onClick={() => start('delete')}
              >
                Delete
              </Button>
            </div>
          </DialogPanelFooter>
        </>
      ) : action === 'edit' ? (
        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
          <DialogPanelHeader>
            <DialogTitle>Edit meme</DialogTitle>
            <DialogDescription>Update the content and tags.</DialogDescription>
          </DialogPanelHeader>
          <DialogBody>
            <fieldset disabled={mutation.isPending} className="space-y-4">
              <label className="block space-y-2 text-xs font-medium">
                Title
                <Input name="title" defaultValue={meme.title} maxLength={200} />
              </label>
              {meme.content_type === 'text' ? (
                <label className="block space-y-2 text-xs font-medium">
                  Text
                  <Textarea
                    name="body_text"
                    defaultValue={meme.body_text || ''}
                    required
                    maxLength={10000}
                    rows={5}
                  />
                </label>
              ) : meme.content_type === 'image' ? (
                <label className="block space-y-2 text-xs font-medium">
                  Extracted text
                  <Textarea
                    name="extracted_text"
                    defaultValue={meme.extracted_text}
                    maxLength={10000}
                    rows={3}
                  />
                </label>
              ) : null}
              <label className="block space-y-2 text-xs font-medium">
                Description
                <Textarea
                  name="description"
                  defaultValue={meme.description}
                  maxLength={10000}
                  rows={3}
                />
              </label>
              <label className="block space-y-2 text-xs font-medium">
                Tags
                <Input name="tags" defaultValue={meme.tags.join(', ')} placeholder="work, monday" />
                <span className="block text-xs font-normal text-muted-foreground">
                  Separate tags with commas.
                </span>
              </label>
              <details className="space-y-4">
                <summary className="cursor-pointer py-2 text-xs font-medium text-muted-foreground">
                  Source · optional
                </summary>
                <label className="block space-y-2 text-xs font-medium">
                  Source URL
                  <Input
                    name="source_url"
                    type="url"
                    defaultValue={meme.source_url || ''}
                    maxLength={2048}
                  />
                </label>
                <div className="grid gap-4 sm:grid-cols-2">
                  <label className="block space-y-2 text-xs font-medium">
                    Source name
                    <Input
                      name="source_name"
                      defaultValue={meme.source_name || ''}
                      maxLength={200}
                    />
                  </label>
                  <label className="block space-y-2 text-xs font-medium">
                    Source author
                    <Input
                      name="source_author"
                      defaultValue={meme.source_author || ''}
                      maxLength={200}
                    />
                  </label>
                </div>
                <label className="block space-y-2 text-xs font-medium">
                  Source note
                  <Textarea
                    name="source_note"
                    defaultValue={meme.source_note || ''}
                    maxLength={2000}
                    rows={2}
                  />
                </label>
              </details>
            </fieldset>
            {error}
          </DialogBody>
          <DialogPanelFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={mutation.isPending}
              onClick={() => setAction(null)}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={mutation.isPending}>
              {mutation.isPending ? 'Saving…' : 'Save changes'}
            </Button>
          </DialogPanelFooter>
        </form>
      ) : (
        <>
          <DialogPanelHeader>
            <DialogTitle>Delete meme?</DialogTitle>
          </DialogPanelHeader>
          <DialogBody>
            <DialogDescription>This meme will be removed from the library.</DialogDescription>
            {error}
          </DialogBody>
          <DialogPanelFooter>
            <Button variant="ghost" disabled={mutation.isPending} onClick={() => setAction(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={mutation.isPending}
              onClick={() => mutation.mutate(null)}
            >
              {mutation.isPending ? 'Deleting…' : 'Delete'}
            </Button>
          </DialogPanelFooter>
        </>
      )}
    </div>
  );
}
