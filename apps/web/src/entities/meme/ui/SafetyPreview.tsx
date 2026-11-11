import type { ReactNode } from 'react';
import { Button } from '../../../shared/ui/button';

export function SafetyPreview({
  hidden,
  onReveal,
  children,
  full = false,
  showControl = true,
}: {
  hidden: boolean;
  onReveal: () => void;
  children: ReactNode;
  full?: boolean;
  showControl?: boolean;
}) {
  if (!hidden) return children;
  return (
    <div className={`relative isolate overflow-hidden ${full ? 'h-full w-full' : ''}`}>
      <div
        inert
        aria-hidden="true"
        className={`pointer-events-none select-none blur-xl ${full ? 'h-full w-full' : ''}`}
      >
        {children}
      </div>
      <div className="absolute inset-0 flex items-center justify-center bg-background/30">
        {showControl && (
          <Button type="button" size="sm" variant="secondary" onClick={onReveal}>
            Show NSFW
          </Button>
        )}
      </div>
    </div>
  );
}
