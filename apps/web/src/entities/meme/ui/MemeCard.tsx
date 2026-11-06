import { useEffect, useState, type ReactNode } from 'react';
import { metadata, siteTitle } from '../model/metadata';
import type { Meme } from '../model/types';
import { useMemeMedia } from '../api/queries';
import { Dialog, DialogTitle } from '../../../shared/ui/dialog';
import {
  DialogPanel,
  DialogBody,
  DialogPanelHeader,
  DialogPanelFooter,
} from '../../../shared/ui/dialog-layout';
import { MemeShare } from './MemeShare';
import { MemeCardInteraction } from './MemeCardInteraction';
import { MemeSave } from './MemeSave';
import { Skeleton } from '../../../shared/ui/skeleton';
import { Button } from '../../../shared/ui/button';
import { AudioIcon, DetailsIcon } from '../../../shared/ui/icons';
import { HoverPreview } from '../../../shared/ui/hover-preview';

function ImagePreview({
  meme,
  full = false,
  eager = false,
}: {
  meme: Meme;
  full?: boolean;
  eager?: boolean;
}) {
  const media = useMemeMedia(meme.id);
  const [failedSrc, setFailedSrc] = useState<string>();
  const [loadedSrc, setLoadedSrc] = useState<string>();
  const src = full ? media.data?.original.url : media.data?.thumbnail.url;
  const failed = (!src && media.isError) || (!!src && failedSrc === src);
  const loaded = !!src && loadedSrc === src;
  const ratio = meme.width && meme.height ? meme.width / meme.height : undefined;
  return (
    <div
      className={
        full
          ? 'relative flex h-full w-full min-h-0 items-center justify-center'
          : 'relative overflow-hidden'
      }
      style={
        !full ? { aspectRatio: ratio ?? (!loaded ? 1 : undefined), maxHeight: '70dvh' } : undefined
      }
      aria-busy={!loaded && !failed}
    >
      {!loaded && !failed && (
        <Skeleton
          className="absolute inset-0 rounded-none motion-reduce:animate-none"
          role="status"
        >
          <span className="sr-only">Loading image…</span>
        </Skeleton>
      )}
      {src && !failed ? (
        <img
          src={src}
          alt={meme.title || meme.description || 'Saved meme'}
          loading={full || eager ? 'eager' : 'lazy'}
          decoding="async"
          width={meme.width ?? undefined}
          height={meme.height ?? undefined}
          onLoad={() => setLoadedSrc(src)}
          draggable={false}
          className={`${full ? 'mx-auto max-h-full max-w-full object-contain' : 'h-auto max-h-[70dvh] w-full object-contain'} ${loaded ? 'opacity-100' : 'opacity-0'}`}
          onError={() => setFailedSrc(src)}
        />
      ) : failed ? (
        <div
          role="status"
          className="flex min-h-24 items-center justify-center text-center text-xs text-muted-foreground"
        >
          Image preview unavailable
        </div>
      ) : null}
    </div>
  );
}

function AudioPreview({ meme }: { meme: Meme }) {
  const media = useMemeMedia(meme.id);
  const [failedSrc, setFailedSrc] = useState<string>();
  const src = media.data?.original.url;
  return (
    <div className="min-w-0 space-y-2">
      {src && (
        <audio
          controls
          preload="none"
          src={src}
          aria-label={meme.title || 'Audio meme'}
          className="w-full"
          onError={() => setFailedSrc(src)}
        />
      )}
      {!src && !media.isError && (
        <p role="status" className="text-xs text-muted-foreground">
          Loading audio…
        </p>
      )}
      {(media.isError || (!!src && failedSrc === src)) && (
        <p role="alert" className="text-xs text-destructive">
          Audio unavailable. Try opening the media URL.
        </p>
      )}
    </div>
  );
}

export function MemeCard({
  meme,
  index,
  onTagSearch,
  onUserSearch,
  onSave,
  manage,
  analysis,
  detailOnly = false,
  onClose,
}: {
  meme: Meme;
  index: number;
  onTagSearch: (tag: string) => void;
  onUserSearch: (username: string) => void;
  onSave: () => void;
  manage?: (props: {
    content: ReactNode;
    actions: ReactNode;
    onDeleted: () => void;
    onPendingChange: (pending: boolean) => void;
  }) => ReactNode;
  analysis?: ReactNode;
  detailOnly?: boolean;
  onClose?: () => void;
}) {
  const [open, setOpen] = useState(detailOnly);
  const [busy, setBusy] = useState(false);
  const [actionsTarget, setActionsTarget] = useState<HTMLDivElement | null>(null);
  const [hovered, setHovered] = useState(false);
  const [optionsVisible, setOptionsVisible] = useState(false);
  useEffect(() => {
    if (!open) return;
    const previous = document.title;
    document.title = metadata(window.location.origin, '/api', meme).title;
    return () => {
      document.title = detailOnly ? siteTitle : previous;
    };
  }, [open, meme, detailOnly]);
  function changeOpen(next: boolean) {
    if (busy) return;
    setOpen(next);
    if (!next) onClose?.();
  }
  const content = (
    <>
      <DialogPanelHeader>
        <DialogTitle>
          {meme.title ||
            (meme.content_type === 'image'
              ? 'Image details'
              : meme.content_type === 'audio'
                ? 'Audio details'
                : 'Text details')}
        </DialogTitle>
      </DialogPanelHeader>
      <DialogBody className="space-y-5 break-words">
        {analysis}
        {meme.content_type === 'audio' && <AudioPreview meme={meme} />}
        <p className="text-xs text-muted-foreground">
          {meme.created_by_name || 'Unknown uploader'}
          {meme.created_by_username && (
            <button
              type="button"
              className="ml-1 hover:underline"
              onClick={() => {
                changeOpen(false);
                onUserSearch(meme.created_by_username!);
              }}
            >
              @{meme.created_by_username}
            </button>
          )}
          {' · '}
          <time dateTime={meme.created_at}>
            {new Date(meme.created_at).toLocaleString('en-US', {
              year: 'numeric',
              month: 'short',
              day: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
              timeZoneName: 'short',
            })}
          </time>
        </p>
        {meme.description && (
          <p className="whitespace-pre-wrap text-sm text-muted-foreground">{meme.description}</p>
        )}
        {meme.content_type === 'text' && (
          <p className="whitespace-pre-wrap text-sm leading-relaxed">{meme.body_text}</p>
        )}
        <section aria-label="Tags" className="space-y-2">
          <h3 className="text-xs font-medium text-muted-foreground">Tags</h3>
          {meme.tags.length ? (
            <ul className="flex flex-wrap gap-x-3 gap-y-1 text-sm">
              {meme.tags.map((tag) => (
                <li key={tag}>
                  <button
                    type="button"
                    className="cursor-pointer hover:underline focus-visible:underline"
                    onClick={() => {
                      changeOpen(false);
                      onTagSearch(tag);
                    }}
                  >
                    #{tag}
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">No tags yet.</p>
          )}
        </section>
        {meme.extracted_text && (
          <section className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">Extracted text</h3>
            <p className="whitespace-pre-wrap text-sm">{meme.extracted_text}</p>
          </section>
        )}
        {(meme.source_name || meme.source_author || meme.source_url) && (
          <section className="space-y-2">
            <h3 className="text-xs font-medium text-muted-foreground">Source</h3>
            <div className="space-y-1 text-xs text-muted-foreground">
              {meme.source_name && <p>{meme.source_name}</p>}
              {meme.source_author && <p>{meme.source_author}</p>}
              {meme.source_url && (
                <a
                  href={meme.source_url}
                  target="_blank"
                  rel="noreferrer"
                  className="underline underline-offset-4"
                >
                  View source
                </a>
              )}
            </div>
          </section>
        )}
      </DialogBody>
    </>
  );
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      {!detailOnly &&
        (meme.content_type === 'audio' ? (
          <article className="meme-card min-w-0 space-y-3 py-2">
            <p className="flex items-center gap-2 text-sm">
              <AudioIcon aria-hidden="true" />
              {meme.title || 'Audio meme'}
            </p>
            {!open && <AudioPreview meme={meme} />}
            <div className="flex justify-end gap-1">
              <Button
                size="icon-sm"
                variant="secondary"
                aria-label="View audio details"
                onClick={() => changeOpen(true)}
              >
                <DetailsIcon aria-hidden="true" />
              </Button>
              <MemeShare meme={meme} />
              <MemeSave onSave={onSave} />
            </div>
          </article>
        ) : (
          <HoverPreview
            disabled={open}
            actionsRef={setActionsTarget}
            onHoverChange={setHovered}
            expandedHitArea={optionsVisible}
          >
            <article
              className="meme-card min-w-0"
              style={{ animationDelay: `${Math.min(index, 8) * 25}ms` }}
            >
              <MemeCardInteraction
                meme={meme}
                onDetails={() => changeOpen(true)}
                onSave={onSave}
                optionsTarget={actionsTarget}
                hovered={hovered}
                onOptionsChange={setOptionsVisible}
              >
                <div className="meme-visual pointer-events-none bg-background">
                  {meme.content_type === 'image' ? (
                    <ImagePreview meme={meme} eager={index < 24} />
                  ) : (
                    <p className="line-clamp-6 whitespace-pre-wrap break-words text-sm leading-snug">
                      {meme.body_text}
                    </p>
                  )}
                </div>
              </MemeCardInteraction>
            </article>
          </HoverPreview>
        ))}
      <DialogPanel
        size={meme.content_type === 'image' ? 'wide' : 'default'}
        aria-describedby={undefined}
      >
        <div
          className={
            meme.content_type === 'image'
              ? 'grid min-h-0 flex-1 grid-rows-[minmax(0,2fr)_minmax(0,3fr)] sm:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] sm:grid-rows-1'
              : 'flex min-h-0 flex-1 flex-col'
          }
        >
          {meme.content_type === 'image' && (
            <div className="flex min-h-0 min-w-0 items-center justify-center overflow-hidden p-6">
              <ImagePreview meme={meme} full />
            </div>
          )}
          {manage ? (
            manage({
              content,
              actions: (
                <>
                  <MemeShare meme={meme} />
                  <MemeSave onSave={onSave} />
                </>
              ),
              onDeleted: () => changeOpen(false),
              onPendingChange: setBusy,
            })
          ) : (
            <div className="flex min-h-0 min-w-0 flex-col">
              {content}
              <DialogPanelFooter>
                <MemeShare meme={meme} />
                <MemeSave onSave={onSave} />
              </DialogPanelFooter>
            </div>
          )}
        </div>
      </DialogPanel>
    </Dialog>
  );
}
