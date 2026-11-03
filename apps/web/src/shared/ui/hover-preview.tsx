import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type ReactElement,
  type Ref,
} from 'react';
import { animatePress } from '../lib/animate-press';
import { desktopHover } from '../lib/desktop-hover';

export function HoverPreview({
  children,
  disabled = false,
  actionsRef,
  onHoverChange,
  expandedHitArea = false,
}: {
  children: ReactElement;
  disabled?: boolean;
  actionsRef?: Ref<HTMLDivElement>;
  onHoverChange?: (hovered: boolean) => void;
  expandedHitArea?: boolean;
}) {
  const [hovered, setHovered] = useState(false);
  const [pressed, setPressed] = useState(false);
  const [closing, setClosing] = useState(false);
  const anchor = useRef<HTMLDivElement>(null);
  const active = hovered && !disabled;

  useLayoutEffect(() => {
    onHoverChange?.(active);
  }, [active, onHoverChange]);

  const close = useCallback(() => {
    setHovered(false);
    setPressed(false);
    setClosing(true);
  }, []);

  useEffect(() => {
    if (!closing) return;
    const timer = setTimeout(() => setClosing(false), 180);
    return () => clearTimeout(timer);
  }, [closing]);

  useEffect(() => {
    if (disabled) close();
  }, [disabled, close]);

  useEffect(() => {
    if (!active) return;
    const dismiss = close;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') dismiss();
    };
    window.addEventListener('resize', dismiss);
    window.addEventListener('scroll', dismiss, true);
    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('resize', dismiss);
      window.removeEventListener('scroll', dismiss, true);
      window.removeEventListener('keydown', onKeyDown);
    };
  }, [active, close]);

  useEffect(() => {
    if (!active || !expandedHitArea) return;
    const track = (event: PointerEvent) => {
      if (event.pointerType !== 'mouse') return;
      const rect = anchor.current?.querySelector('.meme-visual')?.getBoundingClientRect();
      if (
        rect &&
        (event.clientX < rect.left ||
          event.clientX > rect.right ||
          event.clientY < rect.top ||
          event.clientY > rect.bottom)
      )
        close();
    };
    window.addEventListener('pointermove', track);
    return () => window.removeEventListener('pointermove', track);
  }, [active, expandedHitArea, close]);

  useLayoutEffect(() => {
    const target = anchor.current;
    if (!active || !target) return;
    const position = () => {
      const rect = target.getBoundingClientRect();
      const scale = Math.max(
        1,
        Math.min(1.5, (innerWidth - 32) / rect.width, (innerHeight - 32) / rect.height),
      );
      const width = rect.width * scale;
      const height = rect.height * scale;
      const left = rect.left + (rect.width - width) / 2;
      const top = rect.top + (rect.height - height) / 2;
      const x = Math.max(16, Math.min(left, innerWidth - width - 16)) - left;
      const y = Math.max(16, Math.min(top, innerHeight - height - 16)) - top;
      target.style.setProperty('--meme-hover-scale', String(scale));
      target.style.setProperty('--meme-hover-x', `${x}px`);
      target.style.setProperty('--meme-hover-y', `${y}px`);
    };
    position();
    const observer = new ResizeObserver(position);
    observer.observe(target);
    return () => observer.disconnect();
  }, [active]);

  const press = () => {
    if (!window.matchMedia(desktopHover).matches) return;
    const visual = anchor.current?.querySelector<HTMLElement>('.meme-visual');
    if (visual) animatePress(visual, { dim: active });
  };

  function syncHover(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.pointerType !== 'mouse' || disabled || !window.matchMedia(desktopHover).matches)
      return;
    const rect = expandedHitArea
      ? (event.currentTarget.querySelector('.meme-visual')?.getBoundingClientRect() ??
        event.currentTarget.getBoundingClientRect())
      : event.currentTarget.getBoundingClientRect();
    const inside =
      event.clientX >= rect.left &&
      event.clientX <= rect.right &&
      event.clientY >= rect.top &&
      event.clientY <= rect.bottom;
    if (inside) {
      if (!active) {
        setClosing(false);
        setHovered(true);
      }
    } else if (active) {
      close();
    }
  }

  return (
    <div
      ref={anchor}
      data-hovered={active}
      data-pressed={pressed}
      data-options-visible={expandedHitArea}
      className={`meme-hover relative min-w-0 ${active ? 'z-40' : closing ? 'z-30' : ''}`}
      onPointerEnter={syncHover}
      onPointerMoveCapture={syncHover}
      onPointerLeave={(event) => {
        setPressed(false);
        const rect = expandedHitArea
          ? event.currentTarget.querySelector('.meme-visual')?.getBoundingClientRect()
          : undefined;
        if (
          rect &&
          event.clientX >= rect.left &&
          event.clientX <= rect.right &&
          event.clientY >= rect.top &&
          event.clientY <= rect.bottom
        )
          return;
        close();
      }}
      onPointerDownCapture={(event) => {
        if (event.button === 0 && window.matchMedia(desktopHover).matches) setPressed(true);
      }}
      onPointerUpCapture={() => setPressed(false)}
      onPointerCancelCapture={() => {
        setPressed(false);
        close();
      }}
      onClickCapture={press}
      onKeyDownCapture={(event) => {
        if (!event.repeat && (event.key === 'Enter' || event.key === ' ')) press();
      }}
    >
      {children}
      <div
        ref={actionsRef}
        className="meme-hover-actions pointer-events-none absolute inset-0 z-10"
      />
    </div>
  );
}
