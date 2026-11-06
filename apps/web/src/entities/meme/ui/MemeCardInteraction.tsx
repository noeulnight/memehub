import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import type { Meme } from '../model/types';
import { getMedia } from '../api/queries';
import { useApi } from '../../../shared/api/client';
import { copyImage } from '../../../shared/lib/copy-image';
import { animatePress } from '../../../shared/lib/animate-press';
import { useDesktopHover } from '../../../shared/lib/desktop-hover';
import { Button } from '../../../shared/ui/button';
import { CopyIcon, DetailsIcon, CheckIcon, LoadingIcon } from '../../../shared/ui/icons';
import { MemeShare } from './MemeShare';
import { MemeSave } from './MemeSave';

export function MemeCardInteraction({
  meme,
  children,
  onDetails,
  onSave,
  optionsTarget,
  hovered,
  onOptionsChange,
}: {
  meme: Meme;
  children: ReactNode;
  onDetails: () => void;
  onSave: () => void;
  optionsTarget?: HTMLDivElement | null;
  hovered: boolean;
  onOptionsChange?: (open: boolean) => void;
}) {
  const api = useApi();
  const desktop = useDesktopHover();
  const tooltipId = useId();
  const tooltip = useRef<HTMLParagraphElement>(null);
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [optionsClosing, setOptionsClosing] = useState(false);
  const [shareMenuOpen, setShareMenuOpen] = useState(false);
  const [status, setStatus] = useState<'idle' | 'copying' | 'copied' | 'failed'>('idle');
  const [feedback, setFeedback] = useState(0);
  const container = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const start = useRef<{ x: number; y: number } | undefined>(undefined);
  const suppressClick = useRef(false);

  useLayoutEffect(() => {
    onOptionsChange?.(desktop && optionsOpen && !optionsClosing);
  }, [desktop, optionsOpen, optionsClosing, onOptionsChange]);

  useLayoutEffect(() => {
    const target = container.current;
    const tip = tooltip.current;
    if (!desktop || !hovered || !target || !tip) return;
    const position = () => {
      const rect = target.getBoundingClientRect();
      const scale = Math.max(
        1,
        Math.min(1.5, (innerWidth - 32) / rect.width, (innerHeight - 32) / rect.height),
      );
      const width = rect.width * scale;
      const height = rect.height * scale;
      const left = Math.max(
        16,
        Math.min(rect.left + (rect.width - width) / 2, innerWidth - width - 16),
      );
      const top = Math.max(
        16,
        Math.min(rect.top + (rect.height - height) / 2, innerHeight - height - 16),
      );
      const x = Math.max(
        16 + tip.offsetWidth / 2,
        Math.min(left + width / 2, innerWidth - 16 - tip.offsetWidth / 2),
      );
      const y = Math.max(12, Math.min(top + height + 8, innerHeight - tip.offsetHeight - 12));
      tip.style.left = `${x}px`;
      tip.style.top = `${y}px`;
    };
    position();
    const observer = new ResizeObserver(position);
    observer.observe(target);
    return () => observer.disconnect();
  }, [desktop, hovered]);

  function cancelPress() {
    clearTimeout(timer.current);
    timer.current = undefined;
    start.current = undefined;
  }

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    if (hovered) {
      setOptionsClosing(false);
      return;
    }
    cancelPress();
    if (!optionsOpen) return;
    setOptionsClosing(true);
    const reset = setTimeout(() => {
      setOptionsOpen(false);
      setOptionsClosing(false);
    }, 180);
    return () => clearTimeout(reset);
  }, [hovered, optionsOpen]);
  useEffect(() => {
    if (status !== 'failed' && status !== 'copied') return;
    const reset = setTimeout(() => setStatus('idle'), 2000);
    return () => clearTimeout(reset);
  }, [status]);

  useEffect(() => {
    if (!feedback) return;
    const reset = setTimeout(() => setFeedback(0), 1200);
    return () => clearTimeout(reset);
  }, [feedback]);

  function changeOptions(open: boolean) {
    cancelPress();
    setOptionsClosing(false);
    setOptionsOpen(open && hovered);
  }

  useEffect(() => {
    if (!optionsOpen) return;
    const dismiss = (event: PointerEvent) => {
      const target = event.target;
      if (
        target instanceof Element &&
        !container.current?.contains(target) &&
        !optionsTarget?.contains(target) &&
        !target.closest('[data-slot="dropdown-menu-content"]')
      ) {
        setOptionsOpen(false);
      }
    };
    document.addEventListener('pointerdown', dismiss);
    return () => document.removeEventListener('pointerdown', dismiss);
  }, [optionsOpen, optionsTarget]);

  async function copy() {
    if (desktop && optionsTarget) setFeedback((value) => value + 1);
    if (status === 'copying') return;
    setStatus('copying');
    try {
      if (meme.content_type === 'image') {
        await copyImage(async () => (await getMedia(api, meme.id)).original.url);
      } else if (meme.content_type === 'audio') {
        await navigator.clipboard.writeText((await getMedia(api, meme.id)).original.url);
      } else {
        await navigator.clipboard.writeText(meme.body_text || '');
      }
      setStatus(desktop ? 'idle' : 'copied');
    } catch {
      setStatus('failed');
    }
  }

  const options = (
    <div
      data-closing={optionsClosing || !hovered}
      inert={optionsClosing || !hovered}
      aria-hidden={optionsClosing || !hovered || undefined}
      className="meme-options-overlay pointer-events-none absolute inset-0 flex items-center justify-center bg-black/50"
    >
      <div
        role="group"
        aria-label="Meme options"
        className="meme-options pointer-events-auto flex items-center justify-center gap-1"
      >
        <Button
          type="button"
          variant="secondary"
          size="icon-sm"
          aria-label={`Copy ${meme.content_type}`}
          title={`Copy ${meme.content_type}`}
          disabled={status === 'copying'}
          onClick={(event) => {
            animatePress(event.currentTarget);
            void copy();
          }}
        >
          <CopyIcon aria-hidden="true" />
        </Button>
        <Button
          type="button"
          variant="secondary"
          size="icon-sm"
          aria-label={`View details: ${meme.title || 'Meme'}`}
          title="View details"
          onClick={() => {
            changeOptions(false);
            onDetails();
          }}
        >
          <DetailsIcon aria-hidden="true" />
        </Button>
        <MemeShare meme={meme} onOpenChange={setShareMenuOpen} />
        <MemeSave
          onSave={() => {
            changeOptions(false);
            onSave();
          }}
        />
      </div>
    </div>
  );

  const overlay = feedback ? (
    <div
      key={feedback}
      role="status"
      className="meme-copy-feedback pointer-events-none absolute inset-0 flex items-center justify-center bg-black/50 text-sm font-medium text-white"
    >
      <span className="meme-copy-label text-sm">Copied!</span>
    </div>
  ) : optionsOpen || shareMenuOpen ? (
    options
  ) : null;

  if (!desktop) {
    if (meme.content_type === 'text') {
      return (
        <button
          type="button"
          className="block w-full text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-label={`View details: ${meme.title || 'Text'}`}
          onClick={onDetails}
        >
          {children}
        </button>
      );
    }
    return (
      <div className="relative">
        {children}
        <div className="absolute inset-x-0 bottom-0 flex flex-col items-end gap-1 bg-gradient-to-t from-black/80 to-transparent p-1.5 pt-6">
          <div
            role="group"
            aria-label="Meme actions"
            className="grid w-full max-w-[140px] grid-cols-4 gap-1 [&>button]:aspect-square [&>button]:h-auto [&>button]:w-full"
          >
            <Button
              type="button"
              variant="secondary"
              size="icon-sm"
              aria-label={status === 'copied' ? 'Image copied' : 'Copy image'}
              title="Copy image (animated images copy as still images)"
              disabled={status === 'copying'}
              onClick={() => void copy()}
            >
              {status === 'copying' ? (
                <LoadingIcon className="animate-spin" aria-hidden="true" />
              ) : status === 'copied' ? (
                <CheckIcon aria-hidden="true" />
              ) : (
                <CopyIcon aria-hidden="true" />
              )}
            </Button>
            <Button
              type="button"
              variant="secondary"
              size="icon-sm"
              aria-label={`View details: ${meme.title || 'Image'}`}
              title="View details"
              onClick={onDetails}
            >
              <DetailsIcon aria-hidden="true" />
            </Button>
            <MemeShare meme={meme} />
            <MemeSave onSave={onSave} />
          </div>
          {status === 'failed' && (
            <span role="alert" className="w-full text-right text-xs text-white">
              Could not copy. Try again.
            </span>
          )}
        </div>
      </div>
    );
  }

  return (
    <div
      ref={container}
      className="relative"
      onKeyDown={(event) => {
        if (event.key === 'Escape') changeOptions(false);
      }}
    >
      <button
        type="button"
        className="relative block w-full select-none text-left outline-none focus-visible:ring-2 focus-visible:ring-ring [-webkit-touch-callout:none]"
        aria-label={`Copy ${meme.content_type}: ${meme.title || 'Meme'}`}
        aria-describedby={tooltipId}
        data-meme-copy-trigger=""
        aria-busy={status === 'copying'}
        onPointerDown={(event) => {
          if (event.button !== 0 || !event.isPrimary) return;
          event.currentTarget.focus({ preventScroll: true });
          cancelPress();
          suppressClick.current = false;
          start.current = { x: event.clientX, y: event.clientY };
          timer.current = setTimeout(() => {
            suppressClick.current = true;
            changeOptions(true);
          }, 500);
        }}
        onPointerMove={(event) => {
          if (
            start.current &&
            Math.hypot(event.clientX - start.current.x, event.clientY - start.current.y) > 8
          ) {
            suppressClick.current = true;
            cancelPress();
          }
        }}
        onPointerUp={cancelPress}
        onPointerCancel={() => {
          suppressClick.current = true;
          cancelPress();
        }}
        onPointerLeave={() => {
          if (start.current) suppressClick.current = true;
          cancelPress();
        }}
        onClick={() => {
          if (suppressClick.current) {
            suppressClick.current = false;
            return;
          }
          void copy();
        }}
        onContextMenu={(event) => {
          event.preventDefault();
          suppressClick.current = true;
          changeOptions(true);
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            if (!event.repeat) {
              void copy();
            }
          } else if (
            event.key === 'ArrowDown' ||
            event.key === 'ContextMenu' ||
            (event.shiftKey && event.key === 'F10')
          ) {
            event.preventDefault();
            changeOptions(true);
          }
        }}
      >
        {children}
        {status === 'failed' && (
          <span
            role="status"
            className="pointer-events-none absolute inset-x-0 bottom-0 bg-black/75 px-2 py-1 text-center text-xs text-white"
          >
            Could not copy. Try again.
          </span>
        )}
      </button>
      {createPortal(
        <p
          ref={tooltip}
          id={tooltipId}
          role="tooltip"
          data-visible={hovered}
          className="meme-hover-tooltip"
        >
          Click to copy · Hold for options
        </p>,
        document.body,
      )}
      {overlay && optionsTarget && createPortal(overlay, optionsTarget)}
    </div>
  );
}
