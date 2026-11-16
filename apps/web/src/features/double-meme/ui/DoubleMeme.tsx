import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { SafetyPreview, type Meme } from '../../../entities/meme';
import { useApi } from '../../../shared/api/client';
import { copyImage } from '../../../shared/lib/copy-image';
import { Button } from '../../../shared/ui/button';
import { Dialog, DialogTitle, DialogDescription } from '../../../shared/ui/dialog';
import {
  DialogPanel,
  DialogPanelHeader,
  DialogBody,
  DialogPanelFooter,
} from '../../../shared/ui/dialog-layout';
import { CheckIcon, CopyIcon, LinkIcon, SwapIcon } from '../../../shared/ui/icons';
import { doubleMemeURL, getDoubleMeme } from '../api/double';

export function DoubleMeme({
  left,
  right,
  onSwap,
  onClose,
}: {
  left: Meme;
  right: Meme;
  onSwap: () => void;
  onClose: () => void;
}) {
  const api = useApi();
  const [preview, setPreview] = useState('');
  const [copied, setCopied] = useState('');
  const [copyError, setCopyError] = useState('');
  const [revealed, setRevealed] = useState(false);
  const url = doubleMemeURL(
    left,
    right,
    import.meta.env.PUBLIC_APP_BASE_URL || window.location.origin,
    api.defaults.baseURL || '/api',
  );
  const result = useQuery({
    queryKey: ['double-meme', url],
    queryFn: ({ signal }) => getDoubleMeme(api, url, signal),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: false,
  });
  useEffect(() => {
    setPreview('');
    if (!result.data) return;
    const src = URL.createObjectURL(result.data);
    setPreview(src);
    return () => URL.revokeObjectURL(src);
  }, [result.data]);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(''), 2500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const hidden = [left, right].some((meme) => meme.safety_rating === 'nsfw') && !revealed;
  async function copy(kind: 'image' | 'url') {
    setCopyError('');
    try {
      if (kind === 'image') await copyImage(async () => preview);
      else await navigator.clipboard.writeText(url);
      setCopied(kind);
    } catch {
      setCopyError('Could not copy. Try again.');
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogPanel>
        <DialogPanelHeader>
          <DialogTitle>Double meme</DialogTitle>
          <DialogDescription>Copy the media URL to share animated GIFs.</DialogDescription>
        </DialogPanelHeader>
        <DialogBody className="space-y-4">
          {result.isPending && (
            <p role="status" className="text-sm text-muted-foreground">
              Combining images…
            </p>
          )}
          {result.isError && (
            <div role="alert" className="space-y-2 text-sm">
              <p>{result.error.message}</p>
              <Button variant="secondary" onClick={() => void result.refetch()}>
                Try again
              </Button>
            </div>
          )}
          {preview && (
            <SafetyPreview hidden={hidden} onReveal={() => setRevealed(true)}>
              <img
                src={preview}
                width={400}
                height={200}
                alt="Combined double meme"
                className="mx-auto w-full max-w-[400px]"
              />
            </SafetyPreview>
          )}
          {copyError && (
            <p role="alert" className="text-xs text-destructive">
              {copyError}
            </p>
          )}
        </DialogBody>
        <DialogPanelFooter>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Swap images"
            title="Swap images"
            onClick={onSwap}
          >
            <SwapIcon aria-hidden="true" />
          </Button>
          <Button
            variant="secondary"
            title="Copy as a still image"
            disabled={!preview || hidden}
            onClick={() => void copy('image')}
          >
            {copied === 'image' ? (
              <CheckIcon aria-hidden="true" />
            ) : (
              <CopyIcon aria-hidden="true" />
            )}
            Copy image
          </Button>
          <Button disabled={!preview || hidden} onClick={() => void copy('url')}>
            {copied === 'url' ? <CheckIcon aria-hidden="true" /> : <LinkIcon aria-hidden="true" />}
            Copy media URL
          </Button>
        </DialogPanelFooter>
      </DialogPanel>
    </Dialog>
  );
}
