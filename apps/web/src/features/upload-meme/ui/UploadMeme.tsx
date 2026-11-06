import { useEffect, useRef, useState, type SubmitEvent, type ChangeEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { getSession } from '../../../entities/session';
import { ArrowUpIcon, AudioIcon, ImageIcon, LoadingIcon, PlusIcon } from '../../../shared/ui/icons';
import { Dialog, DialogTitle, DialogDescription } from '../../../shared/ui/dialog';
import {
  DialogPanel,
  DialogPanelHeader,
  DialogPanelFooter,
  DialogBody,
} from '../../../shared/ui/dialog-layout';
import { Input } from '../../../shared/ui/input';
import { Textarea } from '../../../shared/ui/textarea';
import { ToggleGroup, ToggleGroupItem } from '../../../shared/ui/toggle-group';
import { Progress } from '../../../shared/ui/progress';
import { Button } from '../../../shared/ui/button';
import { Card, CardContent } from '../../../shared/ui/card';
import { apiError, endpoint, useApi } from '../../../shared/api/client';
import { createTextMeme, mediaMIME, type TextInput } from '../api/upload';
import { useUploadQueue, type UploadMetadata } from '../model/useUploadQueue';

const resumeUploadKey = 'memehub:resume-upload';

export function UploadMeme() {
  const api = useApi();
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const [contentType, setContentType] = useState<'image' | 'text' | 'audio'>('image');
  const [bodyText, setBodyText] = useState('');
  const [dragging, setDragging] = useState(false);
  const [fileError, setFileError] = useState('');
  const queue = useUploadQueue(api, () => {
    void cache.invalidateQueries({ queryKey: ['memes'] });
  });
  function beginLogin() {
    const appBase = import.meta.env.PUBLIC_APP_BASE_URL;
    if (appBase && new URL(appBase).origin !== window.location.origin) {
      // OIDC flow and session cookies must use the configured callback origin.
      const target = new URL(window.location.pathname + window.location.search, appBase);
      target.searchParams.set('upload', '1');
      window.location.assign(target.href);
      return;
    }
    sessionStorage.setItem(resumeUploadKey, '1');
    window.location.assign(endpoint(api.defaults.baseURL || '/api', 'auth/login'));
  }
  const gate = useMutation({
    mutationFn: (_resume: boolean) => getSession(api),
    onSuccess: (session, resume) => {
      if (!session) {
        if (!resume) beginLogin();
        return;
      }
      textMutation.reset();
      setFileError('');
      setDragging(false);
      setOpen(true);
    },
  });
  const checkSession = gate.mutate;
  useEffect(() => {
    const url = new URL(window.location.href);
    if (url.searchParams.get('upload') === '1') {
      url.searchParams.delete('upload');
      window.history.replaceState(null, '', url);
      checkSession(false);
      return;
    }
    if (sessionStorage.getItem(resumeUploadKey) !== '1') return;
    sessionStorage.removeItem(resumeUploadKey);
    checkSession(true);
  }, [checkSession]);

  const textMutation = useMutation({
    mutationFn: (input: TextInput) => createTextMeme(api, input),
    onError: (error) => {
      if (isAxiosError(error) && error.response?.status === 401) {
        setOpen(false);
        beginLogin();
      }
    },
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['memes'] });
      form.current?.reset();
      setBodyText('');
      setOpen(false);
    },
  });
  const busy = queue.running || textMutation.isPending;
  function metadata(): UploadMetadata {
    const data = new FormData(form.current!);
    return {
      title: String(data.get('title') || ''),
      source_url: String(data.get('source_url') || ''),
      source_name: String(data.get('source_name') || ''),
      source_author: String(data.get('source_author') || ''),
    };
  }
  function addFiles(files: File[]) {
    try {
      queue.add(files);
      setFileError('');
    } catch (error) {
      setFileError(apiError(error));
    }
  }
  function runFiles(ids?: string[], allowDuplicate = false) {
    if (form.current?.reportValidity()) void queue.run(ids, allowDuplicate);
  }
  function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (contentType === 'text') textMutation.mutate({ ...metadata(), body_text: bodyText });
    else runFiles();
  }
  const done =
    queue.items.length > 0 &&
    queue.items.every((item) => ['completed', 'skipped'].includes(item.status));
  const actionable = queue.items.some((item) => ['queued', 'failed'].includes(item.status));
  const error = fileError || (textMutation.isError ? apiError(textMutation.error) : '');
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!busy) setOpen(next);
      }}
    >
      <Button
        ref={trigger}
        size="icon"
        variant="outline"
        className="size-[46px] rounded-full bg-muted/95 backdrop-blur-xl dark:bg-muted/95"
        aria-label="Upload meme"
        title="Upload meme"
        disabled={gate.isPending}
        onClick={() => gate.mutate(false)}
      >
        {gate.isPending ? (
          <LoadingIcon className="animate-spin motion-reduce:animate-none" aria-hidden="true" />
        ) : (
          <PlusIcon aria-hidden="true" />
        )}
      </Button>
      {gate.isError && (
        <p
          role="alert"
          className="absolute bottom-full right-0 mb-3 w-64 text-right text-xs text-destructive"
        >
          {apiError(gate.error)}
        </p>
      )}
      <DialogPanel
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          trigger.current?.focus();
        }}
        onEscapeKeyDown={(event) => {
          if (busy) event.preventDefault();
        }}
        onInteractOutside={(event) => {
          if (busy) event.preventDefault();
        }}
      >
        <DialogPanelHeader>
          <DialogTitle>Add memes</DialogTitle>
          <DialogDescription>Save something worth finding again.</DialogDescription>
        </DialogPanelHeader>
        <form ref={form} onSubmit={submit} aria-busy={busy} className="flex min-h-0 flex-col">
          <DialogBody>
            <fieldset disabled={busy} className="space-y-3">
              <ToggleGroup
                type="single"
                value={contentType}
                spacing={1}
                onValueChange={(value) => {
                  if (value === 'image' || value === 'text' || value === 'audio') {
                    setContentType(value);
                    setFileError('');
                    textMutation.reset();
                  }
                }}
                aria-label="Meme type"
                variant="outline"
              >
                <ToggleGroupItem value="image">Images</ToggleGroupItem>
                <ToggleGroupItem value="audio">Audio</ToggleGroupItem>
                <ToggleGroupItem value="text">Text</ToggleGroupItem>
              </ToggleGroup>
              {contentType !== 'text' ? (
                <div>
                  <label
                    onDragOver={(event) => {
                      event.preventDefault();
                      if (!busy) setDragging(true);
                    }}
                    onDragLeave={() => setDragging(false)}
                    onDrop={(event) => {
                      event.preventDefault();
                      setDragging(false);
                      if (!busy) addFiles(Array.from(event.dataTransfer.files));
                    }}
                    className={`flex cursor-pointer items-center gap-3 rounded-lg border border-dashed px-3 py-3 ${dragging ? 'border-ring bg-accent' : 'border-border bg-muted/30 hover:bg-accent/50'}`}
                  >
                    <Input
                      type="file"
                      multiple
                      accept={
                        contentType === 'audio'
                          ? 'audio/mpeg,audio/wav,audio/x-wav,audio/ogg,audio/mp4,.mp3,.wav,.ogg,.m4a'
                          : 'image/jpeg,image/png,image/webp,image/gif'
                      }
                      aria-label={contentType === 'audio' ? 'Choose audio files' : 'Choose images'}
                      className="sr-only"
                      onChange={(event) => {
                        addFiles(Array.from(event.target.files ?? []));
                        event.target.value = '';
                      }}
                    />
                    <span aria-hidden="true">
                      {contentType === 'audio' ? (
                        <AudioIcon className="size-4 text-muted-foreground" />
                      ) : (
                        <ImageIcon className="size-4 text-muted-foreground" />
                      )}
                    </span>
                    <span className="min-w-0">
                      <span className="block text-sm">
                        {contentType === 'audio'
                          ? 'Drop audio files or browse'
                          : 'Drop images or browse'}
                      </span>
                      <span className="block text-xs text-muted-foreground">
                        Up to 100 · 10 MB each
                      </span>
                    </span>
                  </label>
                </div>
              ) : (
                <label className="block text-xs text-muted-foreground">
                  Text
                  <Textarea
                    className="mt-2 min-h-40"
                    value={bodyText}
                    onChange={(event) => setBodyText(event.target.value)}
                    maxLength={10000}
                    placeholder="Paste or write your meme"
                    required
                  />
                </label>
              )}
              {contentType === 'text' && <MetadataFields />}
            </fieldset>
            {contentType !== 'text' && queue.items.length > 0 && (
              <section aria-label="Upload queue" className="mt-5 space-y-3">
                <p role="status" className="text-xs text-muted-foreground">
                  {queue.items.filter((item) => item.status === 'completed').length}/
                  {queue.items.length} saved
                </p>
                <ul className="space-y-4">
                  {queue.items.map((item) => (
                    <li key={item.id}>
                      <Card>
                        <CardContent className="grid grid-cols-[5rem_minmax(0,1fr)] items-start gap-3 p-4 text-xs sm:grid-cols-[7rem_minmax(0,1fr)]">
                          <UploadPreview file={item.file} />
                          <div
                            className={`min-w-0 space-y-3 ${mediaMIME(item.file).startsWith('audio/') ? 'col-span-full' : ''}`}
                          >
                            <div className="flex items-start justify-between gap-3">
                              <div className="min-w-0">
                                <p className="truncate font-medium">{item.file.name}</p>
                                <p className="mt-1 text-muted-foreground">
                                  {item.status === 'processing'
                                    ? 'Verifying and saving…'
                                    : item.status === 'uploading'
                                      ? `Uploading ${item.progress}%`
                                      : item.status === 'completed'
                                        ? 'Saved'
                                        : item.status}
                                </p>
                              </div>
                              {!busy && item.status !== 'completed' && (
                                <Button
                                  type="button"
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => queue.remove(item.id)}
                                >
                                  Remove
                                </Button>
                              )}
                            </div>
                            {['uploading', 'processing'].includes(item.status) && (
                              <Progress
                                value={item.progress}
                                max={100}
                                aria-label={`Upload progress: ${item.file.name}`}
                                className="h-1"
                              />
                            )}
                            <fieldset
                              disabled={
                                busy ||
                                !!item.checkpoint.id ||
                                ['completed', 'skipped'].includes(item.status)
                              }
                              className="space-y-4"
                            >
                              <MetadataFields
                                value={item.metadata}
                                onChange={(metadata) => queue.setMetadata(item.id, metadata)}
                              />
                            </fieldset>
                            {item.checkpoint.id &&
                              !['completed', 'skipped'].includes(item.status) && (
                                <p className="text-muted-foreground">
                                  Metadata is saved. Retry keeps these values.
                                </p>
                              )}
                            {item.error && (
                              <p role="alert" className="text-destructive">
                                {item.error}
                              </p>
                            )}
                            {item.duplicate && (
                              <p className="text-muted-foreground">
                                Existing: {item.duplicate.title || 'Untitled file'}
                              </p>
                            )}
                            {item.status === 'duplicate' && (
                              <div className="flex gap-2">
                                <Button
                                  type="button"
                                  size="xs"
                                  variant="secondary"
                                  disabled={busy}
                                  onClick={() => runFiles([item.id], true)}
                                >
                                  Upload anyway
                                </Button>
                                <Button
                                  type="button"
                                  size="xs"
                                  variant="ghost"
                                  disabled={busy}
                                  onClick={() => queue.skip(item.id)}
                                >
                                  Skip
                                </Button>
                              </div>
                            )}
                            {item.status === 'failed' && (
                              <Button
                                type="button"
                                size="xs"
                                variant="secondary"
                                disabled={busy}
                                onClick={() => runFiles([item.id])}
                              >
                                Retry
                              </Button>
                            )}
                          </div>
                        </CardContent>
                      </Card>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {error && (
              <p role="alert" className="my-4 text-xs text-destructive">
                {error}
              </p>
            )}
          </DialogBody>
          <DialogPanelFooter>
            {contentType !== 'text' && done ? (
              <Button
                type="button"
                onClick={() => {
                  queue.reset();
                  form.current?.reset();
                  setOpen(false);
                }}
              >
                Done
              </Button>
            ) : (
              <Button
                type="submit"
                disabled={busy || (contentType !== 'text' ? !actionable : !bodyText.trim())}
              >
                {busy ? (
                  <LoadingIcon
                    className="animate-spin motion-reduce:animate-none"
                    aria-hidden="true"
                  />
                ) : (
                  <ArrowUpIcon aria-hidden="true" />
                )}
                {busy ? 'Saving…' : contentType !== 'text' ? 'Save / retry files' : 'Save text'}
              </Button>
            )}
          </DialogPanelFooter>
        </form>
      </DialogPanel>
    </Dialog>
  );
}

function MetadataFields({
  value,
  onChange,
}: {
  value?: UploadMetadata;
  onChange?: (value: UploadMetadata) => void;
}) {
  function field(key: keyof UploadMetadata) {
    return value
      ? {
          value: value[key],
          onChange: (event: ChangeEvent<HTMLInputElement>) =>
            onChange?.({ ...value, [key]: event.target.value }),
        }
      : { name: key };
  }
  return (
    <>
      <label className="block text-xs text-muted-foreground">
        Title (optional)
        <Input className="mt-2" {...field('title')} maxLength={200} placeholder="Give it a name" />
      </label>
      <details>
        <summary className="cursor-pointer text-xs text-muted-foreground">
          Add original source (optional)
        </summary>
        <div className="mt-4 space-y-4">
          <label className="block text-xs text-muted-foreground">
            Source URL
            <Input
              className="mt-2"
              type="url"
              {...field('source_url')}
              maxLength={2048}
              placeholder="https://"
              onInvalid={(event) => {
                const details = event.currentTarget.closest('details');
                if (details) details.open = true;
              }}
            />
          </label>
          <label className="block text-xs text-muted-foreground">
            Source name
            <Input className="mt-2" {...field('source_name')} maxLength={200} />
          </label>
          <label className="block text-xs text-muted-foreground">
            Original creator
            <Input className="mt-2" {...field('source_author')} maxLength={200} />
          </label>
        </div>
      </details>
    </>
  );
}

function UploadPreview({ file }: { file: File }) {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    const objectURL = URL.createObjectURL(file);
    setUrl(objectURL);
    return () => URL.revokeObjectURL(objectURL);
  }, [file]);
  return (
    <div
      className={`flex items-center justify-center overflow-hidden rounded-md bg-muted/30 ${mediaMIME(file).startsWith('audio/') ? 'col-span-full min-h-14' : 'aspect-square'}`}
    >
      {url ? (
        mediaMIME(file).startsWith('audio/') ? (
          <audio
            controls
            preload="none"
            src={url}
            aria-label={`Preview: ${file.name}`}
            className="w-full"
          />
        ) : (
          <img src={url} alt={`Preview: ${file.name}`} className="size-full object-contain" />
        )
      ) : (
        <ImageIcon className="size-5 text-muted-foreground" aria-hidden="true" />
      )}
    </div>
  );
}
