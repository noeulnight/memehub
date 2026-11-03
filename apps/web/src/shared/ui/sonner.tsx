import type { CSSProperties } from 'react';
import { Toaster as Sonner, type ToasterProps } from 'sonner';
import { CheckIcon, CloseIcon, LoadingIcon } from './icons';

export function Toaster(props: ToasterProps) {
  return (
    <Sonner
      theme="dark"
      position="top-center"
      className="toaster group"
      icons={{
        success: <CheckIcon className="size-4" aria-hidden="true" />,
        error: <CloseIcon className="size-4" aria-hidden="true" />,
        loading: <LoadingIcon className="size-4 animate-spin" aria-hidden="true" />,
      }}
      style={
        {
          '--normal-bg': 'var(--popover)',
          '--normal-text': 'var(--popover-foreground)',
          '--normal-border': 'var(--border)',
          '--border-radius': 'var(--radius)',
        } as CSSProperties
      }
      {...props}
    />
  );
}
